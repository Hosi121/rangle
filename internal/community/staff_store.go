package community

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StaffRecord and StaffSession are private storage records. Sessions carry a
// credential generation, never a cached role; updates can invalidate old logins.
type StaffRecord struct {
	Staff       publisher.StaffAccount `json:"-" firestore:"staff"`
	Salt        []byte                 `json:"-" firestore:"salt"`
	Hash        []byte                 `json:"-" firestore:"hash"`
	HashVersion int                    `json:"-" firestore:"hash_version"`
	Version     int64                  `json:"-" firestore:"version"`
}
type StaffSession struct {
	Principal publisher.StaffPrincipal `firestore:"principal"`
	Version   int64                    `firestore:"version"`
	ExpiresAt time.Time                `firestore:"expires_at"`
}
type PublisherStore interface {
	ProvisionPublisher(context.Context, publisher.Account, StaffRecord) error
	Publisher(context.Context, publisher.ID) (publisher.Account, error)
	Staff(context.Context, publisher.ID, publisher.StaffID) (StaffRecord, error)
	StaffByUsername(context.Context, publisher.ID, string) (StaffRecord, error)
	PutStaffSession(context.Context, string, StaffSession) error
	StaffSession(context.Context, string) (StaffSession, error)
	DeleteStaffSession(context.Context, string) error
}

func (s StaffRecord) valid() bool {
	return s.Staff.Validate() == nil && len(s.Salt) == 16 && len(s.Hash) == 32 && s.HashVersion == 1 && s.Version > 0
}
func (s StaffRecord) clone() StaffRecord {
	s.Salt = append([]byte(nil), s.Salt...)
	s.Hash = append([]byte(nil), s.Hash...)
	return s
}
func validProvision(a publisher.Account, staff StaffRecord) bool {
	return a.Validate() == nil && a.Status == publisher.Active && staff.valid() && staff.Staff.PublisherAccountID == a.ID && staff.Staff.Role == publisher.Administrator && !staff.Staff.Disabled
}

func (s *MemoryStore) ProvisionPublisher(_ context.Context, a publisher.Account, staff StaffRecord) error {
	if !validProvision(a, staff) {
		return fmt.Errorf("invalid publisher provisioning")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.publishers[a.ID]; ok {
		return ErrConflict
	}
	if s.publishers == nil {
		s.publishers = map[publisher.ID]publisher.Account{}
		s.staff = map[publisher.ID]map[publisher.StaffID]StaffRecord{}
		s.staffNames = map[publisher.ID]map[string]publisher.StaffID{}
	}
	s.publishers[a.ID] = a
	s.staff[a.ID] = map[publisher.StaffID]StaffRecord{staff.Staff.ID: staff.clone()}
	s.staffNames[a.ID] = map[string]publisher.StaffID{staff.Staff.Username: staff.Staff.ID}
	return nil
}
func (s *MemoryStore) Publisher(_ context.Context, id publisher.ID) (publisher.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.publishers[id]
	if !ok {
		return a, ErrNotFound
	}
	return a, nil
}
func (s *MemoryStore) Staff(_ context.Context, account publisher.ID, id publisher.StaffID) (StaffRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.staff[account][id]
	if !ok {
		return row, ErrNotFound
	}
	return row.clone(), nil
}
func (s *MemoryStore) StaffByUsername(_ context.Context, account publisher.ID, name string) (StaffRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.staffNames[account][name]
	if !ok {
		return StaffRecord{}, ErrNotFound
	}
	return s.staff[account][id].clone(), nil
}
func (s *MemoryStore) PutStaffSession(_ context.Context, key string, row StaffSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.staffSessions == nil {
		s.staffSessions = map[string]StaffSession{}
	}
	if _, ok := s.staffSessions[key]; ok {
		return ErrConflict
	}
	s.staffSessions[key] = row
	return nil
}
func (s *MemoryStore) StaffSession(_ context.Context, key string) (StaffSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row, ok := s.staffSessions[key]
	if !ok {
		return row, ErrNotFound
	}
	return row, nil
}
func (s *MemoryStore) DeleteStaffSession(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.staffSessions, key)
	return nil
}

func (s *FirestoreStore) publisherDoc(id publisher.ID) *firestore.DocumentRef {
	return s.client.Collection("publisher_accounts").Doc(string(id))
}
func (s *FirestoreStore) staffDoc(account publisher.ID, id publisher.StaffID) *firestore.DocumentRef {
	return s.publisherDoc(account).Collection("staff_accounts").Doc(string(id))
}
func (s *FirestoreStore) ProvisionPublisher(ctx context.Context, a publisher.Account, staff StaffRecord) error {
	if !validProvision(a, staff) {
		return fmt.Errorf("invalid publisher provisioning")
	}
	parent := s.publisherDoc(a.ID)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		_, err := tx.Get(parent)
		if err == nil {
			return ErrConflict
		}
		if status.Code(err) != codes.NotFound {
			return err
		}
		if err := tx.Create(parent, a); err != nil {
			return err
		}
		if err := tx.Create(s.staffDoc(a.ID, staff.Staff.ID), staff); err != nil {
			return err
		}
		return tx.Create(parent.Collection("staff_names").Doc(staff.Staff.Username), map[string]string{"staff_id": string(staff.Staff.ID)})
	})
}
func (s *FirestoreStore) Publisher(ctx context.Context, id publisher.ID) (publisher.Account, error) {
	if !id.Valid() {
		return publisher.Account{}, ErrNotFound
	}
	doc, err := s.publisherDoc(id).Get(ctx)
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
func (s *FirestoreStore) Staff(ctx context.Context, account publisher.ID, id publisher.StaffID) (StaffRecord, error) {
	if !account.Valid() || !id.Valid() {
		return StaffRecord{}, ErrNotFound
	}
	doc, err := s.staffDoc(account, id).Get(ctx)
	if err != nil {
		return StaffRecord{}, storeError(err)
	}
	var row StaffRecord
	if err := doc.DataTo(&row); err != nil {
		return row, err
	}
	if !row.valid() || row.Staff.ID != id || row.Staff.PublisherAccountID != account {
		return row, fmt.Errorf("invalid staff record")
	}
	return row, nil
}
func (s *FirestoreStore) StaffByUsername(ctx context.Context, account publisher.ID, name string) (StaffRecord, error) {
	if !account.Valid() || !validUsername(name) {
		return StaffRecord{}, ErrNotFound
	}
	doc, err := s.publisherDoc(account).Collection("staff_names").Doc(name).Get(ctx)
	if err != nil {
		return StaffRecord{}, storeError(err)
	}
	var index struct {
		ID string `firestore:"staff_id"`
	}
	if err := doc.DataTo(&index); err != nil {
		return StaffRecord{}, err
	}
	row, err := s.Staff(ctx, account, publisher.StaffID(index.ID))
	if err != nil {
		return row, err
	}
	if row.Staff.Username != name {
		return row, fmt.Errorf("invalid staff name binding")
	}
	return row, nil
}
func (s *FirestoreStore) PutStaffSession(ctx context.Context, key string, row StaffSession) error {
	_, err := s.client.Collection("staff_sessions").Doc(key).Create(ctx, row)
	return storeError(err)
}
func (s *FirestoreStore) StaffSession(ctx context.Context, key string) (StaffSession, error) {
	doc, err := s.client.Collection("staff_sessions").Doc(key).Get(ctx)
	if err != nil {
		return StaffSession{}, storeError(err)
	}
	var row StaffSession
	err = doc.DataTo(&row)
	return row, err
}
func (s *FirestoreStore) DeleteStaffSession(ctx context.Context, key string) error {
	_, err := s.client.Collection("staff_sessions").Doc(key).Delete(ctx)
	return err
}
