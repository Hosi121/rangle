package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/publisher"
	"github.com/Hosi121/rangle/internal/sports"
)

func TestPublisherFilterIsExactAcrossPublishedAndStaticClips(t *testing.T) {
	var source struct {
		Version int    `json:"version"`
		Clips   []clip `json:"clips"`
	}
	if err := json.Unmarshal([]byte(fixtureJSON(t, 4)), &source); err != nil {
		t.Fatal(err)
	}
	for i := range source.Clips {
		id := publisher.ID("club-a")
		if i%2 == 1 {
			id = "club-ab"
		}
		source.Clips[i].Context = &sports.Context{Publisher: &publisher.Account{ID: id, Name: "Same Name", Kind: "organization"}}
	}
	data, _ := json.Marshal(source)
	generated := generatedMetadata(strings.Repeat("a", 32))
	generated.Context = &sports.Context{Publisher: &publisher.Account{ID: "club-a", Name: "Same Name", Kind: "organization"}}
	rows := []PublicClip{generated}
	h, err := WithPublicClips(mustHTTP(t, string(data), ""), func(context.Context) ([]PublicClip, error) { return rows, nil })
	if err != nil {
		t.Fatal(err)
	}
	var page struct {
		Clips  []PublicClip `json:"clips"`
		Cursor string       `json:"next_cursor"`
	}
	r, body := request(t, h, http.MethodGet, feedPath+"?publisher=club-a&limit=2", nil)
	if r.StatusCode != 200 || json.Unmarshal(body, &page) != nil || len(page.Clips) != 2 || page.Clips[0].ID != generated.ID || page.Clips[1].ID != "clip-0" || page.Cursor == "" {
		t.Fatal(r.StatusCode, string(body))
	}
	cursor := url.QueryEscape(page.Cursor)
	r, body = request(t, h, "GET", feedPath+"?publisher=club-a&limit=2&cursor="+cursor, nil)
	page.Cursor = ""
	if r.StatusCode != 200 || json.Unmarshal(body, &page) != nil || len(page.Clips) != 1 || page.Clips[0].ID != "clip-2" || page.Cursor != "" {
		t.Fatal(r.StatusCode, string(body))
	}
	for _, q := range []string{"publisher=club-ab&limit=2&cursor=" + cursor, "limit=2&cursor=" + cursor, "publisher=bad%2Fid", "publisher=club-a&publisher=club-ab"} {
		r, _ = request(t, h, "GET", feedPath+"?"+q, nil)
		if r.StatusCode != 400 {
			t.Fatal("invalid publisher filter/cursor accepted", q, r.StatusCode)
		}
	}
	rows = nil // A withdrawn publication must disappear from the organization's page.
	r, body = request(t, h, "GET", feedPath+"?publisher=club-a&limit=50", nil)
	if r.StatusCode != 200 || json.Unmarshal(body, &page) != nil || len(page.Clips) != 2 || strings.Contains(string(body), generated.ID) {
		t.Fatal("withdrawn clip present", r.StatusCode, string(body))
	}
}
