package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/live"
)

type deletedLiveStore struct {
	community.Store
	calls int
}

func (s *deletedLiveStore) DeletedClips(_ context.Context, ids []string) (map[string]bool, error) {
	s.calls++
	return map[string]bool{"generated-" + strings.Repeat("a", 32): true}, nil
}
func TestDeletedPublisherSessionIsAbsentFromPublicLiveIndex(t *testing.T) {
	stamp := time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)
	session := live.Session{ID: strings.Repeat("a", 32), Title: "Deleted live", State: "starting", StartedAt: stamp, UpdatedAt: stamp, TargetLatencySeconds: 30, Source: live.Source{Label: "recorded source", URL: "https://source.example/data", License: "CC BY 4.0", Caveat: "calibrated input"}, Segments: []live.Segment{}}
	other := session
	other.ID = "shared-live"
	other.Title = "Shared live"
	data, _ := json.Marshal(live.Index{Version: 1, Sessions: []live.Session{session, other}})
	store := &deletedLiveStore{}
	read := filterDeletedLive(func(context.Context) ([]byte, error) { return data, nil }, store, "")
	filtered, err := read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	index, err := live.ParseIndex(filtered, "")
	if err != nil || len(index.Sessions) != 1 || index.Sessions[0].ID != "shared-live" || store.calls != 1 {
		t.Fatal("deleted session leaked or shared session lost", string(filtered), err)
	}
	data = []byte(`{"version":1,"sessions":[],"untrusted":true}`)
	if _, err := read(context.Background()); err == nil || store.calls != 1 {
		t.Fatal("invalid source laundered by deletion filter")
	}
}
func TestCleanupFlagCannotRunAtPublicRuntime(t *testing.T) {
	cfg, _, _ := publishedFixture(t)
	cfg.GenerationCleanupURLMap = "rangle-stg-https"
	if app, err := newApplication(cfg, nil); err == nil {
		app.Close()
		t.Fatal("public runtime accepted cleanup capability")
	}
}
