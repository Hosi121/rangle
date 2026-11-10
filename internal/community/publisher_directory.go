package community

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
	"google.golang.org/api/iterator"
)

type PublisherDirectoryStore interface {
	ListPublishers(context.Context, string, publisher.ID, int) ([]publisher.Account, error)
}

func (s *MemoryStore) ListPublishers(_ context.Context, operator string, after publisher.ID, limit int) ([]publisher.Account, error) {
	if limit < 1 || limit > 21 || after != "" && !after.Valid() {
		return nil, fmt.Errorf("invalid publisher pagination")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.accounts[operator]
	if !ok || !u.User.Admin || u.User.ID != operator {
		return nil, ErrForbidden
	}
	rows := make([]publisher.Account, 0)
	for _, a := range s.publishers {
		if a.ID > after {
			rows = append(rows, a)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

func (s *FirestoreStore) ListPublishers(ctx context.Context, operator string, after publisher.ID, limit int) ([]publisher.Account, error) {
	if !validUsername(operator) {
		return nil, ErrForbidden
	}
	if limit < 1 || limit > 21 || after != "" && !after.Valid() {
		return nil, fmt.Errorf("invalid publisher pagination")
	}
	var rows []publisher.Account
	err := s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(s.client.Collection("accounts").Doc(operator))
		if err != nil {
			return storeError(err)
		}
		var account Account
		if err := doc.DataTo(&account); err != nil {
			return err
		}
		if !account.User.Admin || account.User.ID != operator {
			return ErrForbidden
		}
		collection := s.client.Collection("publisher_accounts")
		q := collection.OrderBy(firestore.DocumentID, firestore.Asc).Limit(limit)
		if after != "" {
			q = q.StartAfter(collection.Doc(string(after)))
		}
		iter := tx.Documents(q)
		defer iter.Stop()
		page := make([]publisher.Account, 0)
		for {
			doc, err := iter.Next()
			if errors.Is(err, iterator.Done) {
				break
			}
			if err != nil {
				return err
			}
			var a publisher.Account
			if err := doc.DataTo(&a); err != nil {
				return err
			}
			if a.Validate() != nil || string(a.ID) != doc.Ref.ID || a.Status == "" {
				return fmt.Errorf("invalid publisher record")
			}
			page = append(page, a)
		}
		rows = page
		return nil
	})
	return rows, storeError(err)
}

func (h *Handler) listPublishers(w http.ResponseWriter, r *http.Request) {
	u, err := h.AdminUser(r)
	if errors.Is(err, ErrForbidden) {
		h.fail(w, r, 403, "operator_required")
		return
	}
	if err != nil {
		h.unavailable(w, r, "publisher_directory_auth", err)
		return
	}
	if u == nil {
		h.fail(w, r, 401, "not_authenticated")
		return
	}
	store, ok := h.cfg.Store.(PublisherDirectoryStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	cursor, err := urlQuery(r)
	after := publisher.ID(cursor)
	if err != nil || after != "" && !after.Valid() {
		h.fail(w, r, 400, "invalid_cursor")
		return
	}
	if !h.allow("publisher-directory:"+u.ID, publicReadInterval, publicReadBurst) {
		w.Header().Set("Retry-After", "1")
		h.fail(w, r, 429, "rate_limited")
		return
	}
	rows, err := store.ListPublishers(r.Context(), u.ID, after, 21)
	if err != nil {
		h.staffManagementError(w, r, "publisher_directory", err)
		return
	}
	next := ""
	if len(rows) > 20 {
		rows = rows[:20]
		next = string(rows[19].ID)
	}
	type view struct {
		Account           publisher.Account `json:"account"`
		Status            publisher.Status  `json:"status"`
		DeletionRequested bool              `json:"deletion_requested"`
	}
	views := make([]view, 0, len(rows))
	for _, a := range rows {
		views = append(views, view{a, a.Status, a.DeletionRequested})
	}
	h.write(w, r, 200, map[string]any{"publishers": views, "next_cursor": next})
}
