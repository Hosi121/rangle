package community

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

type subscriptionContractStore interface {
	Store
	PublisherStore
	SubscriptionStore
	PublisherDeletionStore
}

func subscriptionContract(t *testing.T, s subscriptionContractStore) {
	t.Helper()
	ctx := context.Background()
	for _, id := range []string{"viewer", "other", "admin"} {
		a := newAccount(id, id, "a long test password 2026")
		a.User.Admin = id == "admin"
		if err := s.CreateAccount(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	a, staff := publisherFixture(t, "club-00")
	for i := range 22 {
		a.ID = publisher.ID(fmt.Sprintf("club-%02d", i))
		staff.Staff.PublisherAccountID = a.ID
		if err := s.ProvisionPublisher(ctx, a, staff); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSubscription(ctx, "viewer", a.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := s.SetSubscription(ctx, "viewer", "club-00", true); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	rows, err := s.Subscriptions(ctx, "viewer", "", 21)
	if err != nil || len(rows) != 21 || rows[0].ID != "club-00" || rows[20].ID != "club-20" {
		t.Fatal("stable bounded page", rows, err)
	}
	next, err := s.Subscriptions(ctx, "viewer", rows[20].ID, 21)
	if err != nil || len(next) != 1 || next[0].ID != "club-21" {
		t.Fatal("continuation", next, err)
	}
	if rows, err := s.Subscriptions(ctx, "other", "", 21); err != nil || len(rows) != 0 {
		t.Fatal("cross-user leak", rows, err)
	}
	for _, value := range []bool{false, false} {
		if err := s.SetSubscription(ctx, "viewer", "club-21", value); err != nil {
			t.Fatal(err)
		}
	}
	if followed, err := s.Subscribed(ctx, "viewer", "club-21"); err != nil || followed {
		t.Fatal("unfollow not idempotent", followed, err)
	}
	if err := s.SetSubscription(ctx, "viewer", "missing", true); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown publisher accepted", err)
	}
	if _, err := s.BeginPublisherDeletion(ctx, "admin", "club-00", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSubscription(ctx, "viewer", "club-00", true); !errors.Is(err, ErrNotFound) {
		t.Fatal("follow during deletion", err)
	}
	if _, err := s.Subscribed(ctx, "viewer", "club-00"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleting profile readable", err)
	}
	if err := s.SetSubscription(ctx, "viewer", "club-00", false); err != nil {
		t.Fatal("cannot remove deleted relation", err)
	}
}
func TestMemorySubscriptionsContract(t *testing.T) { subscriptionContract(t, NewMemoryStore()) }
func TestFirestoreSubscriptionsContract(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	project := fmt.Sprintf("demo-subscriptions-%x", time.Now().UnixNano())
	s, err := NewFirestoreStore(context.Background(), project, "community")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	subscriptionContract(t, s)
	reopened, err := NewFirestoreStore(context.Background(), project, "community")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rows, err := reopened.Subscriptions(context.Background(), "viewer", "", 21)
	if err != nil || len(rows) != 20 {
		t.Fatal("subscriptions lost on restart", rows, err)
	}
}

func TestPublicPublisherAndViewerSubscriptions(t *testing.T) {
	h, s, a, _, staff := managedFixture(t)
	viewer := registerUser(t, h, "viewer")
	other := registerUser(t, h, "other")
	base := "/api/v1/publishers/" + string(a.ID)
	r, body := call(t, h, "GET", base, "", nil, nil)
	if r.StatusCode != 200 || !strings.Contains(string(body), a.Name) {
		t.Fatal(r.StatusCode, string(body))
	}
	for _, secret := range []string{"staff", "password", "hash", "salt", "status", "deletion"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("private profile field", secret)
		}
	}
	for _, cookie := range []*http.Cookie{nil, staff} {
		r, _ := call(t, h, "PUT", base+"/subscription", "", cookie, nil)
		if r.StatusCode != 401 {
			t.Fatal("staff or anonymous followed as viewer", r.StatusCode)
		}
	}
	for _, method := range []string{"PUT", "PUT", "GET"} {
		r, body = call(t, h, method, base+"/subscription", "", viewer, nil)
		if r.StatusCode != 200 || string(body) != "{\"subscribed\":true}\n" || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(method, r.StatusCode, string(body))
		}
	}
	r, body = call(t, h, "GET", base+"/subscription", "", other, nil)
	if r.StatusCode != 200 || string(body) != "{\"subscribed\":false}\n" {
		t.Fatal("cross-user follow", r.StatusCode, string(body))
	}
	r, _ = call(t, h, "PUT", base+"/subscription", `{"user":"other"}`, viewer, nil)
	if r.StatusCode != 400 {
		t.Fatal("body can choose viewer", r.StatusCode)
	}
	r, _ = call(t, h, "PUT", base+"/subscription", "", viewer, map[string]string{"Origin": "https://other.test"})
	if r.StatusCode != 403 {
		t.Fatal("cross-origin mutation", r.StatusCode)
	}
	r, _ = call(t, h, "DELETE", base+"/subscription", "", viewer, nil)
	if r.StatusCode != 200 {
		t.Fatal("unfollow", r.StatusCode)
	}
	s.mu.Lock()
	a.Status = publisher.Deleting
	s.publishers[a.ID] = a
	s.mu.Unlock()
	r, _ = call(t, h, "GET", base, "", nil, nil)
	if r.StatusCode != 404 {
		t.Fatal("deleting publisher exposed", r.StatusCode)
	}
}

func TestSubscriptionListCursorIsPersonalAndSkipsInactiveProfiles(t *testing.T) {
	h, s := testHandler()
	viewer := registerUser(t, h, "viewer")
	other := registerUser(t, h, "other")
	a, staff := publisherFixture(t, "club-00")
	for i := range 22 {
		a.ID = publisher.ID(fmt.Sprintf("club-%02d", i))
		staff.Staff.PublisherAccountID = a.ID
		if err := s.ProvisionPublisher(context.Background(), a, staff); err != nil {
			t.Fatal(err)
		}
		if err := s.SetSubscription(context.Background(), "viewer", a.ID, true); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	a = s.publishers["club-00"]
	a.Status = publisher.Suspended
	s.publishers[a.ID] = a
	s.mu.Unlock()
	r, data := call(t, h, "GET", "/api/v1/me/subscriptions", "", viewer, nil)
	var page struct {
		Publishers []publisher.Account `json:"publishers"`
		Cursor     string              `json:"next_cursor"`
	}
	if r.StatusCode != 200 || json.Unmarshal(data, &page) != nil || len(page.Publishers) != 19 || page.Cursor == "" {
		t.Fatal(r.StatusCode, string(data))
	}
	cursor := url.QueryEscape(page.Cursor)
	r, data = call(t, h, "GET", "/api/v1/me/subscriptions?cursor="+cursor, "", viewer, nil)
	if r.StatusCode != 200 || json.Unmarshal(data, &page) != nil || len(page.Publishers) != 2 || page.Cursor != "" {
		t.Fatal(r.StatusCode, string(data))
	}
	for _, suffix := range []string{"?cursor=" + cursor, "?user=viewer", "?cursor=bad", "?cursor=x&cursor=y"} {
		r, _ = call(t, h, "GET", "/api/v1/me/subscriptions"+suffix, "", other, nil)
		if r.StatusCode != 400 {
			t.Fatal("invalid personal cursor", suffix, r.StatusCode)
		}
	}
}

func TestSubscriptionReadAdmissionPrecedesSessionAndDirectoryReads(t *testing.T) {
	h, store, cookie := newPublicReadHandler(t)
	if !h.readRate.AllowN(time.Now(), publicReadBurst) {
		t.Fatal("could not exhaust read budget")
	}
	before := store.snapshot()
	for _, path := range []string{"/api/v1/publishers/club-a", "/api/v1/publishers/club-a/subscription", "/api/v1/me/subscriptions"} {
		for _, method := range []string{"GET", "HEAD"} {
			r, body := call(t, h, method, path, "", cookie, nil)
			assertPublicReadLimited(t, r, body, method)
		}
	}
	if store.snapshot() != before {
		t.Fatal("rejected request reached storage")
	}
}
