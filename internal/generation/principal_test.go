package generation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/publisher"
)

func staffPrincipal(account publisher.ID, id string) Principal {
	return Principal{Kind: "staff", ID: id, Account: &publisher.Account{ID: account, Name: "Example Club", Kind: "organization"}, Role: publisher.Editor, Generation: 1}
}
func TestOrganizationOwnsJobsAcrossStaffAndOperatorNamespaces(t *testing.T) {
	s, _ := testService()
	ctx := context.Background()
	a := staffPrincipal("club-a", "staff-a")
	other := staffPrincipal("club-b", "staff-a")
	successor := staffPrincipal("club-a", "staff-b")
	s.Config.Presets[0].PublisherAccounts = []publisher.ID{"club-a", "club-b"}
	job, err := s.CreateFor(ctx, a, "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	if job.PublisherAccountID != "club-a" || job.CreatedBy.ID != "staff-a" || job.CreatedBy.Kind != "staff" || job.Owner != "publisher:club-a" {
		t.Fatal("ownership was not sealed", job)
	}
	got, err := s.GetFor(ctx, successor, job.ID)
	if err != nil || got.ID != job.ID {
		t.Fatal("successor could not access organization job", err)
	}
	retry, err := s.CreateFor(ctx, successor, "vru-frame0", nonce(1))
	if err != nil || retry.ID != job.ID || retry.CreatedBy != job.CreatedBy {
		t.Fatal("retry rewrote creator or duplicated reservation", err)
	}
	if _, err := s.GetFor(ctx, other, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-organization read", err)
	}
	if _, err := s.CancelFor(ctx, other, job.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-organization cancellation", err)
	}
	if _, err := s.SetPublicationFor(ctx, other, job.ID, false); !errors.Is(err, ErrNotFound) {
		t.Fatal("cross-organization publication", err)
	}
	for _, legacyOwner := range []string{"admin", "publisher_club-a", "publisher:club-a"} {
		if _, err := s.Get(ctx, legacyOwner, job.ID); err == nil {
			t.Fatal("legacy operator namespace reached organization job", legacyOwner)
		}
	}
	rows, err := s.ListFor(ctx, successor)
	if err != nil || len(rows) != 1 {
		t.Fatal("organization list", len(rows), err)
	}
	if _, err := s.CreateFor(ctx, other, "vru-frame0", nonce(1)); !errors.Is(err, ErrBusy) {
		t.Fatal("tenant bypassed the global active slot", err)
	}
	job, err = s.CancelFor(ctx, successor, job.ID)
	if err != nil || job.State != Cancelling {
		t.Fatal("successor cancellation", job.State, err)
	}
}
func TestPresetAssignmentDefaultsClosedAndSnapshotsPostingOrganization(t *testing.T) {
	s, _ := testService()
	ctx := context.Background()
	p := staffPrincipal("club-a", "staff-a")
	if _, err := s.CreateFor(ctx, p, "vru-frame0", nonce(1)); !errors.Is(err, ErrInvalid) {
		t.Fatal("unassigned input granted", err)
	}
	if active, _ := s.Store.Active(ctx); active != nil {
		t.Fatal("unassigned request reserved GPU capacity")
	}
	s.Config.Presets[0].PublisherAccounts = []publisher.ID{"club-a"}
	job, err := s.CreateFor(ctx, p, "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	if job.Snapshot.Context.Publisher.ID != "club-a" || job.Snapshot.Source != s.Config.Presets[0].Source || job.Snapshot.InputURI != s.Config.Presets[0].InputURI {
		t.Fatal("publisher/provenance snapshot differs")
	}
	if s.Config.Presets[0].Context != nil {
		t.Fatal("request mutated reviewed preset")
	}
	p.Account.Name = "Changed later"
	stored, _ := s.Store.Get(ctx, job.ID)
	if stored.Snapshot.Context.Publisher.Name != "Example Club" {
		t.Fatal("creator profile mutation changed published snapshot")
	}
	data, _ := json.Marshal(job)
	for _, private := range []string{"publisher:club-a", "created_by", "generation", "input_uri"} {
		if strings.Contains(string(data), private) {
			t.Fatal("private admission fields leaked", private)
		}
	}
}
func TestPrincipalRejectsMixedAndInactiveAuthority(t *testing.T) {
	for name, p := range map[string]Principal{
		"viewer":             {Kind: "viewer", ID: "staff-a"},
		"mixed operator":     {Kind: "operator", ID: "admin", Account: &publisher.Account{ID: "club-a", Name: "Club", Kind: "organization"}},
		"operator role":      {Kind: "operator", ID: "admin", Role: publisher.Administrator},
		"missing generation": {Kind: "staff", ID: "staff-a", Account: &publisher.Account{ID: "club-a", Name: "Club", Kind: "organization"}, Role: publisher.Editor},
		"suspended":          {Kind: "staff", ID: "staff-a", Account: &publisher.Account{ID: "club-a", Name: "Club", Kind: "organization", Status: publisher.Suspended}, Role: publisher.Editor, Generation: 1},
	} {
		if p.allows(publisher.CreateGeneration) {
			t.Fatal("invalid principal authorized", name)
		}
	}
	if OperatorPrincipal("admin").allows(publisher.ManageStaff) {
		t.Fatal("generation principal gained unrelated permissions")
	}
}
func TestPresetPublisherAssignmentValidation(t *testing.T) {
	for _, ids := range [][]publisher.ID{{"../club"}, {"club", "club"}, make([]publisher.ID, 101)} {
		cfg := testConfig()
		cfg.Presets[0].PublisherAccounts = ids
		if cfg.Validate() == nil {
			t.Fatal("invalid publisher assignment accepted", ids)
		}
	}
}
