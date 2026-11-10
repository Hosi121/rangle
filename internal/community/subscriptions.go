package community

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

// Subscriptions are personal viewers' free follows, not staff membership or
// publishing authority. Each relation has its own document, without a hot counter.
type SubscriptionStore interface {
	Publisher(context.Context, publisher.ID) (publisher.Account, error)
	Subscribed(context.Context, string, publisher.ID) (bool, error)
	SetSubscription(context.Context, string, publisher.ID, bool) error
	Subscriptions(context.Context, string, publisher.ID, int) ([]publisher.Account, error)
}

func (h *Handler) publicPublisher(w http.ResponseWriter, r *http.Request) {
	account := publisher.ID(r.PathValue("account"))
	if !account.Valid() || r.URL.RawQuery != "" {
		h.fail(w, r, 404, "publisher_not_found")
		return
	}
	store, ok := h.cfg.Store.(SubscriptionStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	a, err := store.Publisher(r.Context(), account)
	if errors.Is(err, ErrNotFound) || err == nil && a.Status != publisher.Active {
		h.fail(w, r, 404, "publisher_not_found")
		return
	}
	if err != nil || a.ID != account || a.Validate() != nil {
		h.unavailable(w, r, "public_publisher", err)
		return
	}
	// Account's public JSON contains only id/name/kind, never staff or lifecycle.
	h.write(w, r, 200, map[string]any{"publisher": a})
}

func (h *Handler) subscriptionUser(w http.ResponseWriter, r *http.Request) (*User, SubscriptionStore) {
	user, err := h.SessionUser(r)
	if err != nil {
		h.unavailable(w, r, "subscription_session", err)
		return nil, nil
	}
	if user == nil {
		h.fail(w, r, 401, "not_authenticated")
		return nil, nil
	}
	if !h.allow("subscription:"+user.ID, time.Second, 10) {
		w.Header().Set("Retry-After", "1")
		h.fail(w, r, 429, "rate_limited")
		return nil, nil
	}
	store, ok := h.cfg.Store.(SubscriptionStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return nil, nil
	}
	return user, store
}

func (h *Handler) publisherSubscription(w http.ResponseWriter, r *http.Request) {
	user, store := h.subscriptionUser(w, r)
	if user == nil {
		return
	}
	id := publisher.ID(r.PathValue("account"))
	if !id.Valid() || r.URL.RawQuery != "" || r.Body != nil && r.ContentLength != 0 {
		h.fail(w, r, 400, "invalid_subscription")
		return
	}
	var subscribed bool
	var err error
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		subscribed, err = store.Subscribed(r.Context(), user.ID, id)
	} else {
		subscribed = r.Method == http.MethodPut
		err = store.SetSubscription(r.Context(), user.ID, id, subscribed)
	}
	if errors.Is(err, ErrNotFound) {
		h.fail(w, r, 404, "publisher_not_found")
		return
	}
	if err != nil {
		h.unavailable(w, r, "publisher_subscription", err)
		return
	}
	h.write(w, r, 200, map[string]bool{"subscribed": subscribed})
}

func (h *Handler) publisherSubscriptions(w http.ResponseWriter, r *http.Request) {
	user, store := h.subscriptionUser(w, r)
	if user == nil {
		return
	}
	value, err := urlQuery(r)
	var cursor struct {
		User string       `json:"u"`
		ID   publisher.ID `json:"p"`
	}
	if value != "" {
		data, decodeErr := base64.RawURLEncoding.DecodeString(value)
		if decodeErr != nil || json.Unmarshal(data, &cursor) != nil || cursor.User != user.ID || !cursor.ID.Valid() {
			err = ErrNotFound
		}
	}
	if err != nil {
		h.fail(w, r, 400, "invalid_cursor")
		return
	}
	rows, err := store.Subscriptions(r.Context(), user.ID, cursor.ID, 21)
	if err != nil {
		h.unavailable(w, r, "publisher_subscriptions", err)
		return
	}
	next := ""
	if len(rows) > 20 {
		rows = rows[:20]
		cursor.User, cursor.ID = user.ID, rows[19].ID
		data, _ := json.Marshal(cursor)
		next = base64.RawURLEncoding.EncodeToString(data)
	}
	visible := make([]publisher.Account, 0, len(rows))
	for _, row := range rows {
		if row.Status == publisher.Active && row.Validate() == nil {
			visible = append(visible, row)
		}
	}
	h.write(w, r, 200, map[string]any{"publishers": visible, "next_cursor": next})
}
