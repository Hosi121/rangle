package catalog

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSearchPaginatesMatchesAndBindsCursorToFilter(t *testing.T) {
	var source struct {
		Version int    `json:"version"`
		Clips   []clip `json:"clips"`
	}
	if err := json.Unmarshal([]byte(fixtureJSON(t, 25)), &source); err != nil {
		t.Fatal(err)
	}
	for i := range source.Clips {
		if i%2 == 0 {
			source.Clips[i].Title = "決勝 SLAM ダンク"
			source.Clips[i].Tags = []string{"決勝", "Dunk"}
		}
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	h := mustHTTP(t, string(encoded), "")
	base := feedPath + "?q=" + url.QueryEscape("slam ダンク") + "&tag=dunk&limit=2"
	response, body := request(t, h, http.MethodGet, base, nil)
	var page struct {
		Clips      []clipView `json:"clips"`
		NextCursor string     `json:"next_cursor"`
	}
	if response.StatusCode != 200 || json.Unmarshal(body, &page) != nil || len(page.Clips) != 2 ||
		page.Clips[0].ID != "clip-0" || page.Clips[1].ID != "clip-2" || page.NextCursor == "" {
		t.Fatalf("invalid search page: status=%d body=%s", response.StatusCode, body)
	}
	cursor := "&cursor=" + url.QueryEscape(page.NextCursor)
	response, body = request(t, h, http.MethodGet, base+cursor, nil)
	if response.StatusCode != 200 || json.Unmarshal(body, &page) != nil || page.Clips[0].ID != "clip-4" {
		t.Fatalf("cursor did not advance through matches: %d %s", response.StatusCode, body)
	}
	equivalent := feedPath + "?q=" + url.QueryEscape(" SLAM   ダンク ") + "&tag=DUNK&limit=2" + cursor
	response, _ = request(t, h, http.MethodGet, equivalent, nil)
	if response.StatusCode != 200 {
		t.Fatal("normalized equivalent query rejected")
	}
	for _, target := range []string{feedPath + "?limit=2" + cursor, feedPath + "?q=other&tag=dunk&limit=2" + cursor} {
		response, _ := request(t, h, http.MethodGet, target, nil)
		if response.StatusCode != 400 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("accepted cursor for another search: %s", target)
		}
	}
	response, body = request(t, h, http.MethodGet, feedPath+"?q=no-match", nil)
	if response.StatusCode != 200 || string(body) != `{"clips":[]}` {
		t.Fatalf("empty search = %d %s", response.StatusCode, body)
	}
}

func TestSearchInputAndCatalogTagsAreBounded(t *testing.T) {
	h := mustHTTP(t, fixtureJSON(t, 2), "")
	for _, query := range []string{"q=" + strings.Repeat("a", 257), "q=%FF", "tag=%FF", "tag=bad%2Ftag",
		"tag=%23dunk", "tag=" + strings.Repeat("a", 33), "q=a&q=b", "tag=a&tag=b"} {
		response, _ := request(t, h, http.MethodGet, feedPath+"?"+query, nil)
		if response.StatusCode != 400 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("accepted invalid search %q", query)
		}
	}
	for _, tags := range []string{`["Dunk","dunk"]`, `["bad tag"]`, `["#dunk"]`} {
		data := strings.Replace(fixtureJSON(t, 1), `"title":`, `"tags":`+tags+`,"title":`, 1)
		if _, err := Load(strings.NewReader(data)); err == nil {
			t.Fatalf("accepted invalid catalog tags %s", tags)
		}
	}
}
