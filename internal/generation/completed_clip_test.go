package generation

import (
	"context"
	"errors"
	"testing"
)

type failingCompletedStore struct {
	Store
	err error
}

func (s failingCompletedStore) Get(context.Context, string) (Job, error) { return Job{}, s.err }

func TestCompletedClipExistsRequiresKnownSucceededJobWithoutRegistry(t *testing.T) {
	ctx := context.Background()
	store := newMemory()
	id := nonce(1)
	// A plain job store deliberately has no PublicationStore implementation.
	service := &Service{Store: store}
	for _, state := range []State{Submitting, Queued, Running, Cancelling, Succeeded, Failed, Cancelled} {
		store.jobs[id] = Job{ID: id, State: state, Published: true, Snapshot: Preset{ID: "reviewed"}}
		exists, err := service.CompletedClipExists(ctx, "generated-"+id)
		if err != nil || exists != (state == Succeeded) {
			t.Fatalf("state=%s exists=%t err=%v", state, exists, err)
		}
	}
	store.jobs[id] = Job{ID: id, State: Succeeded}
	if exists, err := service.CompletedClipExists(ctx, "generated-"+id); err != nil || exists {
		t.Fatal("missing shared-result metadata was accepted")
	}
	for _, clip := range []string{"static", "generated-invalid", "generated-" + nonce(999), "generated-" + id + "/private"} {
		if exists, err := service.CompletedClipExists(ctx, clip); err != nil || exists {
			t.Fatalf("unknown clip %q accepted", clip)
		}
	}
	service.Store = failingCompletedStore{Store: store, err: ErrNotFound}
	if exists, err := service.CompletedClipExists(ctx, "generated-"+id); exists || err != nil {
		t.Fatal("unknown job became a backend error")
	}
	outage := errors.New("job storage unavailable")
	service.Store = failingCompletedStore{Store: store, err: outage}
	if exists, err := service.CompletedClipExists(ctx, "generated-"+id); exists || !errors.Is(err, outage) {
		t.Fatal("storage outage was hidden")
	}
}
