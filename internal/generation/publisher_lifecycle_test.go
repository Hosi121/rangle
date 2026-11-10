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

	"github.com/Hosi121/rangle/internal/publisher"
)

type cleanupRecorder struct {
	purged, invalidated []string
	empty, done         bool
	fail                error
}

func (c *cleanupRecorder) Purge(_ context.Context, p string) (bool, error) {
	c.purged = append(c.purged, p)
	return c.empty, c.fail
}
func (c *cleanupRecorder) Invalidate(_ context.Context, p, r string) (string, error) {
	if !regexpUUID.MatchString(r) {
		return "", ErrInvalid
	}
	c.invalidated = append(c.invalidated, p)
	return "operation-1", c.fail
}
func (c *cleanupRecorder) InvalidationDone(context.Context, string) (bool, error) {
	return c.done, c.fail
}

type cleanupMemory struct {
	*memoryStore
	lifecycle PublisherLifecycle
}

func (c *cleanupMemory) PublisherLifecycle(context.Context, publisher.ID) (PublisherLifecycle, error) {
	return c.lifecycle, nil
}
func (c *cleanupMemory) BeginPublisherCleanup(context.Context, publisher.ID, string, string, time.Time) (PublisherLifecycle, error) {
	return c.lifecycle, nil
}
func (c *cleanupMemory) AdvancePublisherCleanup(_ context.Context, old, next PublisherLifecycle) error {
	if old.Revision != c.lifecycle.Revision {
		return ErrConflict
	}
	c.lifecycle = next
	return nil
}

func cleanupFixture() (*Service, *cleanupMemory, *fakeProvider, *cleanupRecorder) {
	id := strings.Repeat("a", 32)
	c := &cleanupMemory{memoryStore: newMemory(), lifecycle: PublisherLifecycle{Account: "club-a", State: publisher.Deleting, RequestID: nonce(9), Jobs: []string{id}, Stage: "jobs", StartedBy: "operator", Revision: 1}}
	c.jobs[id] = Job{ID: id, Owner: "publisher:club-a", PublisherAccountID: "club-a", State: Succeeded}
	p := &fakeProvider{state: Running}
	assets := &cleanupRecorder{empty: true, done: true}
	return &Service{Config: testConfig(), Store: c, Provider: p, Cleanup: assets}, c, p, assets
}
func TestPublisherCleanupWaitsForProviderObjectsAndCDN(t *testing.T) {
	s, store, p, assets := cleanupFixture()
	ctx := context.Background()
	id := store.lifecycle.Jobs[0]
	job := store.jobs[id]
	job.State = Submitting
	store.jobs[id] = job
	d, err := s.PublisherDeletionStatus(ctx, "operator", "club-a")
	if err != nil || d.Stage != "jobs" || len(assets.purged) != 0 {
		t.Fatal("ambiguous submission bypassed", d, err)
	}
	p.found = "provider-1"
	d, err = s.PublisherDeletionStatus(ctx, "operator", "club-a")
	if err != nil || d.Stage != "jobs" || p.cancels.Load() != 1 {
		t.Fatal("not cancelled", d, err)
	}
	p.state = Cancelled
	d, err = s.PublisherDeletionStatus(ctx, "operator", "club-a")
	if err != nil || d.Stage != "objects" {
		t.Fatal(d, err)
	}
	assets.empty = false
	d, err = s.PublisherDeletionStatus(ctx, "operator", "club-a")
	if err != nil || d.Prefix != 0 {
		t.Fatal("nonempty prefix skipped", d, err)
	}
	assets.empty = true
	for range 4 {
		d, err = s.PublisherDeletionStatus(ctx, "operator", "club-a")
		if err != nil {
			t.Fatal(err)
		}
	}
	if d.Stage != "cache" || len(assets.invalidated) != 0 {
		t.Fatal("cache ran before objects", d)
	}
	assets.done = false
	for range 2 {
		d, err = s.PublisherDeletionStatus(ctx, "operator", "club-a")
		if err != nil {
			t.Fatal(err)
		}
	}
	if d.State != publisher.Deleting || d.Prefix != 0 {
		t.Fatal("pending invalidation treated as done", d)
	}
	assets.done = true
	for range 6 {
		d, err = s.PublisherDeletionStatus(ctx, "operator", "club-a")
		if err != nil {
			t.Fatal(err)
		}
	}
	if d.State != publisher.Deleted || d.Stage != "complete" || len(assets.invalidated) != 3 || assets.invalidated[1] != "live/"+id+"/" || p.submits.Load() != 0 {
		t.Fatal("cleanup incomplete or submitted compute", d)
	}
}
func TestPublisherCleanupRejectsOwnershipDriftAndPrivatePaths(t *testing.T) {
	s, store, _, assets := cleanupFixture()
	id := store.lifecycle.Jobs[0]
	job := store.jobs[id]
	job.PublisherAccountID = "other"
	store.jobs[id] = job
	_, err := s.PublisherDeletionStatus(context.Background(), "operator", "club-a")
	if !errors.Is(err, ErrConflict) || len(assets.purged) != 0 {
		t.Fatal("foreign side effect", err)
	}
	if invalidationID(nonce(1), 0) != invalidationID(nonce(1), 0) || invalidationID(nonce(1), 0) == invalidationID(nonce(1), 1) {
		t.Fatal("invalidation retry identity")
	}
}
func TestFirestoreDeletionFencesAdmissionAndPublication(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	s, err := NewFirestoreStore(ctx, fmt.Sprintf("demo-cleanup-%x", time.Now().UnixNano()), "generation")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	p := staffPrincipal("club-a", "staff-a")
	preset := testConfig().Presets[0]
	preset.PublisherAccounts = []publisher.ID{p.Account.ID}
	preset = p.snapshot(preset)
	var job Job
	var d PublisherLifecycle
	var admitErr, deleteErr error
	var wg sync.WaitGroup
	wg.Go(func() { job, _, admitErr = s.AdmitScoped(ctx, p, preset, nonce(1), 10, time.Now()) })
	wg.Go(func() { d, deleteErr = s.BeginPublisherCleanup(ctx, p.Account.ID, nonce(9), "operator", time.Now()) })
	wg.Wait()
	if deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if admitErr == nil {
		found := false
		for _, id := range d.Jobs {
			found = found || id == job.ID
		}
		if !found {
			t.Fatal("admitted job escaped deletion")
		}
		if _, err := s.Transition(ctx, job.ID, "provider-1", Succeeded, time.Now()); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetPublication(ctx, job.Owner, job.ID, &Publication{}, time.Now()); !errors.Is(err, ErrNotFound) {
			t.Fatal("published after deletion", err)
		}
	} else if !errors.Is(admitErr, ErrNotFound) {
		t.Fatal(admitErr)
	}
	if _, _, err := s.AdmitScoped(ctx, p, preset, nonce(1), 10, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatal("replay reopened deletion fence", err)
	}
	if _, _, err := s.AdmitScoped(ctx, p, preset, nonce(2), 10, time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatal("late admission", err)
	}
	next := d
	next.Stage = "objects"
	next.Revision++
	if err := s.AdvancePublisherCleanup(ctx, d, next); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvancePublisherCleanup(ctx, d, next); !errors.Is(err, ErrConflict) {
		t.Fatal("stale worker advanced cleanup", err)
	}
	service := &Service{Config: testConfig(), Store: s, Provider: &fakeProvider{}}
	if _, err := service.PresetsFor(ctx, p); !errors.Is(err, ErrNotFound) {
		t.Fatal("stale staff retained access", err)
	}
}
