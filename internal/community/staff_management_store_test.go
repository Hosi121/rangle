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

func managementContract(t *testing.T, s StaffManagementStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	a, admin := publisherFixture(t, "managed-a")
	if err := s.ProvisionPublisher(ctx, a, admin); err != nil {
		t.Fatal(err)
	}
	manager := StaffManager{Principal: publisher.StaffPrincipal{PublisherAccountID: a.ID, StaffAccountID: admin.Staff.ID}, Version: admin.Version}
	editor := admin.clone()
	editor.Staff.ID = "editor-id"
	editor.Staff.Username = "editor"
	editor.Staff.Role = publisher.Editor
	var count atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			err := s.CreateStaff(ctx, manager, a.ID, editor)
			if err == nil {
				count.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if count.Load() != 1 {
		t.Fatal("staff username uniqueness is not atomic", count.Load())
	}
	editorManager := StaffManager{Principal: publisher.StaffPrincipal{PublisherAccountID: a.ID, StaffAccountID: editor.Staff.ID}, Version: 1}
	if _, err := s.ListStaff(ctx, editorManager, a.ID, "", 21); !errors.Is(err, ErrForbidden) {
		t.Fatal("editor can manage staff", err)
	}
	other, otherAdmin := publisherFixture(t, "managed-b")
	otherAdmin.Staff.ID = admin.Staff.ID
	if err := s.ProvisionPublisher(ctx, other, otherAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListStaff(ctx, manager, other.ID, "", 21); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross-organization staff leak", err)
	}
	for i := range 22 {
		row := editor.clone()
		row.Staff.ID = publisher.StaffID(fmt.Sprintf("member-%02d", i))
		row.Staff.Username = fmt.Sprintf("member_%02d", i)
		if err := s.CreateStaff(ctx, manager, a.ID, row); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.ListStaff(ctx, manager, a.ID, "", 21)
	if err != nil || len(first) != 21 {
		t.Fatal("first staff page", len(first), err)
	}
	last, err := s.ListStaff(ctx, manager, a.ID, first[20].ID, 21)
	if err != nil || len(last) != 3 {
		t.Fatal("staff continuation", len(last), err)
	}
	ids := map[publisher.StaffID]bool{}
	for _, row := range append(first, last...) {
		if ids[row.ID] || row.PublisherAccountID != a.ID {
			t.Fatal("duplicate or foreign staff")
		}
		ids[row.ID] = true
	}
	updated, err := s.UpdateStaff(ctx, manager, a.ID, editor.Staff.ID, publisher.Administrator, false)
	if err != nil || updated.Version != 2 {
		t.Fatal("role change failed", updated.Version, err)
	}
	same, err := s.UpdateStaff(ctx, manager, a.ID, editor.Staff.ID, publisher.Administrator, false)
	if err != nil || same.Version != 2 {
		t.Fatal("idempotent update rotated session generation", same.Version, err)
	}
	// The old actor version is denied even after the target gained administrator.
	if _, err := s.ListStaff(ctx, editorManager, a.ID, "", 21); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale credential generation retained permission", err)
	}
	if _, err := s.UpdateStaff(ctx, manager, a.ID, admin.Staff.ID, publisher.Administrator, true); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateStaff(ctx, manager, a.ID, editor); !errors.Is(err, ErrForbidden) {
		t.Fatal("offboarded manager can create accounts", err)
	}
	if account, err := s.Publisher(ctx, a.ID); err != nil || account.Status != publisher.Active {
		t.Fatal("offboarding changed organization existence", err)
	}
	operator := newAccount("operator", "Operator", "a long operator password 2026")
	operator.User.Admin = true
	if err := s.(Store).CreateAccount(ctx, operator); err != nil {
		t.Fatal(err)
	}
	recovery := StaffManager{OperatorID: operator.User.ID}
	replacement := editor.clone()
	replacement.Staff.ID = "replacement"
	replacement.Staff.Username = "replacement"
	replacement.Staff.Role = publisher.Administrator
	if err := s.CreateStaff(ctx, recovery, a.ID, replacement); err != nil {
		t.Fatal("operator could not recover account", err)
	}
	// An ambiguous principal cannot combine operator and staff identities.
	recovery.Principal = manager.Principal
	if _, err := s.ListStaff(ctx, recovery, a.ID, "", 21); !errors.Is(err, ErrForbidden) {
		t.Fatal("combined actor escaped namespace checks", err)
	}
}
func TestMemoryStaffManagementContract(t *testing.T) { managementContract(t, NewMemoryStore()) }
func TestFirestoreStaffManagementContract(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	s, err := NewFirestoreStore(context.Background(), fmt.Sprintf("demo-manage-%x", time.Now().UnixNano()), "community")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	managementContract(t, s)
}
