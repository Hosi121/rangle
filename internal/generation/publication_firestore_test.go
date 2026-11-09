package generation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFirestorePublicationAcrossInstancesAndRestart(t *testing.T) {
	host := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if host == "" {
		t.Skip("requires a loopback Firestore emulator")
	}
	if !strings.HasPrefix(host, "127.0.0.1:") {
		t.Fatal("requires loopback emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	project := fmt.Sprintf("demo-publication-%d", time.Now().UnixNano())
	open := func() *FirestoreStore {
		store, err := NewFirestoreStore(ctx, project, "rangle-stg")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	first, second := open(), open()
	service, fixtureJob, _ := fixturePublication(t, "dynamic")
	preset := fixtureJob.Snapshot
	preset.QualityProfile = "baseline"
	job, created, err := first.Admit(ctx, fixtureJob.Owner, preset, nonce(1), 2, service.now())
	if err != nil || !created {
		t.Fatalf("admit: %+v %v", job, err)
	}
	if _, err := first.Transition(ctx, job.ID, "", Succeeded, service.now()); err != nil {
		t.Fatal(err)
	}
	service.Store = first
	alternate := *service
	alternate.Store = second
	var group sync.WaitGroup
	failures := make(chan error, 8)
	for index := range 8 {
		group.Go(func() {
			current := service
			if index%2 == 1 {
				current = &alternate
			}
			result, err := current.SetPublication(ctx, job.Owner, job.ID, true)
			if err != nil {
				failures <- err
			} else if !result.Published {
				failures <- fmt.Errorf("successful publication remained false")
			}
		})
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	entries, err := second.Publications(ctx)
	if err != nil || len(entries) != 1 || entries[0].Clip.ID != "generated-"+job.ID {
		t.Fatalf("registry: %+v %v", entries, err)
	}
	persisted, err := second.Get(ctx, job.ID)
	if err != nil || !persisted.Published || persisted.Snapshot.QualityProfile != "baseline" {
		t.Fatalf("state and immutable preset: %+v %v", persisted, err)
	}
	if _, err := second.SetPublication(ctx, "other", job.ID, nil, service.now()); !errors.Is(err, ErrNotFound) {
		t.Fatal("non-owner changed publication", err)
	}
	restarted := *service
	restarted.Store = open()
	restarted.Config.Presets[0].Title = "Changed live preset"
	public, err := restarted.PublicClips(ctx)
	if err != nil || len(public) != 1 || !strings.HasPrefix(public[0].Title, fixtureJob.Snapshot.Title) {
		t.Fatalf("restart lost stable metadata: %+v %v", public, err)
	}
	if _, err := alternate.SetPublication(ctx, job.Owner, job.ID, false); err != nil {
		t.Fatal(err)
	}
	public, err = restarted.PublicClips(ctx)
	if err != nil || len(public) != 0 {
		t.Fatal("another instance retained unpublished entry", err)
	}
	persisted, err = first.Get(ctx, job.ID)
	if err != nil || persisted.Published {
		t.Fatal("unpublication was not atomic", err)
	}
	queued, created, err := second.Admit(ctx, job.Owner, preset, nonce(2), 2, service.now())
	if err != nil || !created {
		t.Fatal(err)
	}
	if _, err := first.SetPublication(ctx, job.Owner, queued.ID, &entries[0], service.now()); !errors.Is(err, ErrConflict) {
		t.Fatal("unfinished publication accepted", err)
	}
	if _, err := first.Transition(ctx, queued.ID, "", Failed, service.now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.Admit(ctx, job.Owner, preset, nonce(3), 2, service.now()); !errors.Is(err, ErrBudget) {
		t.Fatal("publication modified the admission budget", err)
	}
}
