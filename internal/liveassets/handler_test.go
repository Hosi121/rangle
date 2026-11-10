package liveassets

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewModelsBecomeAvailableWithoutRestartAndKeepRangeContract(t *testing.T) {
	root := t.TempDir()
	h, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := httptest.NewServer(h)
	defer server.Close()
	model := []byte("new immutable model")
	sha := fmt.Sprintf("%x", sha256.Sum256(model))
	relative := "basketball-live/000001-" + sha[:16] + "/model.rgs"
	url := server.URL + "/live-assets/" + relative
	r, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal(r.Status)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, relative)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, relative), model, 0600); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Range", "bytes=2-6")
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil || r.StatusCode != 206 || string(body) != string(model[2:7]) || r.Header.Get("ETag") != `"`+sha+`"` || !strings.Contains(r.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("range: %s %q %v", r.Status, body, err)
	}
	req, _ = http.NewRequest("GET", url, nil)
	req.Header.Set("If-None-Match", `"`+sha+`"`)
	r, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 304 {
		t.Fatal(r.Status)
	}
	if err := os.WriteFile(filepath.Join(root, relative), []byte("replaced"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 503 || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("changed asset served: %s", r.Status)
	}
}

func TestUnpublishedFilesSymlinksAndWrongModelHashAreNotServed(t *testing.T) {
	root := t.TempDir()
	h, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	directory := filepath.Join(root, "basketball-live", "000001-0123456789abcdef")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "model.rgs"), []byte("wrong hash"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("model.rgs", filepath.Join(directory, "scene.json")); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/live-assets/basketball-live/000001-0123456789abcdef/model.rgs", "/live-assets/basketball-live/000001-0123456789abcdef/scene.json", "/live-assets/basketball-live/000001-0123456789abcdef/metrics.json", "/live-assets/index.json"} {
		recorder := httptest.NewRecorder()
		h.ServeHTTP(recorder, httptest.NewRequest("GET", target, nil))
		result := recorder.Result()
		result.Body.Close()
		if result.StatusCode != 404 {
			t.Fatalf("served %s: %d", target, result.StatusCode)
		}
	}
}
