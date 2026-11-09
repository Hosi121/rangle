package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/appserver"
	"github.com/Hosi121/rangle/internal/delivery"
	"github.com/Hosi121/rangle/internal/stream"
)

func TestApplicationFeedToManifestAndAsset(t *testing.T) {
	cfg, manifest, assets := publishedFixture(t)
	// Demo UI resources and declared previews coexist with unchanged media URLs.
	ui := t.TempDir()
	writeFile(t, filepath.Join(ui, "landing.html"), []byte("Product entry"))
	writeFile(t, filepath.Join(ui, "index.html"), []byte("Web app"))
	writeFile(t, filepath.Join(ui, "quality-previews/clip.webm"), []byte("preview bytes"))
	writeFile(t, filepath.Join(ui, "quality-previews/unlisted.webm"), []byte("unlisted media"))
	writeFile(t, filepath.Join(ui, "assets/AssetManifest.bin.json"), []byte("Flutter assets"))
	cfg.Web = appserver.Config{UIRoot: ui}
	app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(app)
	defer server.Close()
	var feed struct {
		Clips []struct {
			ManifestURL string `json:"manifest_url"`
			PreviewURL  string `json:"preview_url"`
		} `json:"clips"`
	}
	response, body := fetch(t, server.URL+"/api/v1/clips", nil)
	if err := json.Unmarshal(body, &feed); err != nil || response.StatusCode != 200 || len(feed.Clips) != 1 {
		t.Fatalf("feed: %q error=%v", body, err)
	}
	if feed.Clips[0].ManifestURL != manifest.URL() {
		t.Fatalf("manifest URL = %q", feed.Clips[0].ManifestURL)
	}
	if feed.Clips[0].PreviewURL != "/quality-previews/clip.webm" {
		t.Fatalf("preview URL changed: %q", feed.Clips[0].PreviewURL)
	}
	response, body = fetch(t, server.URL+feed.Clips[0].ManifestURL, nil)
	var received stream.Manifest
	if err := json.Unmarshal(body, &received); err != nil || response.StatusCode != 200 {
		t.Fatalf("manifest: %q error=%v", body, err)
	}
	if err := received.Validate(); err != nil {
		t.Fatal(err)
	}
	assetURL := received.Representations[0].Segments[0].URL
	response, body = fetch(t, server.URL+assetURL, map[string]string{"Range": "bytes=1-4"})
	if response.StatusCode != 206 || string(body) != string(assets[assetURL][1:5]) || !strings.Contains(response.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q %v", response.StatusCode, body, response.Header)
	}
	for target, want := range map[string]string{"/": "Product entry", "/demo/": "Web app", "/quality-previews/clip.webm": "preview bytes", "/demo/assets/AssetManifest.bin.json": "Flutter assets", "/healthz": "ok\n"} {
		response, body := fetch(t, server.URL+target, nil)
		if response.StatusCode != 200 || string(body) != want {
			t.Fatalf("%s: %d %q", target, response.StatusCode, body)
		}
	}
	for _, target := range []string{"/index.html", "/assets/AssetManifest.bin.json", "/quality-previews/unlisted.webm", "/catalog.json", "/assets/demo/synthetic-v1/low/private.txt", "/api/unknown"} {
		response, _ := fetch(t, server.URL+target, nil)
		if response.StatusCode != 404 || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("private or unknown %s: %d %v", target, response.StatusCode, response.Header)
		}
	}
}

func TestApplicationExternalOriginNeedsNoLocalAssets(t *testing.T) {
	cfg, _, _ := publishedFixture(t)
	cfg.AssetRoot = ""
	cfg.AssetOrigin = "https://media.example.test"
	app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(app)
	defer server.Close()
	response, body := fetch(t, server.URL+"/api/v1/clips", nil)
	if response.StatusCode != 200 || !strings.Contains(string(body), `"manifest_url":"https://media.example.test/manifests/demo/synthetic-v1.json"`) {
		t.Fatalf("external feed: %d %q", response.StatusCode, body)
	}
	response, _ = fetch(t, server.URL+"/assets/demo/synthetic-v1/low/segment-0.bin", nil)
	if response.StatusCode != 404 {
		t.Fatal("API unexpectedly served external media bytes")
	}
	response, body = fetch(t, server.URL+"/health", nil)
	if response.StatusCode != 200 || string(body) != "ok\n" {
		t.Fatal("missing API-only health endpoint")
	}
}

func TestServingRevisionIsReportedOnEveryPath(t *testing.T) {
	for _, revision := range []string{"", "rangle-stg-00003-abc"} {
		t.Run(revision, func(t *testing.T) {
			cfg, _, _ := publishedFixture(t)
			cfg.ServingRevision = revision
			app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			for _, path := range []string{"/health", "/api/v1/clips", "/api/v1/live", "/missing"} {
				request := httptest.NewRequest(http.MethodGet, path, nil)
				request.Header.Set("X-Rangle-Revision", "spoofed-revision")
				response := httptest.NewRecorder()
				app.ServeHTTP(response, request)
				if got := response.Header().Get("X-Rangle-Revision"); got != revision {
					t.Fatalf("%s revision = %q, want %q", path, got, revision)
				}
			}
		})
	}
}

func TestApplicationRejectsIncompleteConfiguration(t *testing.T) {
	cfg, _, _ := publishedFixture(t)
	for _, invalid := range []config{
		{}, {AssetRoot: cfg.AssetRoot}, {CatalogFile: cfg.CatalogFile},
		{CatalogFile: "absent.json", AssetRoot: cfg.AssetRoot},
		{CatalogFile: cfg.CatalogFile, AssetRoot: filepath.Join(t.TempDir(), "absent")},
		{CatalogFile: cfg.CatalogFile, AssetOrigin: "https://media.test/prefix"},
		{CatalogFile: cfg.CatalogFile, AssetRoot: cfg.AssetRoot, Web: appserver.Config{SceneID: "unexpected"}},
	} {
		app, err := newApplication(invalid, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		if err == nil || app != nil {
			if app != nil {
				app.Close()
			}
			t.Fatalf("accepted invalid configuration: %+v", invalid)
		}
	}
}

func publishedFixture(t *testing.T) (config, stream.Manifest, map[string][]byte) {
	t.Helper()
	manifest, assets := delivery.DemoFixture()
	root := t.TempDir()
	for target, data := range assets {
		writeFile(t, filepath.Join(root, strings.TrimPrefix(target, "/")), data)
	}
	writeFile(t, filepath.Join(root, "assets/demo/synthetic-v1/low/private.txt"), []byte("private"))
	data, err := json.Marshal(map[string]any{
		"version": 1,
		"clips":   []any{map[string]any{"title": "Synthetic clip", "sport": "demo", "description": "Synthetic transport fixture", "preview_url": "/quality-previews/clip.webm", "manifest": manifest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	catalogFile := filepath.Join(t.TempDir(), "catalog.json")
	writeFile(t, catalogFile, data)
	return config{CatalogFile: catalogFile, AssetRoot: root}, manifest, assets
}

func writeFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func fetch(t *testing.T, target string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	r, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}
