package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
)

func generatedMetadata(id string) PublicClip {
	return PublicClip{ID: "generated-" + id, Revision: id, Title: "Generated basketball", Sport: "basketball", Description: "Source attribution / CC BY 4.0", Tags: []string{"basketball", "4DGS"}, PreviewURL: "https://media.test/preview.webm", SceneURL: "https://media.test/generated/" + id + "/scene.json"}
}
func TestPublishedClipsUseCanonicalSearchPagingValidatorsAndStaticTransport(t *testing.T) {
	base := mustHTTP(t, fixtureJSON(t, 3), "https://media.example.test")
	rows := []PublicClip{}
	reads := 0
	load := func(context.Context) ([]PublicClip, error) { reads++; return rows, nil }
	handler, err := WithPublicClips(base, load)
	if err != nil {
		t.Fatal(err)
	}
	response, before := request(t, handler, "GET", feedPath+"?limit=1", nil)
	previousETag := response.Header.Get("ETag")
	var page struct {
		Clips      []PublicClip `json:"clips"`
		NextCursor string       `json:"next_cursor"`
	}
	if err := json.Unmarshal(before, &page); err != nil {
		t.Fatal(err)
	}
	oldCursor := page.NextCursor
	rows = []PublicClip{generatedMetadata(strings.Repeat("b", 32)), generatedMetadata(strings.Repeat("a", 32))}
	response, body := request(t, handler, "GET", feedPath+"?limit=1", map[string]string{"If-None-Match": previousETag, "Cache-Control": "no-cache"})
	if response.StatusCode != 200 || response.Header.Get("ETag") == previousETag {
		t.Fatal("publication reused the old feed validator")
	}
	if err := json.Unmarshal(body, &page); err != nil || len(page.Clips) != 1 || page.Clips[0].ID != "generated-"+strings.Repeat("a", 32) {
		t.Fatalf("not canonical: %s", body)
	}
	if strings.Contains(string(body), `"codec"`) || strings.Contains(string(body), `"manifest_url"`) {
		t.Fatal("generated metadata fabricated a transport manifest")
	}
	response, _ = request(t, handler, "GET", feedPath+"?limit=1&cursor="+oldCursor, nil)
	if response.StatusCode != 400 {
		t.Fatal("publication accepted the old snapshot cursor")
	}
	response, body = request(t, handler, "GET", feedPath+"?q=generated&tag=4dgs&limit=1", nil)
	if err := json.Unmarshal(body, &page); err != nil || len(page.Clips) != 1 || page.NextCursor == "" {
		t.Fatalf("search: %s", body)
	}
	response, body = request(t, handler, "GET", feedPath+"?q=generated&tag=4dgs&limit=1&cursor="+page.NextCursor, nil)
	page.NextCursor = ""
	if err := json.Unmarshal(body, &page); err != nil || len(page.Clips) != 1 || page.NextCursor != "" {
		t.Fatalf("last search page: %s", body)
	}
	firstResponse, firstBody := request(t, handler, "GET", feedPath, nil)
	slices.Reverse(rows)
	restarted, err := WithPublicClips(mustHTTP(t, fixtureJSON(t, 3), "https://media.example.test"), load)
	if err != nil {
		t.Fatal(err)
	}
	secondResponse, secondBody := request(t, restarted, "GET", feedPath, nil)
	if firstBodyString, secondBodyString := string(firstBody), string(secondBody); firstBodyString != secondBodyString || firstResponse.Header.Get("ETag") != secondResponse.Header.Get("ETag") {
		t.Fatal("instances disagreed on canonical published snapshot")
	}
	response, _ = request(t, handler, "GET", feedPath, map[string]string{"If-None-Match": firstResponse.Header.Get("ETag")})
	if response.StatusCode != 304 || !strings.HasPrefix(response.Header.Get("Cache-Control"), "public,") {
		t.Fatal("shared public caching contract changed")
	}
	response, body = request(t, handler, "GET", feedPath+"/clip-0", nil)
	if response.StatusCode != 200 || !strings.Contains(string(body), `"manifest_url"`) {
		t.Fatal("static transport metadata changed")
	}
	rows = nil
	response, body = request(t, handler, "GET", feedPath+"?limit=1", nil)
	if string(body) != string(before) || response.Header.Get("ETag") != previousETag {
		t.Fatal("unpublication did not restore the static snapshot")
	}
	if reads < 9 {
		t.Fatal("origin validation used stale process-local registry")
	}
}
func TestRegistryErrorsAndOversizedOrDuplicateSetsNeverLookLikeAnEmptyFeed(t *testing.T) {
	first := generatedMetadata(strings.Repeat("a", 32))
	for _, tc := range []struct {
		rows []PublicClip
		err  error
	}{
		{err: errors.New("database unavailable")}, {rows: []PublicClip{first, first}},
		{rows: make([]PublicClip, 11)},
	} {
		h, err := WithPublicClips(mustHTTP(t, fixtureJSON(t, 1), ""), func(context.Context) ([]PublicClip, error) { return tc.rows, tc.err })
		if err != nil {
			t.Fatal(err)
		}
		response, body := request(t, h, "GET", feedPath, nil)
		if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get("Cache-Control") != "no-store" || strings.Contains(string(body), `"clips"`) {
			t.Fatalf("registry failure hidden: %d %s", response.StatusCode, body)
		}
	}
}
