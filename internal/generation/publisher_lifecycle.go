package generation

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// This document is also the admission/publication fence. Deletion and a late
// admission both write it, so an accepted job cannot escape the ownership set.
type PublisherLifecycle struct {
	Account   publisher.ID     `firestore:"publisher_account_id" json:"account"`
	State     publisher.Status `firestore:"state" json:"state"`
	RequestID string           `firestore:"request_id,omitempty" json:"request_id,omitempty"`
	Jobs      []string         `firestore:"jobs,omitempty" json:"jobs"`
	Stage     string           `firestore:"stage,omitempty" json:"stage,omitempty"`
	Prefix    int              `firestore:"prefix,omitempty" json:"prefix"`
	Operation string           `firestore:"operation,omitempty" json:"-"`
	StartedBy string           `firestore:"started_by,omitempty" json:"-"`
	UpdatedAt time.Time        `firestore:"updated_at" json:"updated_at"`
	Revision  int64            `firestore:"revision" json:"-"`
}

type PublisherLifecycleStore interface {
	PublisherLifecycle(context.Context, publisher.ID) (PublisherLifecycle, error)
	BeginPublisherCleanup(context.Context, publisher.ID, string, string, time.Time) (PublisherLifecycle, error)
	AdvancePublisherCleanup(context.Context, PublisherLifecycle, PublisherLifecycle) error
}

func (d PublisherLifecycle) valid(id publisher.ID) bool {
	if !id.Valid() || d.Account != id || d.Revision < 0 {
		return false
	}
	if d.State == publisher.Active {
		return d.RequestID == "" && len(d.Jobs) == 0 && d.Stage == "" && d.Prefix == 0 && d.Operation == ""
	}
	if (d.State != publisher.Deleting && d.State != publisher.Deleted) || !requestID.MatchString(d.RequestID) || !operatorIdentifier.MatchString(d.StartedBy) || len(d.Jobs) > MaxAttempts || d.Prefix < 0 || d.Prefix > 3*len(d.Jobs) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range d.Jobs {
		if !requestID.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	switch d.Stage {
	case "jobs":
		return d.State == publisher.Deleting && d.Prefix == 0 && d.Operation == ""
	case "objects":
		return d.State == publisher.Deleting && d.Operation == ""
	case "cache":
		return d.State == publisher.Deleting && (d.Operation == "" || d.Prefix < 3*len(d.Jobs) && operationName.MatchString(d.Operation))
	case "complete":
		return d.State == publisher.Deleted && d.Prefix == 3*len(d.Jobs) && d.Operation == ""
	default:
		return false
	}
}

func (s *FirestoreStore) lifecycleDoc(id publisher.ID) *firestore.DocumentRef {
	return s.client.Collection("generation_publishers").Doc(string(id))
}
func decodeLifecycle(doc *firestore.DocumentSnapshot, err error, id publisher.ID) (PublisherLifecycle, error) {
	if status.Code(err) == codes.NotFound {
		return PublisherLifecycle{Account: id, State: publisher.Active}, nil
	}
	if err != nil {
		return PublisherLifecycle{}, err
	}
	var d PublisherLifecycle
	if err := doc.DataTo(&d); err != nil {
		return d, err
	}
	if !d.valid(id) {
		return d, fmt.Errorf("invalid publisher lifecycle")
	}
	return d, nil
}
func (s *FirestoreStore) PublisherLifecycle(ctx context.Context, id publisher.ID) (PublisherLifecycle, error) {
	if !id.Valid() {
		return PublisherLifecycle{}, ErrNotFound
	}
	doc, err := s.lifecycleDoc(id).Get(ctx)
	return decodeLifecycle(doc, err, id)
}
func (s *Service) publisherAccess(ctx context.Context, id publisher.ID) error {
	if id == "" {
		return nil
	}
	if store, ok := s.Store.(PublisherLifecycleStore); ok {
		d, err := store.PublisherLifecycle(ctx, id)
		if err != nil {
			return err
		}
		if d.State != publisher.Active {
			return ErrNotFound
		}
	}
	return nil
}
func (s *Service) principalAccess(ctx context.Context, p Principal) error {
	if p.Kind == "staff" && p.Account != nil {
		return s.publisherAccess(ctx, p.Account.ID)
	}
	return nil
}

func (s *FirestoreStore) BeginPublisherCleanup(ctx context.Context, account publisher.ID, rid, actor string, now time.Time) (result PublisherLifecycle, err error) {
	if !account.Valid() || !requestID.MatchString(rid) || !operatorIdentifier.MatchString(actor) {
		return result, ErrInvalid
	}
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(s.lifecycleDoc(account))
		d, err := decodeLifecycle(doc, err, account)
		if err != nil {
			return err
		}
		if d.State != publisher.Active {
			if d.RequestID != rid {
				return ErrConflict
			}
			result = d
			return nil
		}
		docs, err := tx.Documents(s.jobs().Where("owner", "==", "publisher:"+string(account)).Limit(MaxAttempts + 1)).GetAll()
		if err != nil {
			return err
		}
		if len(docs) > MaxAttempts {
			return ErrBudget
		}
		ids := make([]string, 0, len(docs))
		for _, doc := range docs {
			job, err := decodeJob(doc, nil)
			if err != nil {
				return err
			}
			if job.ID != doc.Ref.ID || !requestID.MatchString(job.ID) || job.PublisherAccountID != account || job.Owner != "publisher:"+string(account) {
				return ErrConflict
			}
			ids = append(ids, job.ID)
		}
		registry, err := decodeRegistry(tx.Get(s.registry()))
		if err != nil {
			return err
		}
		entries := make([]Publication, 0, len(registry.Entries))
		for _, e := range registry.Entries {
			if !slices.Contains(ids, e.JobID) {
				entries = append(entries, e)
			}
		}
		sort.Strings(ids)
		result = PublisherLifecycle{Account: account, State: publisher.Deleting, RequestID: rid, Jobs: ids, Stage: "jobs", StartedBy: actor, UpdatedAt: now.UTC(), Revision: d.Revision + 1}
		for _, id := range ids {
			if err := tx.Update(s.jobs().Doc(id), []firestore.Update{{Path: "published", Value: false}}); err != nil {
				return err
			}
		}
		if err := tx.Set(s.registry(), publicRegistry{Entries: entries}); err != nil {
			return err
		}
		return tx.Set(s.lifecycleDoc(account), result)
	})
	return
}

func (s *FirestoreStore) AdvancePublisherCleanup(ctx context.Context, old, next PublisherLifecycle) error {
	if !old.valid(old.Account) || !next.valid(old.Account) || old.State != publisher.Deleting || next.RequestID != old.RequestID || !slices.Equal(next.Jobs, old.Jobs) || next.StartedBy != old.StartedBy || next.Revision != old.Revision+1 {
		return ErrInvalid
	}
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(s.lifecycleDoc(old.Account))
		d, err := decodeLifecycle(doc, err, old.Account)
		if err != nil {
			return err
		}
		if d.Revision != old.Revision || d.RequestID != old.RequestID || d.State != publisher.Deleting || !slices.Equal(d.Jobs, old.Jobs) {
			return ErrConflict
		}
		return tx.Set(s.lifecycleDoc(old.Account), next)
	})
}

// AssetCleanup only receives server-derived job prefixes. It has no dataset or
// account-deletion capability and cannot accept a browser-supplied bucket/URL.
type AssetCleanup interface {
	Purge(context.Context, string) (bool, error)
	Invalidate(context.Context, string, string) (string, error)
	InvalidationDone(context.Context, string) (bool, error)
}

func (s *Service) PublisherDeletionStatus(ctx context.Context, actor string, account publisher.ID) (PublisherLifecycle, error) {
	if !operatorIdentifier.MatchString(actor) {
		return PublisherLifecycle{}, ErrForbidden
	}
	store, ok := s.Store.(PublisherLifecycleStore)
	if !ok || s.Cleanup == nil {
		return PublisherLifecycle{}, fmt.Errorf("publisher cleanup unavailable")
	}
	d, err := store.PublisherLifecycle(ctx, account)
	if err != nil || d.State != publisher.Deleting {
		return d, err
	}
	return s.advancePublisherCleanup(ctx, d)
}
func (s *Service) StartPublisherDeletion(ctx context.Context, actor string, account publisher.ID, rid string) (PublisherLifecycle, error) {
	if !operatorIdentifier.MatchString(actor) {
		return PublisherLifecycle{}, ErrForbidden
	}
	store, ok := s.Store.(PublisherLifecycleStore)
	if !ok || s.Cleanup == nil {
		return PublisherLifecycle{}, fmt.Errorf("publisher cleanup unavailable")
	}
	d, err := store.BeginPublisherCleanup(ctx, account, rid, actor, s.now())
	if err != nil || d.State != publisher.Deleting {
		return d, err
	}
	if s.Logger != nil {
		s.Logger.InfoContext(ctx, "publisher cleanup started", "publisher_account_id", account, "actor_id", actor, "job_count", len(d.Jobs))
	}
	return s.advancePublisherCleanup(ctx, d)
}

func (s *Service) advancePublisherCleanup(ctx context.Context, d PublisherLifecycle) (PublisherLifecycle, error) {
	if !d.valid(d.Account) {
		return d, ErrConflict
	}
	// Recheck ownership before any provider, object or CDN side effect.
	jobs := make([]Job, 0, len(d.Jobs))
	for _, id := range d.Jobs {
		job, err := s.Store.Get(ctx, id)
		if err != nil {
			return d, err
		}
		if job.ID != id || job.PublisherAccountID != d.Account || job.Owner != "publisher:"+string(d.Account) {
			return d, ErrConflict
		}
		jobs = append(jobs, job)
	}
	next := d
	switch d.Stage {
	case "jobs":
		for _, job := range jobs {
			job, err := s.refresh(ctx, job)
			if err != nil {
				return d, err
			}
			if job.State.Terminal() {
				continue
			}
			// An unknown submission may still be accepted. Leave it pending rather
			// than erase files while that job can still materialize/write them.
			if job.ProviderName == "" {
				return d, nil
			}
			if job.State != Cancelling {
				if err := s.Provider.Cancel(ctx, job.ProviderName); err != nil {
					return d, err
				}
				if _, err := s.Store.Transition(ctx, job.ID, job.ProviderName, Cancelling, s.now()); err != nil {
					return d, err
				}
			}
			return d, nil
		}
		next.Stage = "objects"
	case "objects":
		if d.Prefix < 3*len(d.Jobs) {
			empty, err := s.Cleanup.Purge(ctx, cleanupPrefix(d.Jobs, d.Prefix))
			if err != nil {
				return d, err
			}
			if !empty {
				return d, nil
			}
			next.Prefix++
		} else {
			next.Stage = "cache"
			next.Prefix = 0
		}
	case "cache":
		if d.Prefix == 3*len(d.Jobs) {
			next.Stage = "complete"
			next.State = publisher.Deleted
		} else if d.Operation == "" {
			op, err := s.Cleanup.Invalidate(ctx, cleanupPrefix(d.Jobs, d.Prefix), invalidationID(d.RequestID, d.Prefix))
			if err != nil {
				return d, err
			}
			if op == "" {
				return d, ErrConflict
			}
			next.Operation = op
		} else {
			done, err := s.Cleanup.InvalidationDone(ctx, d.Operation)
			if err != nil {
				return d, err
			}
			if !done {
				return d, nil
			}
			next.Operation = ""
			next.Prefix++
		}
	default:
		return d, ErrConflict
	}
	next.Revision++
	next.UpdatedAt = s.now()
	if err := s.Store.(PublisherLifecycleStore).AdvancePublisherCleanup(ctx, d, next); err != nil {
		return d, err
	}
	return next, nil
}

func cleanupPrefix(jobs []string, index int) string {
	id := jobs[index/3]
	switch index % 3 {
	case 0:
		return "generated/" + id + "/"
	case 1:
		return "live/" + id + "/"
	default:
		return "live-index/" + id + ".json"
	}
}

// The existing authenticated scheduler can advance one bounded cleanup step.
// Closing the management screen never reopens admission or restores media.
func (s *Service) syncPublisherCleanup(ctx context.Context) error {
	store, ok := s.Store.(interface {
		PendingPublisherCleanup(context.Context) (*PublisherLifecycle, error)
	})
	if !ok || s.Cleanup == nil {
		return nil
	}
	d, err := store.PendingPublisherCleanup(ctx)
	if err != nil || d == nil {
		return err
	}
	_, err = s.advancePublisherCleanup(ctx, *d)
	if err == ErrConflict {
		return nil
	} // another poll committed first
	return err
}
func (s *FirestoreStore) PendingPublisherCleanup(ctx context.Context) (*PublisherLifecycle, error) {
	docs, err := s.client.Collection("generation_publishers").Where("state", "==", publisher.Deleting).Limit(1).Documents(ctx).GetAll()
	if err != nil || len(docs) == 0 {
		return nil, err
	}
	d, err := decodeLifecycle(docs[0], nil, publisher.ID(docs[0].Ref.ID))
	return &d, err
}
