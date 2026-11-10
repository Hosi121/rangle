package catalog

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/delivery"
)

func TestLoadRejectsInvalidPublication(t *testing.T) {
	base := fixtureJSON(t, 1)
	var duplicate map[string]any
	if err := json.Unmarshal([]byte(base), &duplicate); err != nil {
		t.Fatal(err)
	}
	entries := duplicate["clips"].([]any)
	duplicate["clips"] = append(entries, entries[0])
	data, err := json.Marshal(duplicate)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`{`, base + `{}`, strings.Replace(base, `"version":1`, `"version":2`, 1),
		`{"version":1}`, strings.Replace(base, `"sport":"basketball"`, `"sport":" "`, 1),
		strings.Replace(base, `"preview_url":"/preview.webm"`, `"preview_url":"javascript:alert(1)"`, 1),
		strings.Replace(base, `"preview_url":"/preview.webm"`, `"preview_url":"//other.test/video"`, 1),
		strings.Replace(base, `"preview_url":"/preview.webm"`, `"preview_url":"/a/../private"`, 1),
		strings.Replace(base, `"title":"clip 0"`, `"title":"clip 0","unknown":true`, 1),
		strings.Replace(base, `"codec":"synthetic-transport-v1"`, `"codec":""`, 1),
		strings.Replace(base, `"title":"clip 0"`, `"title":"`+strings.Repeat("a", 257)+`"`, 1),
		string(data), strings.Repeat(" ", maxCatalogBytes+1),
	} {
		if c, err := Load(strings.NewReader(input)); err == nil || c != nil {
			t.Fatalf("accepted invalid catalog (length %d)", len(input))
		}
	}
}

func TestCatalogCopiesManifests(t *testing.T) {
	c := mustCatalog(t, fixtureJSON(t, 2))
	manifests := c.Manifests()
	manifests[0].ClipID = "changed"
	manifests[0].Representations[0].Segments[0].URL = "/changed"
	fresh := c.Manifests()
	if fresh[0].ClipID != "clip-0" || fresh[0].Representations[0].Segments[0].URL == "/changed" {
		t.Fatal("mutable manifest escaped catalog snapshot")
	}
}

func TestLocalPreviewPathsAreIndependentAndExcludeExternalURLs(t *testing.T) {
	data := strings.Replace(fixtureJSON(t, 2), `"preview_url":"/preview.webm"`, `"preview_url":"https://media.example.test/video.webm?signature=example"`, 1)
	c := mustCatalog(t, data)
	paths := c.LocalPreviewPaths()
	if len(paths) != 1 || paths[0] != "/preview.webm" {
		t.Fatalf("local preview paths = %v", paths)
	}
	paths[0] = "/changed.webm"
	if c.LocalPreviewPaths()[0] != "/preview.webm" {
		t.Fatal("mutable preview path escaped catalog snapshot")
	}
}

func fixtureJSON(t testing.TB, count int) string {
	t.Helper()
	entries := make([]clip, count)
	for i := range entries {
		manifest, _ := delivery.DemoFixture()
		id := "clip-" + strconv.Itoa(i)
		manifest.ClipID = id
		for j := range manifest.Representations {
			r := &manifest.Representations[j]
			r.InitURL = strings.Replace(r.InitURL, "/demo/", "/"+id+"/", 1)
			for k := range r.Segments {
				r.Segments[k].URL = strings.Replace(r.Segments[k].URL, "/demo/", "/"+id+"/", 1)
			}
		}
		entries[i] = clip{Title: "clip " + strconv.Itoa(i), Sport: "basketball", Description: "synthetic transport fixture", PreviewURL: "/preview.webm", Manifest: manifest}
	}
	data, err := json.Marshal(struct {
		Version int    `json:"version"`
		Clips   []clip `json:"clips"`
	}{1, entries})
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func mustCatalog(t testing.TB, data string) *Catalog {
	t.Helper()
	c, err := Load(strings.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return c
}
