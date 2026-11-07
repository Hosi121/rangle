// rangle-server serves a local Web build and an allowlisted Gaussian scene.
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
)

func main() {
	address := flag.String("listen", "127.0.0.1:4174", "loopback IP:port; PORT is not read")
	uiRoot := flag.String("ui-root", "", "directory containing the compiled Web index.html")
	sceneDir := flag.String("scene-dir", "", "optional directory containing scene.json and its frames")
	sceneID := flag.String("scene-id", "", "URL ID for the optional scene")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "rangle-server")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *address, appserver.Config{UIRoot: *uiRoot, SceneDir: *sceneDir, SceneID: *sceneID}, logger); err != nil {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, address string, config appserver.Config, logger *slog.Logger) (result error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("listen address must use a loopback IP and port")
	}
	handler, err := appserver.New(config)
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
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
	logger.Info("server started", "scene_id", config.SceneID)
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
