package main

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/generation"
)

func TestManagerReportsServingRevisionOnHealthAndRejectedBusinessPaths(t *testing.T) {
	settings := generation.Config{Project: "comin-up", ProjectNumber: "113704194486", Database: "rangle-stg-generation",
		Image:          "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/trainer@sha256:" + strings.Repeat("a", 64),
		ServiceAccount: "rangle-stg-trainer@comin-up.iam.gserviceaccount.com", OutputBucket: "rangle-stg-media", MediaOrigin: "https://media.rangle.test",
		Deadline: time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC), Attempts: 10,
		Presets: []generation.Preset{{ID: "reviewed", Title: "Reviewed frame", Sport: "basketball", Description: "Reviewed sample", InputURI: "gs://inputs/datasets/sample.npz", InputSHA256: strings.Repeat("b", 64), PreviewURL: "https://media.rangle.test/preview.webm", Source: "https://source.rangle.test/sample", License: "CC BY 4.0"}},
	}
	root := t.TempDir()
	path := filepath.Join(root, "generation.json")
	payload, _ := json.Marshal(settings)
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(root, "catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{"version":1,"clips":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config{GenerationManager: true, GenerationFile: path, CatalogFile: catalogPath,
		GenerationAudience: "https://manager.rangle.test", GenerationCaller: "rangle-stg-runtime@comin-up.iam.gserviceaccount.com",
		ServingRevision: "rangle-stg-generation-00003-abc", GenerationService: &generation.Service{
			Config: settings, Store: struct{ generation.Store }{}, Provider: struct{ generation.Provider }{},
		}}
	app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	for path, code := range map[string]int{"/health": 200, "/healthz": 200, "/api/v1/generation/jobs": 401, "/internal/generation/public-clips": 401} {
		request := httptest.NewRequest("GET", "https://manager.rangle.test"+path, nil)
		request.Header.Set("Authorization", "Bearer deployer-token")
		request.Header.Set("X-Rangle-Revision", "forged")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != code || response.Header().Get("X-Rangle-Revision") != cfg.ServingRevision {
			t.Fatal("manager health/revision or business authentication changed", path, response.Code, response.Body.String())
		}
		if path == "/health" && response.Body.String() != "ok\n" {
			t.Fatal("manager health returned a platform response")
		}
	}
}

func TestPublicRemoteModeRejectsLocalGenerationAndSharedDatabase(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config)
	}{
		{"local config", func(c *config) { c.GenerationFile = "/must-not-open.json" }},
		{"local service", func(c *config) { c.GenerationService = &generation.Service{} }},
		{"public sync", func(c *config) { c.GenerationSyncAudience = "https://manager.rangle.test" }},
		{"no accounts", func(c *config) { c.FirestoreProject = "" }},
		{"insecure cookies", func(c *config) { c.SecureCookies = false }},
		{"no catalog", func(c *config) { c.CatalogFile = "" }},
		{"generation ledger as community", func(c *config) { c.FirestoreDatabase = "rangle-stg-generation" }},
		{"manager audience on public", func(c *config) { c.GenerationAudience = "https://manager.rangle.test" }},
		{"manager caller on public", func(c *config) { c.GenerationCaller = "rangle-stg-runtime@comin-up.iam.gserviceaccount.com" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config{GenerationManagerURL: "https://manager.rangle.test", CatalogFile: "/must-not-open.json", FirestoreProject: "comin-up", FirestoreDatabase: "rangle-stg", SecureCookies: true}
			tc.mutate(&cfg)
			app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			if err == nil || app != nil || strings.Contains(err.Error(), "open catalog") {
				t.Fatal("invalid remote isolation reached file or cloud clients", err)
			}
		})
	}
}

func TestManagerModeRejectsCommunityAndPublicOptionsBeforeReadingConfig(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*config)
	}{
		{"public remote", func(c *config) { c.GenerationManagerURL = "https://manager.rangle.test" }},
		{"ui", func(c *config) { c.Web.UIRoot = "web" }},
		{"community", func(c *config) { c.FirestoreProject = "comin-up" }},
		{"memory community", func(c *config) { c.CommunityMemory = true }},
		{"media delivery", func(c *config) { c.AssetOrigin = "https://media.rangle.test" }},
		{"live index", func(c *config) { c.LiveIndex = "gs://catalog/live/index.json" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config{GenerationManager: true, GenerationFile: "/must-not-open.json", CatalogFile: "/must-not-open.json"}
			tc.mutate(&cfg)
			app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
			if err == nil || app != nil || !strings.Contains(err.Error(), "requires only a catalog") {
				t.Fatal("manager accepted public or community access", err)
			}
		})
	}
}

func TestManagerRequiresIsolatedDatabaseAndExplicitIdentityBeforeCloudClients(t *testing.T) {
	// This fixture only validates startup; it never creates a Firestore client.
	settings := map[string]any{
		"project": "comin-up", "project_number": "113704194486", "database": "rangle-stg",
		"image":           "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/trainer@sha256:" + strings.Repeat("a", 64),
		"service_account": "rangle-stg-trainer@comin-up.iam.gserviceaccount.com", "output_bucket": "rangle-stg-media", "media_origin": "https://media.rangle.test",
		"deadline": "2026-11-14T15:00:00Z", "attempts": 10,
		"presets": []map[string]any{{"id": "reviewed", "title": "Reviewed frame", "sport": "basketball", "description": "Reviewed sample", "input_uri": "gs://inputs/datasets/sample.npz", "input_sha256": strings.Repeat("b", 64), "preview_url": "https://media.rangle.test/preview.webm", "source": "https://source.rangle.test/sample", "license": "CC BY 4.0"}},
	}
	for _, database := range []string{"rangle-stg", "rangle-stg-generation"} {
		settings["database"] = database
		payload, err := json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "generation.json")
		if err := os.WriteFile(path, payload, 0600); err != nil {
			t.Fatal(err)
		}
		cfg := config{GenerationManager: true, GenerationFile: path, CatalogFile: "/must-not-open.json"}
		if database == "rangle-stg" {
			cfg.GenerationAudience = "https://manager.rangle.test"
			cfg.GenerationCaller = "rangle-stg-runtime@comin-up.iam.gserviceaccount.com"
		}
		app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		if err == nil || app != nil || !strings.Contains(err.Error(), "isolated generation database") {
			t.Fatal("manager accessed cloud clients without isolation and caller identity", err)
		}
	}
}
