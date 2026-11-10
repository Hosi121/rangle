package community

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StaffManager is constructed from a verified session, never request JSON.
// The store refreshes the actor inside the same transaction as each mutation.
type StaffManager struct {
	Principal  publisher.StaffPrincipal
	Version    int64
	OperatorID string
}
type StaffManagementStore interface {
	PublisherStore
	ListStaff(context.Context, StaffManager, publisher.ID, publisher.StaffID, int) ([]publisher.StaffAccount, error)
	CreateStaff(context.Context, StaffManager, publisher.ID, StaffRecord) error
	UpdateStaff(context.Context, StaffManager, publisher.ID, publisher.StaffID, publisher.Role, bool) (StaffRecord, error)
}

func (m StaffManager) authorize(a publisher.Account, operator func(string) (Account, error), staff func(publisher.ID, publisher.StaffID) (StaffRecord, error)) error {
	if m.OperatorID != "" {
		if m.Principal != (publisher.StaffPrincipal{}) || m.Version != 0 {
			return ErrForbidden
		}
		row, err := operator(m.OperatorID)
		if errors.Is(err, ErrNotFound) {
			return ErrForbidden
		}
		if err != nil {
			return err
		}
		if row.User.ID == m.OperatorID && row.User.Admin && (a.Status == publisher.Active || a.Status == publisher.Suspended) {
			return nil
		}
		return ErrForbidden
	}
	if m.Version < 1 || m.Principal.PublisherAccountID != a.ID || !m.Principal.StaffAccountID.Valid() {
		return ErrForbidden
	}
	row, err := staff(a.ID, m.Principal.StaffAccountID)
	if errors.Is(err, ErrNotFound) {
		return ErrForbidden
	}
	if err != nil {
		return err
	}
	if row.Version == m.Version && a.Allows(m.Principal, row.Staff, publisher.ManageStaff) {
		return nil
	}
	return ErrForbidden
}
func (s *MemoryStore) staffManagement(account publisher.ID, m StaffManager) (publisher.Account, error) {
	a, ok := s.publishers[account]
	if !ok {
		return a, ErrNotFound
	}
	err := m.authorize(a, func(id string) (Account, error) {
		a, ok := s.accounts[id]
		if !ok {
			return a, ErrNotFound
		}
		return a, nil
	}, func(account publisher.ID, id publisher.StaffID) (StaffRecord, error) {
		row, ok := s.staff[account][id]
		if !ok {
			return row, ErrNotFound
		}
		return row, nil
	})
	return a, err
}
func (s *MemoryStore) ListStaff(_ context.Context, m StaffManager, account publisher.ID, after publisher.StaffID, limit int) ([]publisher.StaffAccount, error) {
	if limit < 1 || limit > 21 {
		return nil, fmt.Errorf("invalid staff page size")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.staffManagement(account, m); err != nil {
		return nil, err
	}
	rows := make([]publisher.StaffAccount, 0)
	for id, row := range s.staff[account] {
		if after == "" || id > after {
			rows = append(rows, row.Staff)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
func (s *MemoryStore) CreateStaff(_ context.Context, m StaffManager, account publisher.ID, row StaffRecord) error {
	if !row.valid() || row.Version != 1 || row.Staff.Disabled || row.Staff.PublisherAccountID != account {
		return fmt.Errorf("invalid staff creation")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.staffManagement(account, m); err != nil {
		return err
	}
	if _, ok := s.staffNames[account][row.Staff.Username]; ok {
		return ErrConflict
	}
	if _, ok := s.staff[account][row.Staff.ID]; ok {
		return ErrConflict
	}
	if s.staff[account] == nil {
		s.staff[account] = map[publisher.StaffID]StaffRecord{}
	}
	if s.staffNames[account] == nil {
		s.staffNames[account] = map[string]publisher.StaffID{}
	}
	s.staff[account][row.Staff.ID] = row.clone()
	s.staffNames[account][row.Staff.Username] = row.Staff.ID
	return nil
}
func updatedStaff(row StaffRecord, role publisher.Role, disabled bool) (StaffRecord, error) {
	if !row.valid() {
		return row, fmt.Errorf("invalid staff record")
	}
	if row.Staff.Role == role && row.Staff.Disabled == disabled {
		return row, nil
	}
	if row.Version == math.MaxInt64 {
		return row, ErrConflict
	}
	row.Staff.Role, row.Staff.Disabled = role, disabled
	row.Version++
	if row.Staff.Validate() != nil {
		return row, fmt.Errorf("invalid staff update")
	}
	return row, nil
}
func (s *MemoryStore) UpdateStaff(_ context.Context, m StaffManager, account publisher.ID, id publisher.StaffID, role publisher.Role, disabled bool) (StaffRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.staffManagement(account, m); err != nil {
		return StaffRecord{}, err
	}
	row, ok := s.staff[account][id]
	if !ok {
		return row, ErrNotFound
	}
	row, err := updatedStaff(row, role, disabled)
	if err != nil {
		return row, err
	}
	s.staff[account][id] = row.clone()
	return row.clone(), nil
}
func (s *FirestoreStore) staffManagementTx(tx *firestore.Transaction, account publisher.ID, m StaffManager) error {
	if !account.Valid() {
		return ErrNotFound
	}
	doc, err := tx.Get(s.publisherDoc(account))
	if err != nil {
		return storeError(err)
	}
	var a publisher.Account
	if err := doc.DataTo(&a); err != nil {
		return err
	}
	if a.ID != account || a.Validate() != nil {
		return fmt.Errorf("invalid publisher record")
	}
	return m.authorize(a, func(id string) (Account, error) {
		if !validUsername(id) {
			return Account{}, ErrForbidden
		}
		doc, err := tx.Get(s.client.Collection("accounts").Doc(id))
		if err != nil {
			return Account{}, storeError(err)
		}
		var row Account
		err = doc.DataTo(&row)
		return row, err
	}, func(account publisher.ID, id publisher.StaffID) (StaffRecord, error) {
		doc, err := tx.Get(s.staffDoc(account, id))
		if err != nil {
			return StaffRecord{}, storeError(err)
		}
		var row StaffRecord
		if err := doc.DataTo(&row); err != nil {
			return row, err
		}
		if row.Staff.ID != id || row.Staff.PublisherAccountID != account || !row.valid() {
			return row, fmt.Errorf("invalid staff record")
		}
		return row, nil
	})
}
func (s *FirestoreStore) ListStaff(ctx context.Context, m StaffManager, account publisher.ID, after publisher.StaffID, limit int) ([]publisher.StaffAccount, error) {
	if limit < 1 || limit > 21 || after != "" && !after.Valid() {
		return nil, fmt.Errorf("invalid staff pagination")
	}
	var result []publisher.StaffAccount
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if err := s.staffManagementTx(tx, account, m); err != nil {
			return err
		}
		collection := s.publisherDoc(account).Collection("staff_accounts")
		q := collection.Select("staff").OrderBy(firestore.DocumentID, firestore.Asc).Limit(limit)
		if after != "" {
			q = q.StartAfter(collection.Doc(string(after)))
		}
		iter := tx.Documents(q)
		defer iter.Stop()
		rows := make([]publisher.StaffAccount, 0)
		for {
			doc, err := iter.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				return err
			}
			var row StaffRecord
			if err := doc.DataTo(&row); err != nil {
				return err
			}
			if row.Staff.Validate() != nil || row.Staff.PublisherAccountID != account || string(row.Staff.ID) != doc.Ref.ID {
				return fmt.Errorf("invalid staff list record")
			}
			rows = append(rows, row.Staff)
		}
		result = rows
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (s *FirestoreStore) CreateStaff(ctx context.Context, m StaffManager, account publisher.ID, row StaffRecord) error {
	if !account.Valid() || !row.valid() || row.Version != 1 || row.Staff.Disabled || row.Staff.PublisherAccountID != account {
		return fmt.Errorf("invalid staff creation")
	}
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if err := s.staffManagementTx(tx, account, m); err != nil {
			return err
		}
		name := s.publisherDoc(account).Collection("staff_names").Doc(row.Staff.Username)
		staff := s.staffDoc(account, row.Staff.ID)
		for _, ref := range []*firestore.DocumentRef{name, staff} {
			_, err := tx.Get(ref)
			if err == nil {
				return ErrConflict
			}
			if status.Code(err) != codes.NotFound {
				return err
			}
		}
		if err := tx.Create(staff, row); err != nil {
			return err
		}
		return tx.Create(name, map[string]string{"staff_id": string(row.Staff.ID)})
	})
	return storeError(err)
}
func (s *FirestoreStore) UpdateStaff(ctx context.Context, m StaffManager, account publisher.ID, id publisher.StaffID, role publisher.Role, disabled bool) (StaffRecord, error) {
	if !account.Valid() || !id.Valid() {
		return StaffRecord{}, ErrNotFound
	}
	var result StaffRecord
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		if err := s.staffManagementTx(tx, account, m); err != nil {
			return err
		}
		ref := s.staffDoc(account, id)
		doc, err := tx.Get(ref)
		if err != nil {
			return storeError(err)
		}
		var row StaffRecord
		if err := doc.DataTo(&row); err != nil {
			return err
		}
		if row.Staff.ID != id || row.Staff.PublisherAccountID != account {
			return fmt.Errorf("invalid staff binding")
		}
		next, err := updatedStaff(row, role, disabled)
		if err != nil {
			return err
		}
		result = next
		if next.Version == row.Version {
			return nil
		}
		return tx.Set(ref, next)
	})
	return result, storeError(err)
}
