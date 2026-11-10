package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/live"
)

func TestLiveDefaultsEmptyAndMissingConfiguredIndexDoesNotBreakHealth(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "configured"}[configured], func(t *testing.T) {
			cfg, _, _ := publishedFixture(t)
			if configured {
				cfg.LiveIndex = filepath.Join(t.TempDir(), "index.json")
			}
			app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close()
			server := httptest.NewServer(app)
			defer server.Close()
			r, b := fetch(t, server.URL+"/api/v1/live", nil)
			if configured {
				if r.StatusCode != 503 || r.Header.Get("Cache-Control") != "no-store" {
					t.Fatalf("bad live source: %d %s", r.StatusCode, b)
				}
			} else if r.StatusCode != 200 || string(b) != "{\"version\":1,\"sessions\":[]}\n" {
				t.Fatalf("disabled live: %d %s", r.StatusCode, b)
			}
			r, b = fetch(t, server.URL+"/health", nil)
			if r.StatusCode != 200 || string(b) != "ok\n" {
				t.Fatal("live source failure broke health")
			}
		})
	}
}

func TestLiveApplicationObservesNewAtomicIndexWithoutRestart(t *testing.T) {
	cfg, _, _ := publishedFixture(t)
	directory := t.TempDir()
	cfg.LiveIndex = filepath.Join(directory, "index.json")
	writeFile(t, cfg.LiveIndex, []byte(`{"version":1,"sessions":[]}`))
	app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	server := httptest.NewServer(app)
	defer server.Close()
	response, _ := fetch(t, server.URL+"/api/v1/live", nil)
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	stamp := time.Now().UTC()
	index := live.Index{Version: 1, Sessions: []live.Session{{ID: "live-test", Title: "test stream", State: "starting", StartedAt: stamp, UpdatedAt: stamp, TargetLatencySeconds: 30, Source: live.Source{Label: "recorded source", URL: "https://source.example/data", License: "CC BY 4.0", Caveat: "calibrated input prior"}, Segments: []live.Segment{}}}}
	body, err := json.Marshal(index)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(directory, "next.json"), body)
	if err := os.Rename(filepath.Join(directory, "next.json"), cfg.LiveIndex); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case <-timeout.C:
			t.Fatal("atomic live update never became visible")
		case <-ticker.C:
			r, b := fetch(t, server.URL+"/api/v1/live/live-test", nil)
			if r.StatusCode == 404 {
				continue
			}
			var got live.Session
			if r.StatusCode != http.StatusOK || json.Unmarshal(b, &got) != nil || got.ID != "live-test" {
				t.Fatalf("live detail: %d %s", r.StatusCode, b)
			}
			return
		}
	}
}
