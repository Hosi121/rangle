package generation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

type grantMemory struct {
	*memoryStore
	grants map[publisher.ID]PublisherGrants
}

func newGrantMemory() *grantMemory {
	return &grantMemory{newMemory(), map[publisher.ID]PublisherGrants{}}
}
func (m *grantMemory) PublisherGrants(_ context.Context, account publisher.ID) (*PublisherGrants, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.grants[account]
	if !ok {
		return nil, nil
	}
	g.Presets = append([]string{}, g.Presets...)
	return &g, nil
}
func (m *grantMemory) SetPublisherGrants(_ context.Context, account publisher.ID, ids []string, operator string, now time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	g := PublisherGrants{account, append([]string{}, ids...), now, operator}
	if !g.valid(account) {
		return ErrInvalid
	}
	m.grants[account] = g
	return nil
}

func TestOperatorAssignsReviewedInputsThroughLocalAndRemoteAPI(t *testing.T) {
	for name, remote := range map[string]bool{"local": false, "manager": true} {
		t.Run(name, func(t *testing.T) {
			s, provider := testService()
			s.Store = newGrantMemory()
			auth := func(*http.Request) (string, error) { return "admin", nil }
			var grantHandler, staffHandler http.Handler
			var err error
			p := staffPrincipal("club-a", "staff-a")
			if remote {
				client, _ := remoteFixture(t, s)
				grantHandler, err = NewPublisherGrantRemoteHandler(client, auth)
				if err != nil {
					t.Fatal(err)
				}
				staffHandler, err = NewPublisherRemoteHandler(client, func(*http.Request) (Principal, error) { return p, nil })
			} else {
				grantHandler, err = NewPublisherGrantHandler(s, auth)
				if err != nil {
					t.Fatal(err)
				}
				staffHandler, err = NewPublisherHandler(s, func(*http.Request) (Principal, error) { return p, nil })
			}
			if err != nil {
				t.Fatal(err)
			}
			path := "/api/v1/operations/publishers/club-a/generation/presets"
			data := scopedRequest(t, grantHandler, "GET", path, "", 200)
			if !strings.Contains(string(data), `"enabled":false`) {
				t.Fatal("unassigned input was enabled")
			}
			data = scopedRequest(t, grantHandler, "PUT", path, `{"presets":["vru-frame0"]}`, 200)
			if !strings.Contains(string(data), `"enabled":true`) {
				t.Fatal("assignment not reflected")
			}
			scopedRequest(t, staffHandler, "GET", "/api/v1/publishers/club-a/generation/presets", "", 200)
			scopedRequest(t, staffHandler, "POST", "/api/v1/publishers/club-a/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(1)+`"}`, 202)
			if provider.submits.Load() != 1 {
				t.Fatal("assigned input could not create job")
			}
		})
	}
}

func TestGrantRevocationPreservesExistingOrganizationJobs(t *testing.T) {
	s, _ := testService()
	s.Store = newGrantMemory()
	ctx := context.Background()
	p := staffPrincipal("club-a", "staff-a")
	operator := OperatorPrincipal("admin")
	if err := s.SetInputGrants(ctx, operator, p.Account.ID, []string{"vru-frame0"}); err != nil {
		t.Fatal(err)
	}
	job, err := s.CreateFor(ctx, p, "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetInputGrants(ctx, operator, p.Account.ID, []string{}); err != nil {
		t.Fatal(err)
	}
	if rows, err := s.PresetsFor(ctx, p); err != nil || len(rows) != 0 {
		t.Fatal("revoked preset listed", err)
	}
	if _, err := s.CreateFor(ctx, p, "vru-frame0", nonce(2)); !errors.Is(err, ErrInvalid) {
		t.Fatal("revoked input admitted", err)
	}
	if retry, err := s.CreateFor(ctx, p, "vru-frame0", nonce(1)); err != nil || retry.ID != job.ID {
		t.Fatal("existing idempotency lost", err)
	}
	if _, err := s.CancelFor(ctx, staffPrincipal("club-a", "successor"), job.ID); err != nil {
		t.Fatal("revoked input prevented cancellation", err)
	}
	if err := s.SetInputGrants(ctx, p, p.Account.ID, []string{"vru-frame0"}); !errors.Is(err, ErrForbidden) {
		t.Fatal("staff could assign input", err)
	}
	for _, ids := range [][]string{{"unknown"}, {"vru-frame0", "vru-frame0"}, {"../input"}} {
		if err := s.SetInputGrants(ctx, operator, p.Account.ID, ids); !errors.Is(err, ErrInvalid) {
			t.Fatal("unreviewed/duplicate input accepted", ids, err)
		}
	}
}

func TestFirestoreGrantRevocationIsCheckedInAdmissionTransaction(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := NewFirestoreStore(ctx, fmt.Sprintf("demo-grants-%x", time.Now().UnixNano()), "generation")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	p := staffPrincipal("club-a", "staff-a")
	if err := store.SetPublisherGrants(ctx, p.Account.ID, []string{"vru-frame0"}, "admin", time.Now()); err != nil {
		t.Fatal(err)
	}
	preset := testConfig().Presets[0]
	preset.PublisherAccounts = []publisher.ID{p.Account.ID}
	preset = p.snapshot(preset)
	job, created, err := store.AdmitScoped(ctx, p, preset, nonce(1), 2, time.Now())
	if err != nil || !created {
		t.Fatal("assigned input admission", created, err)
	}
	if _, err := store.Transition(ctx, job.ID, "", Succeeded, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := store.SetPublisherGrants(ctx, p.Account.ID, []string{}, "admin", time.Now()); err != nil {
		t.Fatal(err)
	}
	// This is the stale, previously authorized preset snapshot. A new request
	// still re-reads the override with the reservation, before spending a slot.
	if _, _, err := store.AdmitScoped(ctx, p, preset, nonce(2), 2, time.Now()); !errors.Is(err, ErrInvalid) {
		t.Fatal("revocation raced admission", err)
	}
	if replay, created, err := store.AdmitScoped(ctx, p, preset, nonce(1), 2, time.Now()); err != nil || created || replay.ID != job.ID {
		t.Fatal("revocation lost admitted request", created, err)
	}
	if active, err := store.Active(ctx); err != nil || active != nil {
		t.Fatal("rejected admission spent active slot", err)
	}
}
