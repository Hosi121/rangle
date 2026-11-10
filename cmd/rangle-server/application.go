package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Hosi121/rangle/internal/appserver"
	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/delivery"
	"github.com/Hosi121/rangle/internal/generation"
	"github.com/Hosi121/rangle/internal/live"
	"github.com/Hosi121/rangle/internal/liveassets"
	"github.com/Hosi121/rangle/internal/livesource"
	"github.com/Hosi121/rangle/internal/privatepreview"
	"github.com/Hosi121/rangle/internal/publisher"
)

type config struct {
	Web                     appserver.Config
	CatalogFile             string
	AssetRoot               string
	AssetOrigin             string
	CommunityStore          community.Store
	CommunityMemory         bool
	FirestoreProject        string
	FirestoreDatabase       string
	SecureCookies           bool
	GenerationFile          string
	GenerationService       *generation.Service
	GenerationSyncAudience  string
	GenerationManager       bool
	GenerationManagerURL    string
	GenerationAudience      string
	GenerationCaller        string
	GenerationCleanupURLMap string
	LiveIndex               string
	LiveAssetsRoot          string
	PrivateSceneRoot        string
	ServingRevision         string
}

type application struct {
	http.Handler
	closers   []io.Closer
	clipCount int
}

func newApplication(cfg config, logger *slog.Logger) (app *application, err error) {
	if cfg.GenerationManager {
		return newGenerationManager(cfg, logger)
	}
	if cfg.GenerationAudience != "" || cfg.GenerationCaller != "" || cfg.GenerationCleanupURLMap != "" {
		return nil, fmt.Errorf("generation manager identity options require manager mode")
	}
	if cfg.GenerationManagerURL != "" && (cfg.GenerationFile != "" || cfg.GenerationService != nil || cfg.GenerationSyncAudience != "") {
		return nil, fmt.Errorf("remote generation cannot load a local generation config, service or sync endpoint")
	}
	if cfg.GenerationManagerURL != "" && (cfg.CatalogFile == "" || !cfg.SecureCookies || cfg.FirestoreProject == "") {
		return nil, fmt.Errorf("remote generation requires persistent accounts, secure cookies and a catalog")
	}
	if cfg.GenerationManagerURL != "" && cfg.FirestoreDatabase == "rangle-stg-generation" {
		return nil, fmt.Errorf("public community storage cannot use the generation manager database")
	}
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
	if cfg.LiveAssetsRoot != "" && cfg.LiveIndex == "" {
		return nil, fmt.Errorf("local live assets require a live index")
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
	var privateScenes http.Handler
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Authorize private paths before ServeMux can clean or redirect them.
		if privateScenes != nil && (r.URL.Path == strings.TrimSuffix(privatepreview.PathPrefix, "/") || strings.HasPrefix(r.URL.Path, privatepreview.PathPrefix)) {
			privateScenes.ServeHTTP(w, r)
			return
		}
		mux.ServeHTTP(w, r)
	})
	app = &application{Handler: limitAPIRequests(routes, logger)}
	if cfg.ServingRevision != "" {
		next := app.Handler
		app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Rangle-Revision", cfg.ServingRevision)
			next.ServeHTTP(w, r)
		})
	}
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
	var generationRemote *generation.Remote
	if cfg.GenerationManagerURL != "" {
		generationRemote, err = generation.NewRemote(context.Background(), cfg.GenerationManagerURL)
		if err != nil {
			return app, err
		}
	}
	interactions := community.NewHandler(community.Config{Store: store, ClipIDs: clipIDs, SecureCookies: cfg.SecureCookies, Logger: logger,
		DescribeClip: func(ctx context.Context, id string) (community.HistoryClip, error) {
			var view catalog.PublicClip
			var err error
			if clips != nil {
				if entry, ok := clips.Metadata(id); ok {
					return community.HistoryClip{ID: entry.ID, Title: entry.Title, Sport: entry.Sport}, nil
				}
			}
			if generationRemote != nil {
				view, err = generationRemote.CompletedClip(ctx, id)
			} else if generationService != nil {
				view, err = generationService.CompletedClip(ctx, id)
			} else {
				err = community.ErrNotFound
			}
			if errors.Is(err, generation.ErrNotFound) {
				err = community.ErrNotFound
			}
			return community.HistoryClip{ID: view.ID, Title: view.Title, Sport: view.Sport}, err
		},
		ClipExists: func(ctx context.Context, id string) (bool, error) {
			if generationRemote != nil {
				return generationRemote.CompletedClipExists(ctx, id)
			}
			if generationService == nil {
				return false, nil
			}
			return generationService.CompletedClipExists(ctx, id)
		},
	})
	for _, pattern := range []string{"/api/v1/auth/", "/api/v1/publisher-auth/", "/api/v1/operations/publishers", "/api/v1/operations/publishers/", "/api/v1/publishers/", "/api/v1/me/cheered-clips", "/api/v1/me/subscriptions", "/api/v1/clips/{clip}/engagement", "/api/v1/clips/{clip}/cheers", "/api/v1/clips/{clip}/likes", "/api/v1/clips/{clip}/comments", "/api/v1/clips/{clip}/comments/{comment}"} {
		mux.Handle(pattern, interactions)
	}
	privateScenes, err = privatepreview.New(privatepreview.Config{Directory: cfg.PrivateSceneRoot, Logger: logger,
		Authorize: func(r *http.Request) error {
			user, err := interactions.AdminUser(r)
			if errors.Is(err, community.ErrForbidden) {
				return privatepreview.ErrForbidden
			}
			if err != nil {
				return err
			}
			if user == nil {
				return privatepreview.ErrUnauthenticated
			}
			return nil
		},
	})
	if err != nil {
		return app, err
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
	readLive := func(context.Context) ([]byte, error) { return []byte(`{"version":1,"sessions":[]}`), nil }
	liveOrigin := ""
	if cfg.LiveIndex != "" {
		liveOrigin = cfg.AssetOrigin
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		reader, err := livesource.Open(ctx, cfg.LiveIndex)
		cancel()
		if err != nil {
			return app, fmt.Errorf("initialize live index: %w", err)
		}
		app.closers = append(app.closers, reader)
		readLive = reader.Read
	}
	liveHandler, err := live.NewHandler(live.Config{Read: filterDeletedLive(readLive, store, liveOrigin), AssetOrigin: liveOrigin, Logger: logger})
	if err != nil {
		return app, err
	}
	mux.Handle("/api/v1/live", liveHandler)
	mux.Handle("/api/v1/live/", liveHandler)
	if cfg.LiveAssetsRoot != "" {
		assets, err := liveassets.New(cfg.LiveAssetsRoot)
		if err != nil {
			return app, fmt.Errorf("initialize local live assets: %w", err)
		}
		app.closers = append(app.closers, assets)
		mux.Handle("/live-assets/", assets)
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
	adminOwner := func(r *http.Request) (string, error) {
		user, err := interactions.AdminUser(r)
		if errors.Is(err, community.ErrForbidden) {
			return "", generation.ErrForbidden
		}
		if err != nil || user == nil {
			return "", err
		}
		return user.ID, nil
	}
	staffAuthority := generationStaffAuthority(interactions)
	grantOperator := func(r *http.Request) (string, error) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		id, err := adminOwner(r)
		if err != nil || id == "" {
			return id, err
		}
		account := publisher.ID(r.PathValue("account"))
		if !account.Valid() {
			return "", generation.ErrNotFound
		}
		directory, ok := store.(community.PublisherStore)
		if !ok {
			return "", generation.ErrInvalid
		}
		a, err := directory.Publisher(r.Context(), account)
		if errors.Is(err, community.ErrNotFound) {
			return "", generation.ErrNotFound
		}
		if err != nil {
			return "", err
		}
		if a.Status != publisher.Active && a.Status != publisher.Suspended {
			return "", generation.ErrNotFound
		}
		return id, nil
	}
	if generationRemote != nil {
		api, err = catalog.WithPublicClips(api, generationRemote.PublicClips)
		if err != nil {
			return app, err
		}
		jobs, err := generation.NewRemoteHandler(generationRemote, adminOwner)
		if err != nil {
			return app, err
		}
		mux.Handle("/api/v1/generation/", jobs)
		staffJobs, err := generation.NewPublisherRemoteHandler(generationRemote, staffAuthority)
		if err != nil {
			return app, err
		}
		mux.Handle("/api/v1/publishers/{account}/generation/", staffJobs)
		grants, err := generation.NewPublisherGrantRemoteHandler(generationRemote, grantOperator)
		if err != nil {
			return app, err
		}
		mux.Handle("/api/v1/operations/publishers/{account}/generation/", grants)
		mux.Handle("/api/v1/operations/publishers/{account}/deletion", publisherDeletionHandler(store, generationRemote, adminOwner, logger))
		mux.Handle("/api/v1/clips/{clip}", jobs.ClipHandler(api))
	}
	if generationService != nil {
		generationService.ReservedClipID = func(id string) bool { return clipIDs[id] }
		api, err = catalog.WithPublicClips(api, generationService.PublicClips)
		if err != nil {
			return app, err
		}
		jobs, err := generation.NewHandler(generationService, adminOwner)
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
		staffJobs, err := generation.NewPublisherHandler(generationService, staffAuthority)
		if err != nil {
			return app, err
		}
		mux.Handle("/api/v1/publishers/{account}/generation/", staffJobs)
		grants, err := generation.NewPublisherGrantHandler(generationService, grantOperator)
		if err != nil {
			return app, err
		}
		mux.Handle("/api/v1/operations/publishers/{account}/generation/", grants)
		mux.Handle("/api/v1/operations/publishers/{account}/deletion", publisherDeletionHandler(store, generationService, adminOwner, logger))
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

func generationStaffAuthority(accounts *community.Handler) func(*http.Request) (generation.Principal, error) {
	return func(r *http.Request) (generation.Principal, error) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		identity, err := accounts.CurrentStaff(r.WithContext(ctx))
		if err != nil || identity == nil {
			return generation.Principal{}, err
		}
		if !identity.Account.Allows(identity.Principal, identity.Record.Staff, publisher.ReadGeneration) {
			return generation.Principal{}, generation.ErrPublisherForbidden
		}
		return generation.Principal{
			Kind: "staff", ID: string(identity.Record.Staff.ID), Role: identity.Record.Staff.Role,
			Generation: identity.Record.Version, Account: &publisher.Account{
				ID: identity.Account.ID, Name: identity.Account.Name, Kind: identity.Account.Kind,
			},
		}, nil
	}
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
