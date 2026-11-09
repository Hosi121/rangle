package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/Hosi121/rangle/internal/appserver"
	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/delivery"
	"github.com/Hosi121/rangle/internal/generation"
)

type config struct {
	Web                    appserver.Config
	CatalogFile            string
	AssetRoot              string
	AssetOrigin            string
	CommunityStore         community.Store
	CommunityMemory        bool
	FirestoreProject       string
	FirestoreDatabase      string
	SecureCookies          bool
	GenerationFile         string
	GenerationService      *generation.Service
	GenerationSyncAudience string
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
	if cfg.CommunityMemory && cfg.FirestoreProject != "" {
		return nil, fmt.Errorf("choose one community store")
	}
	if (cfg.CommunityMemory || cfg.FirestoreProject != "" || cfg.CommunityStore != nil) && cfg.CatalogFile == "" {
		return nil, fmt.Errorf("community requires a clip catalog")
	}
	if cfg.GenerationSyncAudience != "" && (cfg.CatalogFile == "" || cfg.GenerationFile == "" && cfg.GenerationService == nil) {
		return nil, fmt.Errorf("generation sync requires configured generation")
	}
	if cfg.GenerationFile != "" && (cfg.FirestoreProject == "" || !cfg.SecureCookies || cfg.CatalogFile == "") {
		return nil, fmt.Errorf("generation requires persistent accounts, secure cookies and a catalog")
	}
	var clips *catalog.Catalog
	if cfg.CatalogFile != "" {
		source, err := os.Open(cfg.CatalogFile)
		if err != nil {
			return nil, fmt.Errorf("open catalog: %w", err)
		}
		var loadError error
		clips, loadError = catalog.Load(source)
		if err := errors.Join(loadError, source.Close()); err != nil {
			return nil, err
		}
		cfg.Web.PreviewPaths = clips.LocalPreviewPaths()
	}
	mux := http.NewServeMux()
	app = &application{Handler: mux}
	defer func() {
		if err != nil {
			err = errors.Join(err, app.Close())
			app = nil
		}
	}()
	store := cfg.CommunityStore
	if cfg.CommunityMemory {
		store = community.NewMemoryStore()
	}
	if cfg.FirestoreProject != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		database, err := community.NewFirestoreStore(ctx, cfg.FirestoreProject, cfg.FirestoreDatabase)
		cancel()
		if err != nil {
			return app, err
		}
		store = database
		app.closers = append(app.closers, database)
	}
	clipIDs := map[string]bool{}
	if clips != nil {
		for _, m := range clips.Manifests() {
			clipIDs[m.ClipID] = true
		}
	}
	generationService := cfg.GenerationService
	interactions := community.NewHandler(community.Config{Store: store, ClipIDs: clipIDs, SecureCookies: cfg.SecureCookies, Logger: logger,
		ClipExists: func(ctx context.Context, id string) (bool, error) {
			if generationService == nil {
				return false, nil
			}
			return generationService.CompletedClipExists(ctx, id)
		},
	})
	for _, pattern := range []string{"/api/v1/auth/", "/api/v1/clips/{clip}/engagement", "/api/v1/clips/{clip}/likes", "/api/v1/clips/{clip}/comments", "/api/v1/clips/{clip}/comments/{comment}"} {
		mux.Handle(pattern, interactions)
	}
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
		mux.HandleFunc("/health", health)
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
		})
	}
	if cfg.CatalogFile == "" {
		return app, nil
	}
	api, err := catalog.NewHandler(clips, cfg.AssetOrigin)
	if err != nil {
		return app, err
	}
	if cfg.GenerationFile != "" {
		file, err := os.Open(cfg.GenerationFile)
		if err != nil {
			return app, fmt.Errorf("open generation config: %w", err)
		}
		settings, loadError := generation.ReadConfig(file)
		if err := errors.Join(loadError, file.Close()); err != nil {
			return app, err
		}
		if settings.Project != cfg.FirestoreProject || settings.Database != cfg.FirestoreDatabase || settings.MediaOrigin != cfg.AssetOrigin {
			return app, fmt.Errorf("generation project, database and media origin must match this application")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		database, err := generation.NewFirestoreStore(ctx, settings.Project, settings.Database)
		if err != nil {
			cancel()
			return app, err
		}
		app.closers = append(app.closers, database)
		provider, err := generation.NewVertex(ctx, settings)
		cancel()
		if err != nil {
			return app, err
		}
		generationService = &generation.Service{Config: settings, Store: database, Provider: provider, Logger: logger}
	}
	if generationService != nil {
		generationService.ReservedClipID = func(id string) bool { return clipIDs[id] }
		api, err = catalog.WithPublicClips(api, generationService.PublicClips)
		if err != nil {
			return app, err
		}
		jobs, err := generation.NewHandler(generationService, func(r *http.Request) (string, error) {
			user, err := interactions.SessionUser(r)
			if err != nil || user == nil {
				return "", err
			}
			return user.ID, nil
		})
		if err != nil {
			return app, err
		}
		if cfg.GenerationSyncAudience != "" {
			syncHandler, err := generation.NewSyncHandler(generationService, cfg.GenerationSyncAudience)
			if err != nil {
				return app, err
			}
			mux.Handle(generation.SyncPath, syncHandler)
		}
		mux.Handle("/api/v1/generation/", jobs)
		mux.Handle("/api/v1/clips/{clip}", jobs.ClipHandler(api))
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
			// Published media keeps its root URLs; Flutter resources live at /demo/.
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
