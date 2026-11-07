package catalog

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

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
	if err := json.Unmarshal(data, &found); err != nil || detail.StatusCode != 200 || found != first.Clips[0] {
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
