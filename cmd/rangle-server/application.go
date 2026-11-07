package main

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/Hosi121/rangle/internal/appserver"
	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/delivery"
)

type config struct {
	Web         appserver.Config
	CatalogFile string
	AssetRoot   string
	AssetOrigin string
}

type application struct {
	http.Handler
	closers   []io.Closer
	clipCount int
}

func newApplication(cfg config, logger *slog.Logger) (app *application, err error) {
	if cfg.Web.UIRoot == "" && cfg.CatalogFile == "" {
		return nil, fmt.Errorf("a UI root or catalog is required")
	}
	if cfg.CatalogFile == "" && (cfg.AssetRoot != "" || cfg.AssetOrigin != "") {
		return nil, fmt.Errorf("asset options require a catalog")
	}
	if cfg.CatalogFile != "" && cfg.AssetRoot == "" && cfg.AssetOrigin == "" {
		return nil, fmt.Errorf("a catalog requires an asset root or external asset origin")
	}
	mux := http.NewServeMux()
	app = &application{Handler: mux}
	defer func() {
		if err != nil {
			err = errors.Join(err, app.Close())
			app = nil
		}
	}()
	if cfg.Web.UIRoot != "" {
		web, err := appserver.New(cfg.Web)
		if err != nil {
			return app, err
		}
		app.closers = append(app.closers, web)
		mux.Handle("/", web)
	} else {
		if cfg.Web.SceneDir != "" || cfg.Web.SceneID != "" {
			return app, fmt.Errorf("local scene preview requires a UI root")
		}
		mux.HandleFunc("/healthz", health)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
		})
	}
	if cfg.CatalogFile == "" {
		return app, nil
	}
	source, err := os.Open(cfg.CatalogFile)
	if err != nil {
		return app, fmt.Errorf("open catalog: %w", err)
	}
	clips, loadError := catalog.Load(source)
	if err := errors.Join(loadError, source.Close()); err != nil {
		return app, err
	}
	api, err := catalog.NewHandler(clips, cfg.AssetOrigin)
	if err != nil {
		return app, err
	}
	mux.Handle("/api/", api)
	manifests := clips.Manifests()
	app.clipCount = len(manifests)
	if cfg.AssetRoot != "" {
		for _, manifest := range manifests {
			origin, err := delivery.NewFileHandler(manifest, cfg.AssetRoot, logger)
			if err != nil {
				return app, fmt.Errorf("open clip %q revision %q: %w", manifest.ClipID, manifest.Revision, err)
			}
			app.closers = append(app.closers, origin)
			mux.Handle(manifest.URL(), origin)
			// Reserve only published clip revisions, leaving Flutter's own
			// /assets/... resources available through the Web handler.
			mux.Handle("/assets/"+manifest.ClipID+"/"+manifest.Revision+"/", origin)
		}
	}
	return app, nil
}

func (a *application) Close() error {
	var err error
	for i := len(a.closers) - 1; i >= 0; i-- {
		err = errors.Join(err, a.closers[i].Close())
	}
	a.closers = nil
	return err
}

func health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", "3")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, "ok\n")
	}
}
