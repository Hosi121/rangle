package community

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

func publisherFixture(t *testing.T, id publisher.ID) (publisher.Account, StaffRecord) {
	t.Helper()
	row, err := newStaff(id, "manager", "a long staff password 2026", publisher.Administrator)
	if err != nil {
		t.Fatal(err)
	}
	return publisher.Account{ID: id, Name: "Example Club", Kind: "organization", Status: publisher.Active}, row
}
func publisherStoreContract(t *testing.T, s PublisherStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	account, record := publisherFixture(t, "club-a")
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			err := s.ProvisionPublisher(ctx, account, record)
			if err == nil {
				created.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatal("organization provisioning was not atomic", created.Load())
	}
	if got, err := s.Publisher(ctx, account.ID); err != nil || got != account {
		t.Fatal("publisher roundtrip", got, err)
	}
	row, err := s.StaffByUsername(ctx, account.ID, "manager")
	if err != nil || row.Staff != record.Staff || !checkPassword(staffPassword(row), "a long staff password 2026") {
		t.Fatal("credential roundtrip", err)
	}
	row.Salt[0] ^= 255
	if got, err := s.Staff(ctx, account.ID, record.Staff.ID); err != nil || got.Salt[0] != record.Salt[0] {
		t.Fatal("mutable credential escaped store", err)
	}
	other := account
	other.ID = "club-b"
	otherRecord := record
	otherRecord.Staff.PublisherAccountID = other.ID
	if err := s.ProvisionPublisher(ctx, other, otherRecord); err != nil {
		t.Fatal("same local staff ID rejected under another account", err)
	}
	if got, err := s.StaffByUsername(ctx, other.ID, "manager"); err != nil || got.Staff.PublisherAccountID != other.ID {
		t.Fatal("cross-organization username lookup", err)
	}
	if _, err := s.StaffByUsername(ctx, account.ID, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatal("missing username", err)
	}
	session := StaffSession{Principal: publisher.StaffPrincipal{PublisherAccountID: account.ID, StaffAccountID: record.Staff.ID}, Version: record.Version, ExpiresAt: time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)}
	if err := s.PutStaffSession(ctx, "session-hash", session); err != nil {
		t.Fatal(err)
	}
	if err := s.PutStaffSession(ctx, "session-hash", session); !errors.Is(err, ErrConflict) {
		t.Fatal("session overwrote an existing record", err)
	}
	got, err := s.StaffSession(ctx, "session-hash")
	if err != nil || got != session {
		t.Fatal("staff session roundtrip", got, err)
	}
	if err := s.DeleteStaffSession(ctx, "session-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StaffSession(ctx, "session-hash"); !errors.Is(err, ErrNotFound) {
		t.Fatal("logged-out session remained", err)
	}
}
func TestMemoryPublisherStoreContract(t *testing.T) { publisherStoreContract(t, NewMemoryStore()) }
func TestFirestorePublisherStoreContract(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	project := fmt.Sprintf("demo-staff-%x", time.Now().UnixNano())
	s, err := NewFirestoreStore(context.Background(), project, "community")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publisherStoreContract(t, s)
	reopened, err := NewFirestoreStore(context.Background(), project, "community")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if row, err := reopened.StaffByUsername(context.Background(), "club-a", "manager"); err != nil || row.Version != 1 {
		t.Fatal("credentials lost after client reopening", err)
	}
}
