package community

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

type deletionContractStore interface {
	Store
	PublisherDeletionStore
	StaffManagementStore
	CheerHistoryStore
	CheerStore
}

func publisherDeletionContract(t *testing.T, s deletionContractStore) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	op := newAccount("admin", "Operator", "long password for operator")
	op.User.Admin = true
	if err := s.CreateAccount(ctx, op); err != nil {
		t.Fatal(err)
	}
	a, staff := publisherFixture(t, "club-delete")
	other, b := publisherFixture(t, "club-retain")
	if err := s.ProvisionPublisher(ctx, a, staff); err != nil {
		t.Fatal(err)
	}
	if err := s.ProvisionPublisher(ctx, other, b); err != nil {
		t.Fatal(err)
	}
	m := StaffManager{Principal: publisher.StaffPrincipal{PublisherAccountID: a.ID, StaffAccountID: staff.Staff.ID}, Version: staff.Version}
	requested, err := s.RequestPublisherDeletion(ctx, m, a.ID, true)
	if err != nil || !requested.DeletionRequested || requested.Status != publisher.Active {
		t.Fatal("request prematurely disabled organization", err)
	}
	if _, err := s.RequestPublisherDeletion(ctx, m, other.ID, true); !errors.Is(err, ErrForbidden) {
		t.Fatal("foreign deletion request", err)
	}
	if _, err := s.RequestPublisherDeletion(ctx, m, a.ID, false); err != nil {
		t.Fatal(err)
	}
	clip := "generated-" + strings.Repeat("a", 32)
	batch := CheerBatch{ID: strings.Repeat("b", 32), UserID: "admin", Clip: HistoryClip{ID: clip, Title: "Retain history", Sport: "basketball"}, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}
	batch.Bins[0] = 1
	if err := s.RecordCheers(ctx, clip, batch); err != nil {
		t.Fatal(err)
	}
	d, err := s.BeginPublisherDeletion(ctx, "admin", a.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := s.BeginPublisherDeletion(ctx, "admin", a.ID, now.Add(time.Second))
	if err != nil || retry.RequestID != d.RequestID || retry.StartedAt != d.StartedAt {
		t.Fatal("deletion ID not stable", err)
	}
	if _, err := s.RequestPublisherDeletion(ctx, m, a.ID, false); !errors.Is(err, ErrForbidden) {
		t.Fatal("reactivated deleting account", err)
	}
	if err := s.FinishPublisherDeletion(ctx, "admin", a.ID, strings.Repeat("f", 32), []string{clip}, now); !errors.Is(err, ErrConflict) {
		t.Fatal("foreign request finalized", err)
	}
	if err := s.FinishPublisherDeletion(ctx, "admin", a.ID, d.RequestID, []string{"bootstrap-shared"}, now); !errors.Is(err, ErrConflict) {
		t.Fatal("shared media tombstoned", err)
	}
	if err := s.FinishPublisherDeletion(ctx, "admin", a.ID, d.RequestID, []string{clip}, now); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishPublisherDeletion(ctx, "admin", a.ID, d.RequestID, []string{clip}, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	deleted, err := s.Publisher(ctx, a.ID)
	if err != nil || deleted.Status != publisher.Deleted || deleted.DeletionRequested {
		t.Fatal("not deleted", err)
	}
	kept, err := s.Publisher(ctx, other.ID)
	if err != nil || kept.Status != publisher.Active {
		t.Fatal("foreign account changed", err)
	}
	if _, err := s.Staff(ctx, a.ID, staff.Staff.ID); err != nil {
		t.Fatal("staff audit erased", err)
	}
	rows, err := s.CheeredClips(ctx, "admin", nil, 20)
	if err != nil || len(rows) != 1 || rows[0].Clip.ID != clip {
		t.Fatal("history erased", err)
	}
	cheers, err := s.Cheers(ctx, clip)
	if err != nil || cheers.Bins[0] != 1 {
		t.Fatal("cheers erased", err)
	}
	markers, err := s.DeletedClips(ctx, []string{clip, "bootstrap-shared"})
	if err != nil || !markers[clip] || markers["bootstrap-shared"] {
		t.Fatal("tombstones", markers, err)
	}
	finished, _ := s.PublisherDeletion(ctx, "admin", a.ID)
	if !finished.CompletedAt.Equal(now) {
		t.Fatal("idempotency changed audit timestamp")
	}
}
func TestMemoryPublisherDeletionContract(t *testing.T) {
	publisherDeletionContract(t, NewMemoryStore())
}
func TestFirestorePublisherDeletionContract(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	s, err := NewFirestoreStore(context.Background(), fmt.Sprintf("demo-deletion-%x", time.Now().UnixNano()), "community")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	publisherDeletionContract(t, s)
}
func TestPublisherDeletionRequestRequiresCurrentAdministrator(t *testing.T) {
	h, s, a, _, cookie := managedFixture(t)
	path := "/api/v1/publishers/club-a/deletion-request"
	r, data := call(t, h, "PUT", path, `{"requested":true,"confirmation":"wrong"}`, cookie, nil)
	if r.StatusCode != 400 {
		t.Fatal(r.StatusCode, string(data))
	}
	r, data = call(t, h, "PUT", path, `{"requested":true,"confirmation":"club-a"}`, cookie, nil)
	if r.StatusCode != 200 || !strings.Contains(string(data), `"requested":true`) || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(r.StatusCode, string(data))
	}
	r, data = call(t, h, "GET", path, "", cookie, nil)
	if r.StatusCode != 200 || !strings.Contains(string(data), `"requested":true`) {
		t.Fatal(r.StatusCode, string(data))
	}
	_, data = call(t, h, "POST", "/api/v1/publishers/club-a/staff", staffBody("editor", publisher.Editor), cookie, nil)
	login, _ := staffLoginRequest(t, h, string(a.ID), "editor", "a long staff password 2026", nil)
	r, _ = call(t, h, "PUT", path, `{"requested":true,"confirmation":"club-a"}`, login.Cookies()[0], nil)
	if r.StatusCode != 403 {
		t.Fatal("editor deletion", r.StatusCode)
	}
	r, _ = call(t, h, "PUT", path, `{"requested":false,"confirmation":"club-a"}`, cookie, map[string]string{"Origin": "https://foreign.test"})
	if r.StatusCode != 403 {
		t.Fatal("cross-origin deletion", r.StatusCode)
	}
	current, _ := s.Publisher(context.Background(), a.ID)
	if current.Status != publisher.Active {
		t.Fatal("request executed deletion")
	}
}
