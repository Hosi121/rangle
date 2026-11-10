package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/stream"
)

func TestRunServesFixtureAndStops(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	output := make(outputChannel, 2)
	logger := slog.New(slog.NewJSONHandler(output, nil)).With("service", "rangle-dev")
	done := make(chan struct{})
	var runError error
	var listenAddress string
	go func() {
		runError = run(ctx, "127.0.0.1:0", logger)
		close(done)
	}()
	defer func() {
		cancel()
		select {
		case <-done:
			if runError != nil {
				t.Errorf("run() = %v", runError)
				return
			}
			select {
			case message := <-output:
				var stopped logRecord
				if err := json.Unmarshal([]byte(message), &stopped); err != nil {
					t.Errorf("decode shutdown log: %v", err)
					return
				}
				if stopped.Level != "INFO" || stopped.Service != "rangle-dev" || stopped.Message != "server stopped" || stopped.ListenAddress != listenAddress || stopped.Time.IsZero() {
					t.Errorf("shutdown log = %+v", stopped)
				}
			default:
				t.Error("server stopped without a shutdown log")
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
	var started logRecord
	if err := json.Unmarshal([]byte(message), &started); err != nil {
		t.Fatalf("decode startup log: %v", err)
	}
	if started.Level != "INFO" || started.Service != "rangle-dev" || started.ListenAddress == "" || started.Time.IsZero() {
		t.Fatalf("startup log = %+v", started)
	}
	if started.Message != "server started" || started.Fixture != "synthetic" {
		t.Fatalf("startup event = %+v", started)
	}
	listenAddress = started.ListenAddress
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(started.ManifestURL)
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
	if want := "http://" + listenAddress + manifest.URL(); started.ManifestURL != want {
		t.Errorf("startup manifest_url = %q, want %q", started.ManifestURL, want)
	}
}

func TestRunRejectsNonLoopbackAddresses(t *testing.T) {
	for _, address := range []string{"0.0.0.0:0", ":0", "192.0.2.1:0", "bad-address"} {
		t.Run(address, func(t *testing.T) {
			logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
			if err := run(context.Background(), address, logger); err == nil {
				t.Fatalf("run accepted non-loopback address %q", address)
			}
		})
	}
}

type logRecord struct {
	Time          time.Time `json:"time"`
	Level         string    `json:"level"`
	Message       string    `json:"msg"`
	Service       string    `json:"service"`
	Fixture       string    `json:"fixture"`
	ListenAddress string    `json:"listen_addr"`
	ManifestURL   string    `json:"manifest_url"`
}

type outputChannel chan string

func (output outputChannel) Write(data []byte) (int, error) {
	output <- string(data)
	return len(data), nil
}
