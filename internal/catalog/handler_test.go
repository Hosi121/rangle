package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestResponseBytesMatchJSONEncoding(t *testing.T) {
	var source struct {
		Version int    `json:"version"`
		Clips   []clip `json:"clips"`
	}
	if err := json.Unmarshal([]byte(fixtureJSON(t, 55)), &source); err != nil {
		t.Fatal(err)
	}
	origin := "https://media.example.test"
	views := make([]clipView, len(source.Clips))
	for i := range source.Clips {
		entry := &source.Clips[i]
		entry.Title = "シュート <>&\"\\\n" + strconv.Itoa(i)
		entry.Description = "改行\n区切り\u2028\u2029と\tタブ"
		preview := origin + entry.PreviewURL
		if i%2 == 0 {
			entry.PreviewURL = "https://other.example.test/preview.webm?lang=ja&cut=1"
			preview = entry.PreviewURL
			entry.SceneURL = "/assets/" + entry.Manifest.ClipID + "/v1/scene.json"
		}
		scene := ""
		if entry.SceneURL != "" {
			scene = origin + entry.SceneURL
		}
		views[i] = clipView{
			ID: entry.Manifest.ClipID, Revision: entry.Manifest.Revision,
			Title: entry.Title, Sport: entry.Sport, Description: entry.Description,
			Codec: entry.Manifest.Codec, ManifestURL: origin + entry.Manifest.URL(),
			PreviewURL: preview, SceneURL: scene,
			Tags: []string{strings.ToLower(entry.Sport)},
		}
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	h := mustHTTP(t, string(encoded), origin)
	for _, limit := range []int{1, 2, 10, 50} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			cursor := ""
			for start := 0; start < len(views); start += limit {
				target := fmt.Sprintf("%s?limit=%d", feedPath, limit)
				if cursor != "" {
					target += "&cursor=" + url.QueryEscape(cursor)
				}
				response, body := request(t, h, http.MethodGet, target, nil)
				var page struct {
					Clips      []clipView `json:"clips"`
					NextCursor string     `json:"next_cursor,omitempty"`
				}
				if err := json.Unmarshal(body, &page); err != nil || response.StatusCode != http.StatusOK {
					t.Fatalf("page at %d: status=%d error=%v", start, response.StatusCode, err)
				}
				end := min(start+limit, len(views))
				if (end < len(views)) != (page.NextCursor != "") {
					t.Fatalf("unexpected page end at %d", end)
				}
				page.Clips = views[start:end]
				want, err := json.Marshal(page)
				if err != nil || !bytes.Equal(body, want) {
					t.Fatalf("page at %d differs from JSON encoding: error=%v", start, err)
				}
				cursor = page.NextCursor
			}
		})
	}
	for _, view := range views {
		response, body := request(t, h, http.MethodGet, feedPath+"/"+view.ID, nil)
		want, err := json.Marshal(view)
		if err != nil || response.StatusCode != http.StatusOK || !bytes.Equal(body, want) {
			t.Fatalf("detail %s differs from JSON encoding: error=%v", view.ID, err)
		}
	}
}

func TestResponseValidatorsAndRanges(t *testing.T) {
	h := mustHTTP(t, fixtureJSON(t, 15), "https://media.example.test")
	for _, target := range []string{feedPath, feedPath + "/clip-0"} {
		response, body := request(t, h, http.MethodGet, target, nil)
		sum := sha256.Sum256(body)
		etag := `"sha256-` + hex.EncodeToString(sum[:]) + `"`
		if response.StatusCode != http.StatusOK || response.Header.Get("ETag") != etag {
			t.Fatalf("%s: ETag does not identify the exact response bytes", target)
		}
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			cached, data := request(t, h, method, target, map[string]string{"If-None-Match": etag})
			if cached.StatusCode != http.StatusNotModified || len(data) != 0 || cached.Header.Get("ETag") != etag {
				t.Fatalf("%s %s: invalid conditional response", method, target)
			}
		}
		partial, data := request(t, h, http.MethodGet, target, map[string]string{"Range": "bytes=1-17", "If-Range": etag})
		if partial.StatusCode != http.StatusPartialContent || !bytes.Equal(data, body[1:18]) || partial.Header.Get("ETag") != etag {
			t.Fatalf("%s: range does not preserve snapshot bytes", target)
		}
		failed, _ := request(t, h, http.MethodGet, target, map[string]string{"If-Match": `"other"`})
		if failed.StatusCode != http.StatusPreconditionFailed || failed.Header.Get("Cache-Control") != "no-store" || failed.Header.Get("ETag") != "" {
			t.Fatalf("%s: precondition failure advertised cached content", target)
		}
	}
}

func TestFeedPaginationAndConditionalRequests(t *testing.T) {
	h := mustHTTP(t, fixtureJSON(t, 3), "https://media.example.test/")
	response, body := request(t, h, "GET", feedPath+"?limit=2", nil)
	if response.StatusCode != 200 || response.Header.Get("Cache-Control") != "public, max-age=15, stale-while-revalidate=30" {
		t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
	}
	var first struct {
		Clips      []clipView `json:"clips"`
		NextCursor string     `json:"next_cursor"`
	}
	if err := json.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Clips) != 2 || first.Clips[0].ID != "clip-0" || first.Clips[1].ID != "clip-1" || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	if first.Clips[0].ManifestURL != "https://media.example.test/manifests/clip-0/synthetic-v1.json" || first.Clips[0].PreviewURL != "https://media.example.test/preview.webm" {
		t.Fatalf("origin resolution: %+v", first.Clips[0])
	}
	_, nextBody := request(t, h, "GET", feedPath+"?limit=2&cursor="+url.QueryEscape(first.NextCursor), nil)
	var last struct {
		Clips      []clipView `json:"clips"`
		NextCursor string     `json:"next_cursor"`
	}
	if err := json.Unmarshal(nextBody, &last); err != nil {
		t.Fatal(err)
	}
	if len(last.Clips) != 1 || last.Clips[0].ID != "clip-2" || last.NextCursor != "" {
		t.Fatalf("last page: %+v", last)
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	for _, method := range []string{"GET", "HEAD"} {
		notModified, data := request(t, h, method, feedPath+"?limit=2", map[string]string{"If-None-Match": "W/" + etag})
		if notModified.StatusCode != 304 || len(data) != 0 || notModified.Header.Get("ETag") != etag || notModified.Header.Get("Cache-Control") == "" {
			t.Fatalf("conditional %s: %v body=%q", method, notModified, data)
		}
	}
	head, data := request(t, h, "HEAD", feedPath+"?limit=2", nil)
	if head.StatusCode != 200 || len(data) != 0 || head.Header.Get("Content-Length") != response.Header.Get("Content-Length") {
		t.Fatalf("HEAD: %v body=%q", head, data)
	}
	detail, data := request(t, h, "GET", feedPath+"/clip-0", nil)
	var found clipView
	if err := json.Unmarshal(data, &found); err != nil || detail.StatusCode != 200 || !reflect.DeepEqual(found, first.Clips[0]) {
		t.Fatalf("detail: %+v error=%v", found, err)
	}

	// A cursor cannot combine pages from different catalogs or delivery origins.
	for _, replacement := range []http.Handler{
		mustHTTP(t, strings.Replace(fixtureJSON(t, 3), `"title":"clip 0"`, `"title":"updated"`, 1), "https://media.example.test"),
		mustHTTP(t, fixtureJSON(t, 3), "https://other.example.test"),
	} {
		response, _ := request(t, replacement, "GET", feedPath+"?cursor="+first.NextCursor, nil)
		if response.StatusCode != 400 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("stale cursor accepted")
		}
	}
}

func TestFeedErrorsAndEmptyCatalog(t *testing.T) {
	h := mustHTTP(t, fixtureJSON(t, 1), "")
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"GET", feedPath + "?limit=0", 400}, {"GET", feedPath + "?limit=51", 400},
		{"GET", feedPath + "?limit=abc", 400}, {"GET", feedPath + "?limit=1&limit=2", 400},
		{"GET", feedPath + "?unknown=1", 400}, {"GET", feedPath + "?cursor=", 400},
		{"GET", feedPath + "?cursor=broken", 400}, {"GET", feedPath + "?cursor=" + strings.Repeat("a", 129), 400},
		{"GET", feedPath + "?limit=%xx", 400}, {"GET", feedPath + "/clip-0?limit=1", 400},
		{"GET", feedPath + "/unknown", 404}, {"HEAD", feedPath + "/unknown", 404},
		{"GET", feedPath + "/clip-0/extra", 404}, {"GET", "/api/unknown", 404},
		{"POST", feedPath, 405},
	} {
		response, body := request(t, h, test.method, test.path, nil)
		if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("ETag") != "" {
			t.Fatalf("%s %s: status=%d headers=%v body=%q", test.method, test.path, response.StatusCode, response.Header, body)
		}
		if test.method == "HEAD" && len(body) != 0 {
			t.Fatal("error HEAD wrote body")
		}
	}
	empty := mustHTTP(t, `{"version":1,"clips":[]}`, "")
	response, body := request(t, empty, "GET", feedPath, nil)
	if response.StatusCode != 200 || string(body) != `{"clips":[]}` {
		t.Fatalf("empty feed: %d %q", response.StatusCode, body)
	}
}

func TestRejectInvalidOrigin(t *testing.T) {
	c := mustCatalog(t, fixtureJSON(t, 1))
	for _, origin := range []string{"//media.test", "file:///tmp", "https://media.test/prefix", "https://user:secret@media.test", "https://media.test?token=secret", "https://media.test#fragment", "https://media.test?"} {
		if h, err := NewHandler(c, origin); err == nil || h != nil {
			t.Fatalf("accepted origin %q", origin)
		}
	}
}

func mustHTTP(t *testing.T, data, origin string) http.Handler {
	t.Helper()
	h, err := NewHandler(mustCatalog(t, data), origin)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func request(t *testing.T, h http.Handler, method, target string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, r)
	response := recorder.Result()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}
