package community

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
)

// Labels are snapshots taken when a cheer is accepted, so rendering a page
// never fans out to the catalog or generation manager for each history row.
type HistoryClip struct {
	ID    string `json:"id" firestore:"id"`
	Title string `json:"title" firestore:"title"`
	Sport string `json:"sport" firestore:"sport"`
}
type CheeredClip struct {
	Deleted       bool        `json:"deleted,omitempty" firestore:"-"`
	Clip          HistoryClip `json:"clip" firestore:"clip"`
	LastCheeredAt time.Time   `json:"last_cheered_at" firestore:"last_cheered_at"`
}
type CheerHistoryCursor struct {
	User string    `json:"u"`
	Clip string    `json:"c"`
	At   time.Time `json:"t"`
}
type CheerHistoryStore interface {
	CheeredClips(context.Context, string, *CheerHistoryCursor, int) ([]CheeredClip, error)
}

var historyClipID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func (s *MemoryStore) CheeredClips(_ context.Context, user string, after *CheerHistoryCursor, limit int) ([]CheeredClip, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]CheeredClip, 0)
	for id, row := range s.history[user] {
		if after == nil || row.LastCheeredAt.Before(after.At) || row.LastCheeredAt.Equal(after.At) && id < after.Clip {
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].LastCheeredAt.Equal(rows[j].LastCheeredAt) {
			return rows[i].Clip.ID > rows[j].Clip.ID
		}
		return rows[i].LastCheeredAt.After(rows[j].LastCheeredAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
func (s *FirestoreStore) historyCollection(user string) *firestore.CollectionRef {
	return s.client.Collection("accounts").Doc(user).Collection("cheered_clips")
}
func (s *FirestoreStore) CheeredClips(ctx context.Context, user string, after *CheerHistoryCursor, limit int) ([]CheeredClip, error) {
	collection := s.historyCollection(user)
	query := collection.OrderBy("last_cheered_at", firestore.Desc).OrderBy(firestore.DocumentID, firestore.Desc).Limit(limit)
	if after != nil {
		query = query.StartAfter(after.At, collection.Doc(after.Clip))
	}
	iter := query.Documents(ctx)
	defer iter.Stop()
	rows := make([]CheeredClip, 0)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		var row CheeredClip
		if err := doc.DataTo(&row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
}
func (h *Handler) cheerHistory(w http.ResponseWriter, r *http.Request) {
	user, err := h.SessionUser(r)
	if err != nil {
		h.unavailable(w, r, "history_session", err)
		return
	}
	if user == nil {
		h.fail(w, r, 401, "not_authenticated")
		return
	}
	if !h.allow("history:"+user.ID, time.Second, 10) {
		w.Header().Set("Retry-After", "1")
		h.fail(w, r, 429, "rate_limited")
		return
	}
	store, ok := h.cfg.Store.(CheerHistoryStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	value, err := urlQuery(r)
	if err != nil {
		h.fail(w, r, 400, "invalid_cursor")
		return
	}
	var after *CheerHistoryCursor
	if value != "" {
		data, err := base64.RawURLEncoding.DecodeString(value)
		var c CheerHistoryCursor
		if err != nil || json.Unmarshal(data, &c) != nil || c.User != user.ID || !historyClipID.MatchString(c.Clip) || c.At.IsZero() {
			h.fail(w, r, 400, "invalid_cursor")
			return
		}
		after = &c
	}
	rows, err := store.CheeredClips(r.Context(), user.ID, after, 21)
	if err != nil {
		h.unavailable(w, r, "cheer_history", err)
		return
	}
	cursor := ""
	if len(rows) > 20 {
		rows = rows[:20]
		last := rows[19]
		data, _ := json.Marshal(CheerHistoryCursor{user.ID, last.Clip.ID, last.LastCheeredAt})
		cursor = base64.RawURLEncoding.EncodeToString(data)
	}
	if rows == nil {
		rows = []CheeredClip{}
	}
	if tombstones, ok := h.cfg.Store.(interface {
		DeletedClips(context.Context, []string) (map[string]bool, error)
	}); ok && len(rows) > 0 {
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = row.Clip.ID
		}
		deleted, err := tombstones.DeletedClips(r.Context(), ids)
		if err != nil {
			h.unavailable(w, r, "history_deleted_clips", err)
			return
		}
		for i := range rows {
			rows[i].Deleted = deleted[rows[i].Clip.ID]
		}
	}
	h.write(w, r, 200, map[string]any{"clips": rows, "next_cursor": cursor})
}
