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
		if old.Bins != batch.Bins {
			return ErrConflict
		}
		return nil
	}
	s.cheers[clip][batch.ID] = batch
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
			if previous.Bins != batch.Bins {
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
		for i, n := range batch.Bins {
			counts.Bins[i] += n
		}
		if err := tx.Create(receipt, batch); err != nil {
			return err
		}
		return tx.Set(shard, counts)
	})
}

func (h *Handler) cheers(w http.ResponseWriter, r *http.Request) {
	clip := h.clip(w, r)
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
		batch := CheerBatch{ID: hex.EncodeToString(sum[:16]), ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour)}
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
