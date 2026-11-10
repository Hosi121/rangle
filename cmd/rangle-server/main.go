// rangle-server serves a clip API, a local Web build, and published media assets.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Hosi121/rangle/internal/appserver"
	"github.com/Hosi121/rangle/internal/serverlimit"
)

func main() {
	defaultAddress := "127.0.0.1:4174"
	if port := os.Getenv("PORT"); port != "" {
		defaultAddress = net.JoinHostPort("0.0.0.0", port)
	}

	address := flag.String("listen", defaultAddress, "IP:port; defaults to PORT when set")
	uiRoot := flag.String("ui-root", "", "directory containing the compiled Web index.html")
	sceneDir := flag.String("scene-dir", "", "optional directory containing scene.json and its frames")
	sceneID := flag.String("scene-id", "", "URL ID for the optional scene")
	catalogFile := flag.String("catalog", "", "version-1 clip catalog JSON")
	assetRoot := flag.String("asset-root", "", "directory containing published assets at their URL paths")
	assetOrigin := flag.String("asset-origin", "", "optional external HTTP(S) media origin advertised by the API")
	communityMemory := flag.Bool("community-memory", false, "ephemeral community store for loopback-only local demos")
	firestoreProject := flag.String("firestore-project", os.Getenv("RANGLE_FIRESTORE_PROJECT"), "project for the persistent community database")
	firestoreDatabase := flag.String("firestore-database", "rangle-stg", "dedicated Firestore database ID")
	insecureCookies := flag.Bool("insecure-local-cookies", false, "allow session cookies over HTTP on loopback only")
	generationFile := flag.String("generation-config", "", "optional immutable generation configuration JSON")
	generationSyncAudience := flag.String("generation-sync-audience", "", "optional exact HTTPS audience for managed generation state synchronization")
	generationManager := flag.Bool("generation-manager", false, "run the private bounded generation manager without the public UI or community database")
	generationManagerURL := flag.String("generation-manager-url", "", "HTTPS base URL of the private generation manager; excludes local generation config")
	generationAudience := flag.String("generation-manager-audience", "", "manager mode: exact HTTPS service base URL for signed caller tokens")
	generationCaller := flag.String("generation-caller-service-account", "", "manager mode: the one public runtime service account allowed to forward account IDs")
	generationCleanupURLMap := flag.String("generation-cleanup-url-map", "", "manager mode: configured media URL map for owned artifact cleanup")
	liveIndex := flag.String("live-index", os.Getenv("RANGLE_LIVE_INDEX"), "optional local live index or gs://bucket/live/index.json")
	liveAssetsRoot := flag.String("live-assets-root", "", "optional local live-assets directory; production media uses the CDN")
	privateSceneRoot := flag.String("private-scene-root", "/mnt/catalog/private-scenes", "administrator-only scene snapshots directory; empty disables assets")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		ReplaceAttr: cloudLogAttribute,
	})).With("service", "rangle-server")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := config{
		Web:                     appserver.Config{UIRoot: *uiRoot, SceneDir: *sceneDir, SceneID: *sceneID},
		CatalogFile:             *catalogFile,
		AssetRoot:               *assetRoot,
		AssetOrigin:             *assetOrigin,
		CommunityMemory:         *communityMemory,
		FirestoreProject:        *firestoreProject,
		FirestoreDatabase:       *firestoreDatabase,
		SecureCookies:           !*insecureCookies,
		GenerationFile:          *generationFile,
		GenerationSyncAudience:  *generationSyncAudience,
		GenerationManager:       *generationManager,
		GenerationManagerURL:    *generationManagerURL,
		GenerationAudience:      *generationAudience,
		GenerationCaller:        *generationCaller,
		GenerationCleanupURLMap: *generationCleanupURLMap,
		LiveIndex:               *liveIndex,
		LiveAssetsRoot:          *liveAssetsRoot,
		PrivateSceneRoot:        *privateSceneRoot,
		ServingRevision:         os.Getenv("K_REVISION"),
	}

	if err := run(ctx, *address, cfg, logger); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, address string, cfg config, logger *slog.Logger) (result error) {
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || (!ip.IsLoopback() && !ip.IsUnspecified()) {
		return fmt.Errorf("listen address must use a loopback or unspecified IP and port")
	}
	if !ip.IsLoopback() && (cfg.CommunityMemory || !cfg.SecureCookies && (cfg.CommunityStore != nil || cfg.FirestoreProject != "")) {
		return fmt.Errorf("ephemeral community storage and insecure cookies require a loopback listener")
	}

	handler, err := newApplication(cfg, logger)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, handler.Close()) }()

	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()

	logger = logger.With("listen_addr", listener.Addr().String())
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
	logger.Info("server started", "scene_id", cfg.Web.SceneID, "clip_count", handler.clipCount, "api_max_active_requests", serverlimit.MaxConcurrentAPIRequests, "api_queue_capacity", 0, "live_enabled", cfg.LiveIndex != "")

	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()

	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			server.Close()
			return fmt.Errorf("stop server: %w", err)
		}
		err = <-serveError
	case err = <-serveError:
	}

	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	if err == nil {
		logger.Info("server stopped")
	}
	return err
}
