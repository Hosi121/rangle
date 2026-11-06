package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/stream"
)

func TestRunServesFixtureAndStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	output := make(outputChannel, 1)
	done := make(chan struct{})
	var runError error
	go func() {
		runError = run(ctx, "127.0.0.1:0", output)
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
			if runError != nil {
				t.Errorf("run() = %v", runError)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop after cancellation")
		}
	}()
	var message string
	select {
	case message = <-output:
	case <-done:
		t.Fatalf("server stopped before announcing fixture: %v", runError)
	case <-ctx.Done():
		t.Fatal("server did not announce fixture")
	}
	if !strings.Contains(message, "Synthetic transport fixture only") || !strings.Contains(message, "not actual 4DGS") {
		t.Fatalf("fixture nature missing from output: %q", message)
	}
	_, manifestURL, ok := strings.Cut(message, "Manifest: ")
	if !ok {
		t.Fatalf("manifest URL missing from output: %q", message)
	}
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(strings.TrimSpace(manifestURL))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("manifest HTTP status = %d", response.StatusCode)
	}
	var manifest stream.Manifest
	if err := json.NewDecoder(response.Body).Decode(&manifest); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Validate(); err != nil {
		t.Fatal(err)
	}
	if manifest.Codec != "synthetic-transport-v1" {
		t.Errorf("codec = %q", manifest.Codec)
	}
}

func TestRunRejectsNonLoopbackAddresses(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", ":0", "192.0.2.1:0", "bad-address"} {
		t.Run(address, func(t *testing.T) {
			if err := run(context.Background(), address, io.Discard); err == nil {
				t.Fatalf("run accepted non-loopback address %q", address)
			}
		})
	}
}

type outputChannel chan string

func (output outputChannel) Write(data []byte) (int, error) {
	output <- string(data)
	return len(data), nil
}
