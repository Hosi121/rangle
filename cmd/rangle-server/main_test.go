package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/appserver"
)

func TestLocalCommunityOptionsCannotBindPublicly(t *testing.T) {
	for _, cfg := range []config{{CommunityMemory: true, SecureCookies: true}, {FirestoreProject: "example", SecureCookies: false}} {
		err := run(context.Background(), "0.0.0.0:0", cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		if err == nil || !strings.Contains(err.Error(), "loopback") {
			t.Fatalf("unsafe public configuration accepted: %v", err)
		}
	}
}

func TestRunServesWebAndSceneAndStops(t *testing.T) {
	ui, scene := t.TempDir(), t.TempDir()
	for name, data := range map[string]string{
		filepath.Join(ui, "landing.html"):   "product entry",
		filepath.Join(ui, "index.html"):     "local Web app",
		filepath.Join(scene, "scene.json"):  `{"version":1,"frameRate":25,"frames":["frame.splat"]}`,
		filepath.Join(scene, "frame.splat"): "0123456789",
	} {
		if err := os.WriteFile(name, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output := make(logEvents, 4)
	logger := slog.New(slog.NewJSONHandler(output, nil)).With("service", "rangle-server")
	done := make(chan error, 1)

	go func() {
		done <- run(
			ctx,
			"127.0.0.1:0",
			config{
				Web: appserver.Config{
					UIRoot:   ui,
					SceneDir: scene,
					SceneID:  "basketball",
				},
			},
			logger,
		)
	}()

	var started event
	select {
	case line := <-output:
		if err := json.Unmarshal(line, &started); err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("server stopped before startup: %v", err)
	case <-ctx.Done():
		t.Fatal("server failed to announce startup")
	}

	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server failed to shut down")
		}

		select {
		case line := <-output:
			var stopped event
			if err := json.Unmarshal(line, &stopped); err != nil {
				t.Error(err)
				return
			}
			if stopped.Message != "server stopped" ||
				stopped.Level != "INFO" ||
				stopped.Service != "rangle-server" ||
				stopped.Address != started.Address {
				t.Errorf("shutdown log: %+v", stopped)
			}
		default:
			t.Error("missing normal shutdown event")
		}

		client := &http.Client{Timeout: time.Second}
		if response, err := client.Get("http://" + started.Address + "/healthz"); err == nil {
			response.Body.Close()
			t.Error("server still accepting after cancellation")
		}
	}()

	if started.Message != "server started" ||
		started.Level != "INFO" ||
		started.Service != "rangle-server" ||
		started.SceneID != "basketball" ||
		started.Address == "" ||
		started.Time.IsZero() {
		t.Fatalf("startup log: %+v", started)
	}

	client := &http.Client{Timeout: time.Second}
	for target, want := range map[string]string{
		"/":                               "product entry",
		"/demo/":                          "local Web app",
		"/healthz":                        "ok\n",
		"/samples/basketball/frame.splat": "0123456789",
	} {
		response, err := client.Get("http://" + started.Address + target)
		if err != nil {
			t.Fatal(err)
		}

		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != 200 || string(body) != want {
			t.Fatalf(
				"%s: status=%d body=%q error=%v",
				target,
				response.StatusCode,
				body,
				err,
			)
		}
	}
}

func TestRunRejectsUnsupportedAndMalformedAddresses(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))

	for _, address := range []string{
		":4174",
		"192.0.2.1:4174",
		"localhost:4174",
		"bad",
	} {
		err := run(context.Background(), address, config{}, logger)
		if err == nil {
			t.Fatalf("accepted address %q", address)
		}
		if !strings.Contains(err.Error(), "listen address") {
			t.Fatalf("address %q failed for an unexpected reason: %v", address, err)
		}
	}
}

func TestRunAllowsContainerAddress(t *testing.T) {
	ui := t.TempDir()
	for name, data := range map[string]string{
		"landing.html": "product entry",
		"index.html":   "test page",
	} {
		if err := os.WriteFile(
			filepath.Join(ui, name),
			[]byte(data),
			0600,
		); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	output := make(logEvents, 4)
	logger := slog.New(slog.NewJSONHandler(output, nil)).With("service", "rangle-server")
	done := make(chan error, 1)

	go func() {
		done <- run(
			ctx,
			"0.0.0.0:0",
			config{Web: appserver.Config{UIRoot: ui}},
			logger,
		)
	}()

	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("run: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server failed to shut down")
		}
	}()

	var started event
	select {
	case line := <-output:
		if err := json.Unmarshal(line, &started); err != nil {
			t.Fatal(err)
		}
	case err := <-done:
		t.Fatalf("server stopped before startup: %v", err)
	case <-ctx.Done():
		t.Fatal("server failed to announce startup")
	}

	address := strings.Replace(started.Address, "[::]", "127.0.0.1", 1)
	address = strings.Replace(address, "0.0.0.0", "127.0.0.1", 1)

	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + address + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || string(body) != "ok\n" {
		t.Fatalf("healthz: status=%d body=%q", response.StatusCode, body)
	}
}

type event struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Message string    `json:"msg"`
	Service string    `json:"service"`
	Address string    `json:"listen_addr"`
	SceneID string    `json:"scene_id"`
}

type logEvents chan []byte

func (output logEvents) Write(data []byte) (int, error) {
	output <- append([]byte(nil), data...)
	return len(data), nil
}
