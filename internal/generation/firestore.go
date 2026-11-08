package generation

import (
	"context"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type FirestoreStore struct{ client *firestore.Client }
type admission struct {
	Used   int    `firestore:"used"`
	Active string `firestore:"active"`
}

func NewFirestoreStore(ctx context.Context, project, database string) (*FirestoreStore, error) {
	c, err := firestore.NewClientWithDatabase(ctx, project, database)
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
	ref := s.jobs().Doc(requestJobID(owner, nonce))
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		created = false
		old, err := decodeJob(tx.Get(ref))
		if err == nil {
			if old.Owner != owner || old.Preset != preset.ID {
				return ErrConflict
			}
			job = old
			return nil
		}
		if err != ErrNotFound {
			return err
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
		job = Job{ID: ref.ID, Owner: owner, Preset: preset.ID, Snapshot: preset, State: Submitting, CreatedAt: now, UpdatedAt: now}
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
		if err != nil || result == old {
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
