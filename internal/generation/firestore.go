package generation

import (
	"context"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
	"google.golang.org/api/option"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type FirestoreStore struct{ client *firestore.Client }
type admission struct {
	Used   int    `firestore:"used"`
	Active string `firestore:"active"`
}

func NewFirestoreStore(ctx context.Context, project, database string, opts ...option.ClientOption) (*FirestoreStore, error) {
	c, err := firestore.NewClientWithDatabase(ctx, project, database, opts...)
	if err != nil {
		return nil, err
	}
	return &FirestoreStore{client: c}, nil
}

func (s *FirestoreStore) Close() error { return s.client.Close() }
func (s *FirestoreStore) jobs() *firestore.CollectionRef {
	return s.client.Collection("generation_jobs")
}
func (s *FirestoreStore) ledger() *firestore.DocumentRef {
	return s.client.Collection("generation_control").Doc("admission")
}

func decodeJob(snapshot *firestore.DocumentSnapshot, err error) (Job, error) {
	if status.Code(err) == codes.NotFound {
		return Job{}, ErrNotFound
	}
	if err != nil {
		return Job{}, err
	}
	var job Job
	if err := snapshot.DataTo(&job); err != nil {
		return Job{}, err
	}
	return job, nil
}

func (s *FirestoreStore) Get(ctx context.Context, id string) (Job, error) {
	return decodeJob(s.jobs().Doc(id).Get(ctx))
}

func (s *FirestoreStore) List(ctx context.Context, owner string) ([]Job, error) {
	snapshots, err := s.jobs().Where("owner", "==", owner).Limit(MaxAttempts).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	jobs := make([]Job, 0, len(snapshots))
	for _, snap := range snapshots {
		job, err := decodeJob(snap, nil)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	// The entire release has at most ten jobs; sorting these avoids a composite
	// index and keeps the per-account query bounded.
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].CreatedAt.After(jobs[j].CreatedAt) })
	return jobs, nil
}

func (s *FirestoreStore) Active(ctx context.Context) (*Job, error) {
	snapshot, err := s.ledger().Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var a admission
	if err := snapshot.DataTo(&a); err != nil {
		return nil, err
	}
	if a.Active == "" {
		return nil, nil
	}
	job, err := s.Get(ctx, a.Active)
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *FirestoreStore) Admit(ctx context.Context, owner string, preset Preset, nonce string, maximum int, now time.Time) (job Job, created bool, err error) {
	return s.AdmitScoped(ctx, OperatorPrincipal(owner), preset, nonce, maximum, now)
}

func (s *FirestoreStore) AdmitScoped(ctx context.Context, p Principal, preset Preset, nonce string, maximum int, now time.Time) (job Job, created bool, err error) {
	if !p.valid() || !p.permitsPreset(preset) {
		return Job{}, false, ErrInvalid
	}
	owner := p.ownerKey()
	ref := s.jobs().Doc(requestJobID(owner, nonce))
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		created = false
		var fence PublisherLifecycle
		if p.Kind == "staff" {
			doc, e := tx.Get(s.lifecycleDoc(p.Account.ID))
			fence, e = decodeLifecycle(doc, e, p.Account.ID)
			if e != nil {
				return e
			}
			if fence.State != publisher.Active {
				return ErrNotFound
			}
		}
		old, err := decodeJob(tx.Get(ref))
		if err == nil {
			if !p.owns(old) || old.Preset != preset.ID {
				return ErrConflict
			}
			job = old
			return nil
		}
		if err != ErrNotFound {
			return err
		}
		if p.Kind == "staff" {
			doc, err := tx.Get(s.grantsDoc(p.Account.ID))
			grants, err := decodeGrants(doc, err, p.Account.ID)
			if err != nil {
				return err
			}
			if grants != nil && !grants.allows(preset.ID) {
				return ErrInvalid
			}
		}
		var a admission
		snapshot, err := tx.Get(s.ledger())
		if err == nil {
			if err := snapshot.DataTo(&a); err != nil {
				return err
			}
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		if a.Used < 0 || a.Used >= maximum {
			return ErrBudget
		}
		if a.Active != "" {
			return ErrBusy
		}
		job = Job{ID: ref.ID, Owner: owner, PublisherAccountID: p.accountID(), CreatedBy: p.actor(), Preset: preset.ID, Snapshot: preset, State: Submitting, CreatedAt: now, UpdatedAt: now}
		if p.Kind == "staff" {
			fence.Revision++
			fence.UpdatedAt = now
			if err := tx.Set(s.lifecycleDoc(p.Account.ID), fence); err != nil {
				return err
			}
		}
		if err := tx.Create(ref, job); err != nil {
			return err
		}
		if err := tx.Set(s.ledger(), admission{Used: a.Used + 1, Active: job.ID}); err != nil {
			return err
		}
		created = true
		return nil
	})
	return
}

func nextJob(old Job, name string, state State, now time.Time) (Job, error) {
	if old.State.Terminal() {
		return old, nil
	}
	switch state {
	case Submitting, Queued, Running, Cancelling, Succeeded, Failed, Cancelled:
	default:
		return Job{}, fmt.Errorf("invalid provider state %q", state)
	}
	if old.ProviderName != "" && name != "" && name != old.ProviderName {
		return Job{}, ErrConflict
	}
	if name != "" {
		old.ProviderName = name
	}
	if old.State == Cancelling && !state.Terminal() || old.State == Running && state == Queued {
		return old, nil
	}
	if old.State != state {
		old.State = state
		old.UpdatedAt = now
	}
	return old, nil
}

func (s *FirestoreStore) Transition(ctx context.Context, id, name string, state State, now time.Time) (result Job, err error) {
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		old, err := decodeJob(tx.Get(s.jobs().Doc(id)))
		if err != nil {
			return err
		}
		result, err = nextJob(old, name, state, now)
		if err != nil || result.State == old.State && result.ProviderName == old.ProviderName && result.UpdatedAt.Equal(old.UpdatedAt) {
			return err
		}
		var a admission
		if result.State.Terminal() {
			snap, err := tx.Get(s.ledger())
			if err != nil {
				return err
			}
			if err := snap.DataTo(&a); err != nil {
				return err
			}
		}
		if err := tx.Set(s.jobs().Doc(id), result); err != nil {
			return err
		}
		if result.State.Terminal() && a.Active == id {
			return tx.Update(s.ledger(), []firestore.Update{{Path: "active", Value: ""}})
		}
		return nil
	})
	return
}

// One aggregate document bounds every public feed revalidation to one read.
// The admission ledger caps the entire release at MaxAttempts jobs.
type publicRegistry struct {
	Entries []Publication `firestore:"entries"`
}

func (s *FirestoreStore) registry() *firestore.DocumentRef {
	return s.client.Collection("generation_control").Doc("public_feed")
}
func decodeRegistry(snapshot *firestore.DocumentSnapshot, err error) (publicRegistry, error) {
	if status.Code(err) == codes.NotFound {
		return publicRegistry{}, nil
	}
	if err != nil {
		return publicRegistry{}, err
	}
	var registry publicRegistry
	if err := snapshot.DataTo(&registry); err != nil {
		return registry, err
	}
	if len(registry.Entries) > MaxAttempts {
		return publicRegistry{}, ErrBudget
	}
	seen := map[string]bool{}
	for _, entry := range registry.Entries {
		if !requestID.MatchString(entry.JobID) || seen[entry.JobID] || entry.Clip.ID != "generated-"+entry.JobID || entry.Clip.ValidateGenerated() != nil || !entry.validProofs() {
			return publicRegistry{}, fmt.Errorf("invalid publication registry")
		}
		seen[entry.JobID] = true
	}
	return registry, nil
}
func (s *FirestoreStore) Publications(ctx context.Context) ([]Publication, error) {
	registry, err := decodeRegistry(s.registry().Get(ctx))
	return registry.Entries, err
}
func (s *FirestoreStore) SetPublication(ctx context.Context, owner, id string, publication *Publication, now time.Time) (result Job, err error) {
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		job, err := decodeJob(tx.Get(s.jobs().Doc(id)))
		if err != nil {
			return err
		}
		if job.Owner != owner {
			return ErrNotFound
		}
		if job.PublisherAccountID != "" {
			doc, e := tx.Get(s.lifecycleDoc(job.PublisherAccountID))
			fence, e := decodeLifecycle(doc, e, job.PublisherAccountID)
			if e != nil {
				return e
			}
			if fence.State != publisher.Active {
				return ErrNotFound
			}
		}
		if job.State != Succeeded {
			return ErrConflict
		}
		registry, err := decodeRegistry(tx.Get(s.registry()))
		if err != nil {
			return err
		}
		result = job
		if job.Published == (publication != nil) {
			return nil
		}
		entries := make([]Publication, 0, len(registry.Entries)+1)
		for _, entry := range registry.Entries {
			if entry.JobID != id {
				entries = append(entries, entry)
			}
		}
		if publication != nil {
			if publication.JobID != id || publication.Clip.ID != "generated-"+id || publication.Clip.ValidateGenerated() != nil || !publication.validProofs() {
				return ErrInvalid
			}
			if len(entries) >= MaxAttempts {
				return ErrBudget
			}
			entries = append(entries, *publication)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].JobID < entries[j].JobID })
		result.Published = publication != nil
		result.UpdatedAt = now
		if err := tx.Update(s.jobs().Doc(id), []firestore.Update{{Path: "published", Value: result.Published}, {Path: "updated_at", Value: now}}); err != nil {
			return err
		}
		return tx.Set(s.registry(), publicRegistry{Entries: entries})
	})
	return
}
