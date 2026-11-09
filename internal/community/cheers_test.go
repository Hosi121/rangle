package community

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestCheerRetryIsAtomicAndClipScoped(t *testing.T) {
	s := NewMemoryStore()
	batch := CheerBatch{ID: "receipt"}
	batch.Bins[12] = 3
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.RecordCheers(context.Background(), "a", batch); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	total, _ := s.Cheers(context.Background(), "a")
	if total.Bins[12] != 3 {
		t.Fatalf("retry counted twice: %v", total)
	}
	changed := batch
	changed.Bins[12]++
	if err := s.RecordCheers(context.Background(), "a", changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutated receipt: %v", err)
	}
	if err := s.RecordCheers(context.Background(), "b", batch); err != nil {
		t.Fatal(err)
	}
	other, _ := s.Cheers(context.Background(), "b")
	if other.Bins[12] != 3 {
		t.Fatal(other)
	}
}

func TestCheerAPIAuthenticationValidationAndIdempotence(t *testing.T) {
	h, _ := testHandler()
	cookie := registerUser(t, h, "cheerer")
	body := func(id string, bins []int64) string {
		b, _ := json.Marshal(map[string]any{"request_id": id, "bins": bins})
		return string(b)
	}
	bins := make([]int64, CheerBinCount)
	bins[4] = 2
	bins[30] = 3
	id := "1234567890abcdef1234567890abcdef"
	valid := body(id, bins)
	r, _ := call(t, h, "POST", "/api/v1/clips/basketball/cheers", valid, nil, nil)
	if r.StatusCode != 401 {
		t.Fatalf("anonymous write: %d", r.StatusCode)
	}
	r, _ = call(t, h, "POST", "/api/v1/clips/basketball/cheers", valid, cookie, map[string]string{"Origin": "https://other.test"})
	if r.StatusCode != 403 {
		t.Fatalf("cross origin write: %d", r.StatusCode)
	}
	for range 2 {
		r, data := call(t, h, "POST", "/api/v1/clips/basketball/cheers", valid, cookie, nil)
		var got Cheers
		_ = json.Unmarshal(data, &got)
		if r.StatusCode != 200 || got.Bins[4] != 2 || got.Bins[30] != 3 {
			t.Fatalf("retry: %d %s", r.StatusCode, data)
		}
	}
	bins[4]++
	r, _ = call(t, h, "POST", "/api/v1/clips/basketball/cheers", body(id, bins), cookie, nil)
	if r.StatusCode != 409 {
		t.Fatalf("conflicting retry: %d", r.StatusCode)
	}
	for _, bad := range [][]int64{{1}, make([]int64, CheerBinCount)} {
		r, _ = call(t, h, "POST", "/api/v1/clips/basketball/cheers", body(id, bad), cookie, nil)
		if r.StatusCode != 400 {
			t.Fatalf("invalid bins: %d", r.StatusCode)
		}
	}
	bins[4] = -1
	r, _ = call(t, h, "POST", "/api/v1/clips/basketball/cheers", body(id, bins), cookie, nil)
	if r.StatusCode != 400 {
		t.Fatalf("negative bins: %d", r.StatusCode)
	}
	r, data := call(t, h, "GET", "/api/v1/clips/basketball/cheers", "", nil, nil)
	var got Cheers
	_ = json.Unmarshal(data, &got)
	if r.StatusCode != 200 || got.Bins[4] != 2 {
		t.Fatalf("public aggregate: %d %s", r.StatusCode, data)
	}
	r, _ = call(t, h, "GET", "/api/v1/clips/missing/cheers", "", nil, nil)
	if r.StatusCode != 404 {
		t.Fatalf("unknown clip: %d", r.StatusCode)
	}
}
