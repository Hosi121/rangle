package generation

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

func TestFirestoreScopedAdmissionPreservesOwnershipAndCreator(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	project := fmt.Sprintf("demo-publisher-generation-%x", time.Now().UnixNano())
	store, err := NewFirestoreStore(ctx, project, "generation")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	principal := staffPrincipal("club-a", "staff-a")
	preset := testConfig().Presets[0]
	preset.PublisherAccounts = []publisher.ID{"club-a"}
	preset = principal.snapshot(preset)
	job, created, err := store.AdmitScoped(ctx, principal, preset, nonce(1), 2, time.Now().UTC())
	if err != nil || !created || job.PublisherAccountID != "club-a" || job.CreatedBy.ID != "staff-a" {
		t.Fatal("scoped admission", job, created, err)
	}
	successor := staffPrincipal("club-a", "staff-b")
	retry, created, err := store.AdmitScoped(ctx, successor, preset, nonce(1), 2, time.Now().UTC())
	if err != nil || created || retry.CreatedBy != job.CreatedBy {
		t.Fatal("creator changed on replay", retry, created, err)
	}
	second, err := NewFirestoreStore(ctx, project, "generation")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	got, err := second.Get(ctx, job.ID)
	if err != nil || got.Owner != job.Owner || got.CreatedBy != job.CreatedBy || got.PublisherAccountID != job.PublisherAccountID {
		t.Fatal("ownership lost on reopening client", got, err)
	}
	if _, err := second.Transition(ctx, job.ID, "provider-1", Succeeded, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	other := staffPrincipal("club-b", "staff-a")
	preset.PublisherAccounts = []publisher.ID{"club-b"}
	next, created, err := second.AdmitScoped(ctx, other, other.snapshot(preset), nonce(1), 2, time.Now().UTC())
	if err != nil || !created || next.ID == job.ID {
		t.Fatal("organization-scoped idempotency collided", next, created, err)
	}
}
