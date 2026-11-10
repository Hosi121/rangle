package catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/sports"
)

func sportsFixture(t *testing.T) *sports.Context {
	t.Helper()
	b, err := os.ReadFile("../../testdata/sports/context.json")
	if err != nil {
		t.Fatal(err)
	}
	var c sports.Context
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	return &c
}
func TestContextSearchForStaticAndGeneratedClips(t *testing.T) {
	var source struct {
		Version int    `json:"version"`
		Clips   []clip `json:"clips"`
	}
	if err := json.Unmarshal([]byte(fixtureJSON(t, 2)), &source); err != nil {
		t.Fatal(err)
	}
	source.Clips[1].Context = sportsFixture(t)
	b, _ := json.Marshal(source)
	base := mustHTTP(t, string(b), "")
	generated := PublicClip{ID: "generated-" + strings.Repeat("a", 32), Revision: strings.Repeat("a", 32), Title: "generated", Sport: "basketball", SceneURL: "https://media.test/generated/scene.json", Context: sportsFixture(t)}
	h, err := WithPublicClips(base, func(context.Context) ([]PublicClip, error) { return []PublicClip{generated}, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"Example Sports", "サンプル選手", "例示体育館", "東京", "サンプルA", "サンプルカップ決勝"} {
		response, body := request(t, h, http.MethodGet, feedPath+"?q="+url.QueryEscape(term), nil)
		var page struct {
			Clips []PublicClip `json:"clips"`
		}
		if response.StatusCode != 200 || json.Unmarshal(body, &page) != nil || len(page.Clips) != 2 || page.Clips[1].Context.Event.ID != "sample-final" {
			t.Fatalf("%s: %d %s", term, response.StatusCode, body)
		}
	}
	source.Clips[0].Context = sportsFixture(t)
	source.Clips[0].Context.Event.Venue.Name = "contradictory venue"
	b, _ = json.Marshal(source)
	if _, err := Load(strings.NewReader(string(b))); err == nil {
		t.Fatal("conflicting identity in catalog accepted")
	}
	generated.Context.Publisher.Kind = "person"
	if generated.ValidateGenerated() == nil {
		t.Fatal("generated metadata bypassed context validation")
	}
}
