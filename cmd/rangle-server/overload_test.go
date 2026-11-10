package main

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/appserver"
	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/generation"
	"github.com/Hosi121/rangle/internal/serverlimit"
)

type blockingSessions struct {
	community.Store
	entered chan struct{}
	calls   atomic.Int32
}

func (store *blockingSessions) Session(ctx context.Context, _ string) (community.Session, error) {
	store.calls.Add(1)
	store.entered <- struct{}{}
	<-ctx.Done()
	return community.Session{}, ctx.Err()
}

func TestApplicationOverloadSharesAPICapacityAndPreservesHealthAndMedia(t *testing.T) {
	cfg, manifest, assets := publishedFixture(t)
	ui := t.TempDir()
	writeFile(t, filepath.Join(ui, "landing.html"), []byte("Product entry"))
	writeFile(t, filepath.Join(ui, "index.html"), []byte("Web app"))
	writeFile(t, filepath.Join(ui, "quality-previews/clip.webm"), []byte("preview bytes"))
	cfg.Web = appserver.Config{UIRoot: ui}
	store := &blockingSessions{Store: community.NewMemoryStore(), entered: make(chan struct{}, serverlimit.MaxConcurrentAPIRequests)}
	cfg.CommunityStore = store
	app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	finished := make(chan string, serverlimit.MaxConcurrentAPIRequests)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		app.ServeHTTP(w, r)
		if id := r.Header.Get("X-Test-Held-Request"); id != "" {
			// The application gate's defer has released capacity before this signal.
			finished <- id
		}
	}))
	t.Cleanup(server.Close)
	var cancellations []context.CancelFunc
	t.Cleanup(func() {
		for _, cancel := range cancellations {
			cancel()
		}
	})
	client := &http.Client{Timeout: 5 * time.Second}
	before, originalFeed := fetch(t, server.URL+"/api/v1/clips", nil)
	results := make(chan error, serverlimit.MaxConcurrentAPIRequests)
	for index := range serverlimit.MaxConcurrentAPIRequests {
		ctx, cancel := context.WithCancel(context.Background())
		cancellations = append(cancellations, cancel)
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/auth/me", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Test-Held-Request", strconv.Itoa(index))
		request.AddCookie(&http.Cookie{Name: "rangle_session", Value: base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
		go func() {
			response, err := client.Do(request)
			if response != nil {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
			}
			results <- err
		}()
	}
	for range serverlimit.MaxConcurrentAPIRequests {
		select {
		case <-store.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("authentication requests did not enter the store")
		}
	}
	for _, target := range []string{
		"/api/v1/auth/me", "/api/v1/clips", "/api/v1/clips/demo",
		"/api/v1/clips/demo/engagement", "/api/v1/clips/demo/cheers", "/api/v1/clips/demo/comments",
		"/api/v1/generation/presets", "/api/v1/generation/jobs", generation.SyncPath,
	} {
		response, body := fetch(t, server.URL+target, nil)
		if response.StatusCode != 503 || string(body) != "{\"error\":\"api_overloaded\"}\n" || response.Header.Get("Retry-After") != "1" || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s was not rejected by the shared gate: %d %q %v", target, response.StatusCode, body, response.Header)
		}
	}
	if store.calls.Load() != serverlimit.MaxConcurrentAPIRequests {
		t.Fatal("overloaded request reached dependency", store.calls.Load())
	}
	for target, expected := range map[string]string{
		"/healthz": "ok\n", "/health": "ok\n", "/": "Product entry", "/demo/": "Web app",
		"/quality-previews/clip.webm": "preview bytes",
	} {
		response, body := fetch(t, server.URL+target, nil)
		if response.StatusCode != 200 || string(body) != expected {
			t.Fatalf("overload blocked %s: %d %q", target, response.StatusCode, body)
		}
	}
	response, body := fetch(t, server.URL+manifest.URL(), nil)
	if response.StatusCode != 200 || !strings.Contains(response.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("overload blocked manifest: %d %q", response.StatusCode, body)
	}
	assetURL := manifest.Representations[0].Segments[0].URL
	response, body = fetch(t, server.URL+assetURL, map[string]string{"Range": "bytes=1-4"})
	if response.StatusCode != 206 || string(body) != string(assets[assetURL][1:5]) || !strings.Contains(response.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("overload changed range delivery: %d %q %v", response.StatusCode, body, response.Header)
	}
	// Client cancellation propagates through the real HTTP route and store context.
	cancellations[0]()
	select {
	case id := <-finished:
		if id != "0" {
			t.Fatal("an uncanceled handler exited before the canceled one", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancellation did not return API capacity")
	}
	after, currentFeed := fetch(t, server.URL+"/api/v1/clips", nil)
	if after.StatusCode != 200 || string(currentFeed) != string(originalFeed) || after.Header.Get("ETag") != before.Header.Get("ETag") || after.Header.Get("Cache-Control") != before.Header.Get("Cache-Control") {
		t.Fatal("capacity or admitted feed bytes/cache headers were not restored", after.StatusCode)
	}
	response, body = fetch(t, server.URL+"/api/v1/clips", map[string]string{"If-None-Match": before.Header.Get("ETag")})
	if response.StatusCode != 304 || len(body) != 0 {
		t.Fatal("admission changed conditional GET semantics", response.StatusCode, string(body))
	}
	// The gate does not replace the existing CSRF or body-size protection.
	request, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/logout", nil)
	request.Header.Set("Origin", "https://other.example.test")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("admission changed CSRF protection", response.StatusCode)
	}
	request, _ = http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", strings.NewReader(`{"username":"viewer","password":"`+strings.Repeat("x", 4096)+`"}`))
	request.Header.Set("Content-Type", "application/json")
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatal("admission changed the 4096-byte body limit", response.StatusCode)
	}
	for _, cancel := range cancellations {
		cancel()
	}
	for range serverlimit.MaxConcurrentAPIRequests {
		select {
		case err := <-results:
			if err == nil {
				t.Fatal("expected canceled HTTP client")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("canceled HTTP request leaked")
		}
	}
}
