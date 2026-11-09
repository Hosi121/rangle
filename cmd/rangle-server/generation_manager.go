package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/generation"
)

func newGenerationManager(cfg config, logger *slog.Logger) (app *application, err error) {
	if cfg.GenerationManagerURL != "" || cfg.GenerationFile == "" || cfg.CatalogFile == "" ||
		cfg.Web.UIRoot != "" || cfg.Web.SceneDir != "" || cfg.Web.SceneID != "" ||
		cfg.FirestoreProject != "" || cfg.CommunityStore != nil || cfg.CommunityMemory ||
		cfg.LiveIndex != "" || cfg.LiveAssetsRoot != "" || cfg.AssetRoot != "" || cfg.AssetOrigin != "" {
		return nil, fmt.Errorf("generation manager requires only a catalog, isolated generation config and manager identity options")
	}
	app = &application{}
	defer func() {
		if err != nil {
			err = errors.Join(err, app.Close())
			app = nil
		}
	}()
	file, err := os.Open(cfg.GenerationFile)
	if err != nil {
		return app, fmt.Errorf("open generation config: %w", err)
	}
	settings, loadError := generation.ReadConfig(file)
	if err := errors.Join(loadError, file.Close()); err != nil {
		return app, err
	}
	managerConfig := generation.ManagerConfig{Audience: cfg.GenerationAudience, Caller: cfg.GenerationCaller, SyncAudience: cfg.GenerationSyncAudience}
	if err := managerConfig.Validate(settings); err != nil {
		return app, err
	}
	file, err = os.Open(cfg.CatalogFile)
	if err != nil {
		return app, fmt.Errorf("open catalog: %w", err)
	}
	clips, loadError := catalog.Load(file)
	if err := errors.Join(loadError, file.Close()); err != nil {
		return app, err
	}
	reserved := map[string]bool{}
	for _, clip := range clips.Manifests() {
		reserved[clip.ClipID] = true
	}
	service := cfg.GenerationService
	if service == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		database, err := generation.NewFirestoreStore(ctx, settings.Project, settings.Database)
		if err != nil {
			return app, err
		}
		app.closers = append(app.closers, database)
		provider, err := generation.NewVertex(ctx, settings)
		if err != nil {
			return app, err
		}
		service = &generation.Service{Config: settings, Store: database, Provider: provider, Logger: logger}
	}
	service.ReservedClipID = func(id string) bool { return reserved[id] }
	app.Handler, err = generation.NewManager(service, managerConfig)
	return app, err
}
