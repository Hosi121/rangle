package community

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const CheerBinCount = 48

// Bins are fractions of a clip's duration, not wall-clock timestamps.
type Cheers struct {
	Bins [CheerBinCount]int64 `json:"bins" firestore:"bins"`
}

type CheerBatch struct {
	ID        string               `firestore:"id"`
	UserID    string               `firestore:"user_id,omitempty"`
	Clip      HistoryClip          `firestore:"clip"`
	CreatedAt time.Time            `firestore:"created_at"`
	Bins      [CheerBinCount]int64 `firestore:"bins"`
	ExpiresAt time.Time            `firestore:"expires_at"`
}

type CheerStore interface {
	Cheers(context.Context, string) (Cheers, error)
	RecordCheers(context.Context, string, CheerBatch) error
}

func (s *MemoryStore) Cheers(_ context.Context, clip string) (Cheers, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total Cheers
	for _, b := range s.cheers[clip] {
		for i, n := range b.Bins {
			total.Bins[i] += n
		}
	}
	return total, nil
}

func (s *MemoryStore) RecordCheers(_ context.Context, clip string, batch CheerBatch) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cheers == nil {
		s.cheers = map[string]map[string]CheerBatch{}
	}
	if s.cheers[clip] == nil {
		s.cheers[clip] = map[string]CheerBatch{}
	}
	if old, ok := s.cheers[clip][batch.ID]; ok {
		if old.Bins != batch.Bins || old.UserID != "" && old.UserID != batch.UserID {
			return ErrConflict
		}
		return nil
	}
	s.cheers[clip][batch.ID] = batch
	if batch.UserID != "" {
		if s.history == nil {
			s.history = map[string]map[string]CheeredClip{}
		}
		if s.history[batch.UserID] == nil {
			s.history[batch.UserID] = map[string]CheeredClip{}
		}
		if old, ok := s.history[batch.UserID][clip]; !ok || batch.CreatedAt.After(old.LastCheeredAt) {
			s.history[batch.UserID][clip] = CheeredClip{Clip: batch.Clip, LastCheeredAt: batch.CreatedAt}
		}
	}
	return nil
}

func (s *FirestoreStore) Cheers(ctx context.Context, clip string) (Cheers, error) {
	var result Cheers
	iter := s.clip(clip).Collection("cheer_shards").Limit(32).Documents(ctx)
	defer iter.Stop()
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			return result, nil
		}
		if err != nil {
			return Cheers{}, err
		}
		var shard Cheers
		if err := doc.DataTo(&shard); err != nil {
			return Cheers{}, err
		}
		for i, n := range shard.Bins {
			result.Bins[i] += n
		}
	}
}

// Shard counters to avoid a single shared write document on a popular clip.
// An atomic receipt makes retries after a lost HTTP response idempotent.
func (s *FirestoreStore) RecordCheers(ctx context.Context, clip string, batch CheerBatch) error {
	receipt := s.clip(clip).Collection("cheer_receipts").Doc(batch.ID)
	sum := sha256.Sum256([]byte(batch.ID))
	shard := s.clip(clip).Collection("cheer_shards").Doc(fmt.Sprintf("%02d", sum[0]%32))
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		old, err := tx.Get(receipt)
		if err == nil {
			var previous CheerBatch
			if err := old.DataTo(&previous); err != nil {
				return err
			}
			if previous.Bins != batch.Bins || previous.UserID != "" && previous.UserID != batch.UserID {
				return ErrConflict
			}
			return nil
		}
		if status.Code(err) != codes.NotFound {
			return err
		}
		var counts Cheers
		previous, err := tx.Get(shard)
		if err == nil {
			if err := previous.DataTo(&counts); err != nil {
				return err
			}
		} else if status.Code(err) != codes.NotFound {
			return err
		}
		// Read before any writes; the receipt, aggregate and private history
		// commit together. An older in-flight batch cannot move history backwards.
		var history *firestore.DocumentRef
		updateHistory := false
		if batch.UserID != "" {
			history = s.historyCollection(batch.UserID).Doc(clip)
			old, err := tx.Get(history)
			if err == nil {
				var row CheeredClip
				if err := old.DataTo(&row); err != nil {
					return err
				}
				updateHistory = batch.CreatedAt.After(row.LastCheeredAt)
			} else if status.Code(err) == codes.NotFound {
				updateHistory = true
			} else {
				return err
			}
		}
		for i, n := range batch.Bins {
			counts.Bins[i] += n
		}
		if updateHistory {
			if err := tx.Set(history, CheeredClip{Clip: batch.Clip, LastCheeredAt: batch.CreatedAt}); err != nil {
				return err
			}
		}
		if err := tx.Create(receipt, batch); err != nil {
			return err
		}
		return tx.Set(shard, counts)
	})
}

func (h *Handler) cheers(w http.ResponseWriter, r *http.Request) {
	info := h.cheerClip(w, r)
	clip := info.ID
	if clip == "" {
		return
	}
	store, ok := h.cfg.Store.(CheerStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	if r.Method == http.MethodPost {
		user := h.requireUser(w, r)
		if user == nil {
			return
		}
		var input struct {
			RequestID string  `json:"request_id"`
			Bins      []int64 `json:"bins"`
		}
		if !h.decode(w, r, &input) {
			return
		}
		if !validHexID(input.RequestID) || len(input.Bins) != CheerBinCount {
			h.fail(w, r, 400, "invalid_cheer")
			return
		}
		var total int64
		for _, n := range input.Bins {
			if n < 0 || n > 64 {
				h.fail(w, r, 400, "invalid_cheer")
				return
			}
			total += n
		}
		if total < 1 || total > 64 {
			h.fail(w, r, 400, "invalid_cheer")
			return
		}
		sum := sha256.Sum256([]byte(user.ID + "\n" + input.RequestID))
		now := time.Now().UTC().Truncate(time.Microsecond)

		batch := CheerBatch{ID: hex.EncodeToString(sum[:16]), UserID: user.ID, Clip: info, CreatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
		copy(batch.Bins[:], input.Bins)
		if err := store.RecordCheers(r.Context(), clip, batch); err != nil {
			if errors.Is(err, ErrConflict) {
				h.fail(w, r, 409, "request_conflict")
			} else {
				h.unavailable(w, r, "cheer", err)
			}
			return
		}
	}
	value, err := store.Cheers(r.Context(), clip)
	if err != nil {
		h.unavailable(w, r, "cheers", err)
		return
	}
	h.write(w, r, 200, value)
}

// Resolve eligibility and labels together. In cloud deployments this avoids
// fetching the same generated job twice for each accepted batch.
func (h *Handler) cheerClip(w http.ResponseWriter, r *http.Request) HistoryClip {
	id := r.PathValue("clip")
	if h.cfg.DescribeClip == nil {
		if h.clip(w, r) == "" {
			return HistoryClip{}
		}
		return HistoryClip{ID: id, Title: id}
	}
	info, err := h.cfg.DescribeClip(r.Context(), id)
	if errors.Is(err, ErrNotFound) {
		h.fail(w, r, 404, "clip_not_found")
		return HistoryClip{}
	}
	if err != nil {
		h.unavailable(w, r, "cheer_clip", err)
		return HistoryClip{}
	}
	if info.ID != id || info.Title == "" {
		h.unavailable(w, r, "cheer_clip", fmt.Errorf("invalid clip labels"))
		return HistoryClip{}
	}
	return info
}
