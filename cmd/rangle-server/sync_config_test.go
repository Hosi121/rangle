package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/generation"
)

func TestSyncFlagRequiresGenerationAndIsDisabledByDefault(t *testing.T) {
	cfg, _, _ := publishedFixture(t)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	cfg.GenerationSyncAudience = "https://app.rangle.test/internal/generation/sync"
	if app, err := newApplication(cfg, logger); err == nil || app != nil || !strings.Contains(err.Error(), "requires configured generation") {
		t.Fatal("sync without generation was silently ignored", err)
	}
	noCatalog := config{GenerationSyncAudience: cfg.GenerationSyncAudience, GenerationService: &generation.Service{}, Web: cfg.Web}
	noCatalog.Web.UIRoot = t.TempDir()
	if app, err := newApplication(noCatalog, logger); err == nil || app != nil {
		t.Fatal("sync without a catalog was silently ignored")
	}
	cfg.GenerationSyncAudience = ""
	app, err := newApplication(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest(http.MethodPost, generation.SyncPath, nil))
	if response.Code != 404 {
		t.Fatal("sync route was enabled without configuration", response.Code)
	}
}
