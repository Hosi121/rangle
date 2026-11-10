package community

import (
	"context"
	"crypto/rand"
	"fmt"
	"math"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
)

type StaffPassword struct {
	Salt        []byte `json:"-"`
	Hash        []byte `json:"-"`
	HashVersion int    `json:"-"`
}

type StaffPasswordStore interface {
	PublisherStore
	ResetStaffPassword(context.Context, StaffManager, publisher.ID, publisher.StaffID, StaffPassword) (StaffRecord, error)
	ChangeStaffPassword(context.Context, publisher.StaffPrincipal, int64, StaffPassword) (StaffRecord, error)
}

func newStaffPassword(password string) (StaffPassword, error) {
	if !validPassword(password) {
		return StaffPassword{}, fmt.Errorf("invalid staff password")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return StaffPassword{}, err
	}
	return StaffPassword{salt, passwordKey(password, salt), 1}, nil
}

func applyStaffPassword(row StaffRecord, password StaffPassword) (StaffRecord, error) {
	if !row.valid() || len(password.Salt) != 16 || len(password.Hash) != 32 || password.HashVersion != 1 {
		return row, fmt.Errorf("invalid staff password record")
	}
	if row.Version == math.MaxInt64 {
		return row, ErrConflict
	}
	row.Salt = append([]byte(nil), password.Salt...)
	row.Hash = append([]byte(nil), password.Hash...)
	row.HashVersion = password.HashVersion
	row.Version++
	return row, nil
}

func canChangeStaffPassword(a publisher.Account, row StaffRecord, actor publisher.StaffPrincipal, version int64) bool {
	return version > 0 && row.Version == version && a.Allows(actor, row.Staff, publisher.ReadGeneration)
}

func (s *MemoryStore) ResetStaffPassword(_ context.Context, m StaffManager, account publisher.ID, id publisher.StaffID, password StaffPassword) (StaffRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.staffManagement(account, m); err != nil {
		return StaffRecord{}, err
	}
	row, ok := s.staff[account][id]
	if !ok {
		return row, ErrNotFound
	}
	row, err := applyStaffPassword(row, password)
	if err != nil {
		return row, err
	}
	s.staff[account][id] = row.clone()
	return row.clone(), nil
}

func (s *MemoryStore) ChangeStaffPassword(_ context.Context, actor publisher.StaffPrincipal, version int64, password StaffPassword) (StaffRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.publishers[actor.PublisherAccountID]
	row, found := s.staff[actor.PublisherAccountID][actor.StaffAccountID]
	if !ok || !found || !canChangeStaffPassword(a, row, actor, version) {
		return StaffRecord{}, ErrForbidden
	}
	row, err := applyStaffPassword(row, password)
	if err != nil {
		return row, err
	}
	s.staff[a.ID][row.Staff.ID] = row.clone()
	return row.clone(), nil
}

func (s *FirestoreStore) ResetStaffPassword(ctx context.Context, m StaffManager, account publisher.ID, id publisher.StaffID, password StaffPassword) (StaffRecord, error) {
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
			return fmt.Errorf("invalid staff record")
		}
		result, err = applyStaffPassword(row, password)
		if err != nil {
			return err
		}
		return tx.Set(ref, result)
	})
	return result, storeError(err)
}

func (s *FirestoreStore) ChangeStaffPassword(ctx context.Context, actor publisher.StaffPrincipal, version int64, password StaffPassword) (StaffRecord, error) {
	if !actor.PublisherAccountID.Valid() || !actor.StaffAccountID.Valid() {
		return StaffRecord{}, ErrForbidden
	}
	var result StaffRecord
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		aDoc, err := tx.Get(s.publisherDoc(actor.PublisherAccountID))
		if err != nil {
			return storeError(err)
		}
		ref := s.staffDoc(actor.PublisherAccountID, actor.StaffAccountID)
		doc, err := tx.Get(ref)
		if err != nil {
			return storeError(err)
		}
		var a publisher.Account
		var row StaffRecord
		if err := aDoc.DataTo(&a); err != nil {
			return err
		}
		if err := doc.DataTo(&row); err != nil {
			return err
		}
		if !canChangeStaffPassword(a, row, actor, version) {
			return ErrForbidden
		}
		result, err = applyStaffPassword(row, password)
		if err != nil {
			return err
		}
		return tx.Set(ref, result)
	})
	return result, storeError(err)
}
