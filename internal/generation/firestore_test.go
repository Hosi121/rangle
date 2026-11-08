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

func TestFirestoreAdmissionAcrossClients(t *testing.T) {
	host := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if host == "" {
		t.Skip("requires a loopback Firestore emulator")
	}
	if !strings.HasPrefix(host, "127.0.0.1:") {
		t.Fatal("Firestore integration test requires loopback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	project := fmt.Sprintf("demo-generation-%d", time.Now().UnixNano())
	first, err := NewFirestoreStore(ctx, project, "rangle-stg")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewFirestoreStore(ctx, project, "rangle-stg")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	preset := testConfig().Presets[0]
	now := time.Now().UTC()
	winners := make(chan Job, 8)
	failures := make(chan error, 8)
	var wg sync.WaitGroup
	for index := range 8 {
		wg.Go(func() {
			store := first
			if index%2 == 1 {
				store = second
			}
			job, created, err := store.Admit(ctx, fmt.Sprintf("user%d", index), preset, nonce(index+1), 2, now)
			if err != nil && !errors.Is(err, ErrBusy) {
				failures <- err
			}
			if created {
				winners <- job
			}
		})
	}
	wg.Wait()
	close(winners)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if len(winners) != 1 {
		t.Fatalf("expected one GPU admission, got %d", len(winners))
	}
	winner := <-winners
	if winner.Snapshot.InputSHA256 != preset.InputSHA256 {
		t.Fatal("input specification was not snapshotted")
	}
	if _, err := first.Transition(ctx, winner.ID, "projects/demo/locations/asia-northeast1/customJobs/1", Succeeded, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	next, created, err := second.Admit(ctx, "next-owner", preset, nonce(99), 2, now.Add(2*time.Second))
	if err != nil || !created {
		t.Fatalf("second admission: %+v %v", next, err)
	}
	// A late poll from the old job must not clear the new global slot.
	old, err := first.Transition(ctx, winner.ID, "", Running, now.Add(3*time.Second))
	if err != nil || old.State != Succeeded {
		t.Fatalf("terminal state regressed: %+v %v", old, err)
	}
	active, err := second.Active(ctx)
	if err != nil || active == nil || active.ID != next.ID {
		t.Fatalf("active slot changed: %+v %v", active, err)
	}
	rows, err := first.List(ctx, winner.Owner)
	if err != nil || len(rows) != 1 || rows[0].ID != winner.ID {
		t.Fatalf("owner isolation: %+v %v", rows, err)
	}
	if _, err := second.Transition(ctx, next.ID, "", Failed, now.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := first.Admit(ctx, "third-owner", preset, nonce(100), 2, now); !errors.Is(err, ErrBudget) {
		t.Fatalf("attempt budget bypass: %v", err)
	}
	changed := preset
	changed.Title = "changed after deployment"
	replayed, created, err := second.Admit(ctx, winner.Owner, changed, nonceForWinner(winner.Owner), 2, now)
	if err != nil || created || replayed.ID != winner.ID || replayed.Snapshot.Title != preset.Title {
		t.Fatalf("replay lost immutable specification: %+v created=%v err=%v", replayed, created, err)
	}
}

func nonceForWinner(owner string) string {
	var index int
	_, _ = fmt.Sscanf(owner, "user%d", &index)
	return nonce(index + 1)
}
