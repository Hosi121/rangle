package community

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
	"google.golang.org/api/iterator"
)

func (s *MemoryStore) Subscribed(_ context.Context, user string, id publisher.ID) (bool, error) {
	if !validUsername(user) || !id.Valid() {
		return false, ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.publishers[id]
	if !ok || a.ID != id || a.Status != publisher.Active || a.Validate() != nil {
		return false, ErrNotFound
	}
	return s.follows[user][id], nil
}
func (s *MemoryStore) SetSubscription(_ context.Context, user string, id publisher.ID, value bool) error {
	if !validUsername(user) || !id.Valid() {
		return ErrNotFound
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[user]; !ok {
		return ErrNotFound
	}
	if value {
		if a, ok := s.publishers[id]; !ok || a.Status != publisher.Active {
			return ErrNotFound
		}
		if s.follows == nil {
			s.follows = map[string]map[publisher.ID]bool{}
		}
		if s.follows[user] == nil {
			s.follows[user] = map[publisher.ID]bool{}
		}
		s.follows[user][id] = true
	} else {
		delete(s.follows[user], id)
	}
	return nil
}
func (s *MemoryStore) Subscriptions(_ context.Context, user string, after publisher.ID, limit int) ([]publisher.Account, error) {
	if !validUsername(user) || limit < 1 || limit > 21 || after != "" && !after.Valid() {
		return nil, fmt.Errorf("invalid subscription pagination")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ids := make([]publisher.ID, 0)
	for id := range s.follows[user] {
		if id > after {
			ids = append(ids, id)
		}
	}
	slices.SortFunc(ids, func(a, b publisher.ID) int { return strings.Compare(string(a), string(b)) })
	if len(ids) > limit {
		ids = ids[:limit]
	}
	rows := make([]publisher.Account, 0, len(ids))
	for _, id := range ids {
		a, ok := s.publishers[id]
		if !ok {
			a = publisher.Account{ID: id, Status: publisher.Deleted}
		}
		rows = append(rows, a)
	}
	return rows, nil
}

func (s *FirestoreStore) subscriptionCollection(user string) *firestore.CollectionRef {
	return s.client.Collection("accounts").Doc(user).Collection("publisher_subscriptions")
}
func (s *FirestoreStore) Subscribed(ctx context.Context, user string, id publisher.ID) (bool, error) {
	if !validUsername(user) || !id.Valid() {
		return false, ErrNotFound
	}
	a, err := s.Publisher(ctx, id)
	if err != nil {
		return false, err
	}
	if a.ID != id || a.Status != publisher.Active || a.Validate() != nil {
		return false, ErrNotFound
	}
	_, err = s.subscriptionCollection(user).Doc(string(id)).Get(ctx)
	if storeError(err) == ErrNotFound {
		return false, nil
	}
	return err == nil, err
}
func (s *FirestoreStore) SetSubscription(ctx context.Context, user string, id publisher.ID, value bool) error {
	if !validUsername(user) || !id.Valid() {
		return ErrNotFound
	}
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(s.client.Collection("accounts").Doc(user))
		if err != nil {
			return storeError(err)
		}
		var account Account
		if err := doc.DataTo(&account); err != nil {
			return err
		}
		if account.User.ID != user {
			return ErrForbidden
		}
		ref := s.subscriptionCollection(user).Doc(string(id))
		if !value {
			return tx.Delete(ref)
		}
		// Recheck lifecycle in the same transaction as the follow write. Deletion
		// and suspension cannot race a stale profile into a new active relation.
		doc, err = tx.Get(s.publisherDoc(id))
		if err != nil {
			return storeError(err)
		}
		var a publisher.Account
		if err := doc.DataTo(&a); err != nil {
			return err
		}
		if a.ID != id || a.Status != publisher.Active || a.Validate() != nil {
			return ErrNotFound
		}
		return tx.Set(ref, map[string]string{"publisher_account_id": string(id)})
	})
}
func (s *FirestoreStore) Subscriptions(ctx context.Context, user string, after publisher.ID, limit int) ([]publisher.Account, error) {
	if !validUsername(user) || limit < 1 || limit > 21 || after != "" && !after.Valid() {
		return nil, fmt.Errorf("invalid subscription pagination")
	}
	collection := s.subscriptionCollection(user)
	q := collection.OrderBy(firestore.DocumentID, firestore.Asc).Limit(limit)
	if after != "" {
		q = q.StartAfter(collection.Doc(string(after)))
	}
	it := q.Documents(ctx)
	defer it.Stop()
	refs := make([]*firestore.DocumentRef, 0, limit)
	for {
		doc, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		id := publisher.ID(doc.Ref.ID)
		stored, err := doc.DataAt("publisher_account_id")
		if !id.Valid() || err != nil || stored != string(id) {
			return nil, fmt.Errorf("invalid subscription record")
		}
		refs = append(refs, s.publisherDoc(id))
	}
	rows := make([]publisher.Account, 0, len(refs))
	if len(refs) == 0 {
		return rows, nil
	}
	// One bounded batch refreshes names/lifecycle; no per-row session or query.
	docs, err := s.client.GetAll(ctx, refs)
	if err != nil {
		return nil, err
	}
	for i, doc := range docs {
		a := publisher.Account{ID: publisher.ID(refs[i].ID), Status: publisher.Deleted}
		if doc.Exists() {
			if err := doc.DataTo(&a); err != nil {
				return nil, err
			}
			if a.ID != publisher.ID(refs[i].ID) || a.Validate() != nil {
				return nil, fmt.Errorf("invalid publisher record")
			}
		}
		rows = append(rows, a)
	}
	return rows, nil
}
