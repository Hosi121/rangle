package livesource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"google.golang.org/api/option"
)

func TestLocalIndexReadsAtomicReplacementsAndRejectsUnsafeInput(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "index.json")
	r, err := Open(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Read(context.Background()); err == nil {
		t.Fatal("absent index accepted")
	}
	for _, body := range []string{`{"version":1,"sessions":[]}`, `{"replacement":true}`} {
		temporary := filepath.Join(dir, "next.json")
		if err := os.WriteFile(temporary, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(temporary, name); err != nil {
			t.Fatal(err)
		}
		got, err := r.Read(context.Background())
		if err != nil || string(got) != body {
			t.Fatalf("stale index: %q %v", got, err)
		}
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("next.json", name); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(context.Background()); err == nil {
		t.Fatal("symlink accepted")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(strings.Repeat("x", MaxIndexBytes+1)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Read(context.Background()); err == nil {
		t.Fatal("oversized index accepted")
	}
}

func TestGCSReaderUsesOnlyConfiguredPrivateObjectAndBoundsResponse(t *testing.T) {
	var oversized atomic.Bool
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/b/example-bucket/o/live/index.json" || r.URL.Query().Get("alt") != "media" || r.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		if oversized.Load() {
			w.Write([]byte(strings.Repeat("x", MaxIndexBytes+1)))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"version":1,"sessions":[]}`))
	}))
	defer api.Close()
	r, err := Open(context.Background(), "gs://example-bucket/live/index.json", option.WithEndpoint(api.URL+"/"), option.WithHTTPClient(api.Client()))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if b, err := r.Read(context.Background()); err != nil || string(b) != `{"version":1,"sessions":[]}` {
		t.Fatalf("download: %q %v", b, err)
	}
	oversized.Store(true)
	if _, err := r.Read(context.Background()); err == nil {
		t.Fatal("unbounded GCS response accepted")
	}
}

func TestInvalidLocationsAndCancellation(t *testing.T) {
	for _, location := range []string{"", "https://example.com/index.json", "gs://example-bucket/other.json", "gs://example-bucket/live/index.json?generation=1", "gs://user@example-bucket/live/index.json"} {
		if r, err := Open(context.Background(), location); err == nil {
			r.Close()
			t.Fatalf("accepted %q", location)
		}
	}
	r, err := Open(context.Background(), filepath.Join(t.TempDir(), "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Read(ctx); err != context.Canceled {
		t.Fatalf("cancellation lost: %v", err)
	}
}
