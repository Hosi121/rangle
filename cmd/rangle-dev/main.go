// rangle-dev serves a tiny synthetic transport fixture on a local loopback port.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *address, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, address string, output io.Writer) error {
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
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	if _, err := fmt.Fprintf(output, "Synthetic transport fixture only: fake bytes and estimates, not actual 4DGS or playable media.\nManifest: http://%s%s\n", listener.Addr(), manifest.URL()); err != nil {
		return err
	}
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
		return nil
	}
	return err
}
