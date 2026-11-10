package community

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

type directoryContractStore interface {
	Store
	PublisherStore
	PublisherDirectoryStore
}

func directoryContract(t *testing.T, s directoryContractStore) {
	t.Helper()
	ctx := context.Background()
	op := newAccount("admin", "Operator", "a long operator password 2026")
	op.User.Admin = true
	if err := s.CreateAccount(ctx, op); err != nil {
		t.Fatal(err)
	}
	a, staff := publisherFixture(t, "club-00")
	for i := range 22 {
		a.ID = publisher.ID(fmt.Sprintf("club-%02d", i))
		staff.Staff.PublisherAccountID = a.ID
		if err := s.ProvisionPublisher(ctx, a, staff); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ListPublishers(ctx, "admin", "", 21)
	if err != nil || len(rows) != 21 || rows[0].ID != "club-00" {
		t.Fatal("directory ordering", len(rows), err)
	}
	next, err := s.ListPublishers(ctx, "admin", rows[20].ID, 21)
	if err != nil || len(next) != 1 || next[0].ID != "club-21" {
		t.Fatal("directory pagination", len(next), err)
	}
	viewer := newAccount("viewer", "Viewer", "a long viewer password 2026")
	if err := s.CreateAccount(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListPublishers(ctx, "viewer", "", 21); !errors.Is(err, ErrForbidden) {
		t.Fatal("viewer read directory", err)
	}
}
func TestMemoryPublisherDirectoryContract(t *testing.T) { directoryContract(t, NewMemoryStore()) }
func TestFirestorePublisherDirectoryContract(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	s, err := NewFirestoreStore(context.Background(), fmt.Sprintf("demo-directory-%x", time.Now().UnixNano()), "community")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	directoryContract(t, s)
}
func TestPublisherDirectoryAPIIsOperatorOnly(t *testing.T) {
	h, s, _, _, staff := managedFixture(t)
	op := operatorCookie(t, s)
	r, data := call(t, h, "GET", "/api/v1/operations/publishers", "", staff, nil)
	if r.StatusCode != 401 {
		t.Fatal("staff cookie reached operations", r.StatusCode, string(data))
	}
	r, data = call(t, h, "GET", "/api/v1/operations/publishers", "", op, nil)
	var result struct {
		Publishers []struct {
			Account publisher.Account
			Status  publisher.Status
		}
		Cursor string `json:"next_cursor"`
	}
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" || json.Unmarshal(data, &result) != nil || len(result.Publishers) != 1 || result.Publishers[0].Status != publisher.Active {
		t.Fatal(r.StatusCode, string(data))
	}
	for _, secret := range []string{"password", "salt", "hash", "staff_accounts"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("directory leaked private fields")
		}
	}
}
