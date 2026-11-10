package community

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var deletionClipID = regexp.MustCompile(`^generated-[a-f0-9]{32}$`)
var publisherDeletionID = regexp.MustCompile(`^[a-f0-9]{32}$`)

type PublisherDeletion struct {
	Account     publisher.ID `firestore:"publisher_account_id"`
	RequestID   string       `firestore:"request_id"`
	StartedBy   string       `firestore:"started_by"`
	StartedAt   time.Time    `firestore:"started_at"`
	CompletedAt time.Time    `firestore:"completed_at,omitempty"`
}
type PublisherDeletionStore interface {
	PublisherStore
	RequestPublisherDeletion(context.Context, StaffManager, publisher.ID, bool) (publisher.Account, error)
	BeginPublisherDeletion(context.Context, string, publisher.ID, time.Time) (PublisherDeletion, error)
	PublisherDeletion(context.Context, string, publisher.ID) (*PublisherDeletion, error)
	FinishPublisherDeletion(context.Context, string, publisher.ID, string, []string, time.Time) error
	DeletedClips(context.Context, []string) (map[string]bool, error)
}

func deletionID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	return hex.EncodeToString(b), err
}
func validDeletion(d PublisherDeletion, account publisher.ID) bool {
	return d.Account == account && account.Valid() && publisherDeletionID.MatchString(d.RequestID) && validUsername(d.StartedBy) && !d.StartedAt.IsZero()
}
func validDeletedClips(ids []string) bool {
	if len(ids) > 10 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !deletionClipID.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (s *MemoryStore) operatorPublisher(operator string, id publisher.ID) (publisher.Account, error) {
	u, ok := s.accounts[operator]
	if !ok || !u.User.Admin || u.User.ID != operator {
		return publisher.Account{}, ErrForbidden
	}
	a, ok := s.publishers[id]
	if !ok {
		return a, ErrNotFound
	}
	return a, nil
}
func (s *MemoryStore) RequestPublisherDeletion(_ context.Context, m StaffManager, id publisher.ID, requested bool) (publisher.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.OperatorID != "" {
		return publisher.Account{}, ErrForbidden
	}
	a, err := s.staffManagement(id, m)
	if err != nil {
		return a, err
	}
	row := s.staff[id][m.Principal.StaffAccountID]
	if !a.Allows(m.Principal, row.Staff, publisher.RequestDeletion) {
		return a, ErrForbidden
	}
	a.DeletionRequested = requested
	s.publishers[id] = a
	return a, nil
}
func (s *MemoryStore) BeginPublisherDeletion(_ context.Context, operator string, id publisher.ID, now time.Time) (PublisherDeletion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.operatorPublisher(operator, id)
	if err != nil {
		return PublisherDeletion{}, err
	}
	if a.Status == publisher.Deleting || a.Status == publisher.Deleted {
		d, ok := s.deletions[id]
		if !ok || !validDeletion(d, id) {
			return d, ErrConflict
		}
		return d, nil
	}
	if a.Status != publisher.Active && a.Status != publisher.Suspended {
		return PublisherDeletion{}, ErrForbidden
	}
	rid, err := deletionID()
	if err != nil {
		return PublisherDeletion{}, err
	}
	d := PublisherDeletion{Account: id, RequestID: rid, StartedBy: operator, StartedAt: now.UTC()}
	if s.deletions == nil {
		s.deletions = map[publisher.ID]PublisherDeletion{}
	}
	a.Status = publisher.Deleting
	s.publishers[id] = a
	s.deletions[id] = d
	return d, nil
}
func (s *MemoryStore) PublisherDeletion(_ context.Context, operator string, id publisher.ID) (*PublisherDeletion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.operatorPublisher(operator, id); err != nil {
		return nil, err
	}
	d, ok := s.deletions[id]
	if !ok {
		return nil, nil
	}
	if !validDeletion(d, id) {
		return nil, ErrConflict
	}
	return &d, nil
}
func (s *MemoryStore) FinishPublisherDeletion(_ context.Context, operator string, id publisher.ID, rid string, clips []string, now time.Time) error {
	if !validDeletedClips(clips) {
		return ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.operatorPublisher(operator, id)
	if err != nil {
		return err
	}
	d, ok := s.deletions[id]
	if !ok || !validDeletion(d, id) || d.RequestID != rid || (a.Status != publisher.Deleting && a.Status != publisher.Deleted) {
		return ErrConflict
	}
	if a.Status == publisher.Deleted {
		return nil
	}
	if s.deletedClips == nil {
		s.deletedClips = map[string]publisher.ID{}
	}
	for _, clip := range clips {
		if owner, ok := s.deletedClips[clip]; ok && owner != id {
			return ErrConflict
		}
	}
	for _, clip := range clips {
		s.deletedClips[clip] = id
	}
	a.Status = publisher.Deleted
	a.DeletionRequested = false
	s.publishers[id] = a
	if d.CompletedAt.IsZero() {
		d.CompletedAt = now.UTC()
		s.deletions[id] = d
	}
	return nil
}
func (s *MemoryStore) DeletedClips(_ context.Context, ids []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]bool{}
	for _, id := range ids {
		_, result[id] = s.deletedClips[id]
	}
	return result, nil
}

func (s *FirestoreStore) deletionDoc(id publisher.ID) *firestore.DocumentRef {
	return s.client.Collection("publisher_deletions").Doc(string(id))
}
func (s *FirestoreStore) operatorPublisherTx(tx *firestore.Transaction, operator string, id publisher.ID) (publisher.Account, error) {
	if !validUsername(operator) {
		return publisher.Account{}, ErrForbidden
	}
	if !id.Valid() {
		return publisher.Account{}, ErrNotFound
	}
	doc, err := tx.Get(s.client.Collection("accounts").Doc(operator))
	if err != nil {
		return publisher.Account{}, storeError(err)
	}
	var u Account
	if err := doc.DataTo(&u); err != nil {
		return publisher.Account{}, err
	}
	if !u.User.Admin || u.User.ID != operator {
		return publisher.Account{}, ErrForbidden
	}
	doc, err = tx.Get(s.publisherDoc(id))
	if err != nil {
		return publisher.Account{}, storeError(err)
	}
	var a publisher.Account
	if err := doc.DataTo(&a); err != nil {
		return a, err
	}
	if a.ID != id || a.Validate() != nil {
		return a, fmt.Errorf("invalid publisher record")
	}
	return a, nil
}
func (s *FirestoreStore) RequestPublisherDeletion(ctx context.Context, m StaffManager, id publisher.ID, requested bool) (publisher.Account, error) {
	if m.OperatorID != "" {
		return publisher.Account{}, ErrForbidden
	}
	var a publisher.Account
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if err := s.staffManagementTx(tx, id, m); err != nil {
			return err
		}
		doc, err := tx.Get(s.publisherDoc(id))
		if err != nil {
			return storeError(err)
		}
		if err := doc.DataTo(&a); err != nil {
			return err
		}
		doc, err = tx.Get(s.staffDoc(id, m.Principal.StaffAccountID))
		if err != nil {
			return storeError(err)
		}
		var row StaffRecord
		if err := doc.DataTo(&row); err != nil {
			return err
		}
		if !a.Allows(m.Principal, row.Staff, publisher.RequestDeletion) {
			return ErrForbidden
		}
		a.DeletionRequested = requested
		return tx.Update(s.publisherDoc(id), []firestore.Update{{Path: "deletion_requested", Value: requested}})
	})
	return a, storeError(err)
}
func (s *FirestoreStore) BeginPublisherDeletion(ctx context.Context, operator string, id publisher.ID, now time.Time) (PublisherDeletion, error) {
	rid, err := deletionID()
	if err != nil {
		return PublisherDeletion{}, err
	}
	var result PublisherDeletion
	err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		a, err := s.operatorPublisherTx(tx, operator, id)
		if err != nil {
			return err
		}
		doc, err := tx.Get(s.deletionDoc(id))
		if err == nil {
			if err := doc.DataTo(&result); err != nil {
				return err
			}
			if !validDeletion(result, id) || (a.Status != publisher.Deleting && a.Status != publisher.Deleted) {
				return ErrConflict
			}
			return nil
		}
		if status.Code(err) != codes.NotFound {
			return err
		}
		if a.Status != publisher.Active && a.Status != publisher.Suspended {
			return ErrConflict
		}
		result = PublisherDeletion{Account: id, RequestID: rid, StartedBy: operator, StartedAt: now.UTC()}
		if err := tx.Create(s.deletionDoc(id), result); err != nil {
			return err
		}
		return tx.Update(s.publisherDoc(id), []firestore.Update{{Path: "status", Value: publisher.Deleting}})
	})
	return result, storeError(err)
}
func (s *FirestoreStore) PublisherDeletion(ctx context.Context, operator string, id publisher.ID) (*PublisherDeletion, error) {
	var result *PublisherDeletion
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if _, err := s.operatorPublisherTx(tx, operator, id); err != nil {
			return err
		}
		doc, err := tx.Get(s.deletionDoc(id))
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		var d PublisherDeletion
		if err := doc.DataTo(&d); err != nil {
			return err
		}
		if !validDeletion(d, id) {
			return ErrConflict
		}
		result = &d
		return nil
	})
	return result, storeError(err)
}
func (s *FirestoreStore) FinishPublisherDeletion(ctx context.Context, operator string, id publisher.ID, rid string, clips []string, now time.Time) error {
	if !validDeletedClips(clips) {
		return ErrConflict
	}
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		a, err := s.operatorPublisherTx(tx, operator, id)
		if err != nil {
			return err
		}
		doc, err := tx.Get(s.deletionDoc(id))
		if err != nil {
			return storeError(err)
		}
		var d PublisherDeletion
		if err := doc.DataTo(&d); err != nil {
			return err
		}
		if !validDeletion(d, id) || d.RequestID != rid || (a.Status != publisher.Deleting && a.Status != publisher.Deleted) {
			return ErrConflict
		}
		if a.Status == publisher.Deleted {
			return nil
		}
		for _, clip := range clips {
			doc, err := tx.Get(s.client.Collection("deleted_clips").Doc(clip))
			if err == nil {
				owner, err := doc.DataAt("publisher_account_id")
				if err != nil || owner != string(id) {
					return ErrConflict
				}
			} else if status.Code(err) != codes.NotFound {
				return err
			}
		}
		for _, clip := range clips {
			if err := tx.Set(s.client.Collection("deleted_clips").Doc(clip), map[string]any{"publisher_account_id": string(id), "deleted_at": now.UTC()}); err != nil {
				return err
			}
		}
		if err := tx.Update(s.publisherDoc(id), []firestore.Update{{Path: "status", Value: publisher.Deleted}, {Path: "deletion_requested", Value: false}}); err != nil {
			return err
		}
		if d.CompletedAt.IsZero() {
			return tx.Update(s.deletionDoc(id), []firestore.Update{{Path: "completed_at", Value: now.UTC()}})
		}
		return nil
	})
	return storeError(err)
}
func (s *FirestoreStore) DeletedClips(ctx context.Context, ids []string) (map[string]bool, error) {
	if len(ids) > 20 {
		return nil, fmt.Errorf("too many deletion references")
	}
	refs := make([]*firestore.DocumentRef, 0, len(ids))
	for _, id := range ids {
		if deletionClipID.MatchString(id) {
			refs = append(refs, s.client.Collection("deleted_clips").Doc(id))
		}
	}
	result := map[string]bool{}
	if len(refs) == 0 {
		return result, nil
	}
	docs, err := s.client.GetAll(ctx, refs)
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		if doc.Exists() {
			result[doc.Ref.ID] = true
		}
	}
	return result, nil
}
