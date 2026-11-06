// rangle-dev serves a tiny synthetic transport fixture on a local loopback port.
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

	"github.com/Hosi121/rangle/internal/delivery"
)

func main() {
	address := flag.String("listen", "127.0.0.1:8080", "loopback IP:port to listen on")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil)).With("service", "rangle-dev")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *address, logger); err != nil {
		logger.Error("server failed", "listen_addr", *address, "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, address string, logger *slog.Logger) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("listen address must use a loopback IP and port: %q", address)
	}
	manifest, assets := delivery.DemoFixture()
	handler, err := delivery.NewHandler(manifest, assets)
	if err != nil {
		return err
	}
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
	logger.Info("server started", "fixture", "synthetic",
		"manifest_url", fmt.Sprintf("http://%s%s", listener.Addr(), manifest.URL()))
	serveError := make(chan error, 1)
	go func() { serveError <- server.Serve(listener) }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
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
