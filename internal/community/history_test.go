package community

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"
)

func historyContract(t *testing.T, cheer CheerStore, history CheerHistoryStore) {
	t.Helper()
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Microsecond)
	b := CheerBatch{ID: "one", UserID: "viewer", Clip: HistoryClip{ID: "a", Title: "Initial", Sport: "basketball"}, CreatedAt: at}
	b.Bins[2] = 3
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := cheer.RecordCheers(ctx, "a", b); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	newBatch := b
	newBatch.ID = "two"
	newBatch.CreatedAt = at.Add(time.Second)
	newBatch.Clip.Title = "Latest"
	if err := cheer.RecordCheers(ctx, "a", newBatch); err != nil {
		t.Fatal(err)
	}
	retry := b
	retry.CreatedAt = at.Add(time.Hour)
	if err := cheer.RecordCheers(ctx, "a", retry); err != nil {
		t.Fatal(err)
	}
	late := b
	late.ID = "three"
	late.CreatedAt = at.Add(-time.Hour)
	if err := cheer.RecordCheers(ctx, "a", late); err != nil {
		t.Fatal(err)
	}
	otherClip := newBatch
	otherClip.ID = "four"
	otherClip.Clip.ID = "b"
	if err := cheer.RecordCheers(ctx, "b", otherClip); err != nil {
		t.Fatal(err)
	}
	rows, err := history.CheeredClips(ctx, "viewer", nil, 1)
	if err != nil || len(rows) != 1 || rows[0].Clip.ID != "b" {
		t.Fatalf("deterministic ties: %+v %v", rows, err)
	}
	cursor := &CheerHistoryCursor{User: "viewer", Clip: "b", At: otherClip.CreatedAt}
	rows, err = history.CheeredClips(ctx, "viewer", cursor, 20)
	if err != nil || len(rows) != 1 || rows[0].Clip.Title != "Latest" || !rows[0].LastCheeredAt.Equal(newBatch.CreatedAt) {
		t.Fatalf("retry or late arrival changed history: %+v %v", rows, err)
	}
	if rows, err := history.CheeredClips(ctx, "another", nil, 20); err != nil || len(rows) != 0 {
		t.Fatal("cross-user history leak", rows, err)
	}
	total, err := cheer.Cheers(ctx, "a")
	if err != nil || total.Bins[2] != 9 {
		t.Fatal("retry changed aggregate", total, err)
	}
	// Old receipts have no reversible identity; retrying them cannot invent history.
	legacy := CheerBatch{ID: "legacy"}
	legacy.Bins[1] = 1
	if err := cheer.RecordCheers(ctx, "legacy", legacy); err != nil {
		t.Fatal(err)
	}
	legacy.UserID = "oldviewer"
	legacy.Clip = HistoryClip{ID: "legacy"}
	legacy.CreatedAt = at
	if err := cheer.RecordCheers(ctx, "legacy", legacy); err != nil {
		t.Fatal(err)
	}
	if rows, err := history.CheeredClips(ctx, "oldviewer", nil, 20); err != nil || len(rows) != 0 {
		t.Fatal("invented legacy history")
	}
}
func TestMemoryCheerHistoryContract(t *testing.T) { s := NewMemoryStore(); historyContract(t, s, s) }
func TestFirestoreCheerHistoryContract(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local Firestore emulator")
	}
	project := fmt.Sprintf("demo-history-%x", time.Now().UnixNano())
	s, err := NewFirestoreStore(context.Background(), project, "community")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	historyContract(t, s, s)
	second, err := NewFirestoreStore(context.Background(), project, "community")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	rows, err := second.CheeredClips(context.Background(), "viewer", nil, 20)
	if err != nil || len(rows) != 2 {
		t.Fatal("history did not survive a new client", rows, err)
	}
}
func TestHistoryAPIIsPrivatePagedAndRouted(t *testing.T) {
	h, s := testHandler()
	owner := registerUser(t, h, "owner")
	other := registerUser(t, h, "other")
	at := time.Now().UTC().Truncate(time.Microsecond)
	for i := range 21 {
		id := fmt.Sprintf("clip-%02d", i)
		if err := s.RecordCheers(context.Background(), id, CheerBatch{ID: id, UserID: "owner", Clip: HistoryClip{ID: id, Title: "Highlight", Sport: "basketball"}, CreatedAt: at}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/me/cheered-clips"
	r, _ := call(t, h, "GET", path, "", nil, nil)
	if r.StatusCode != 401 {
		t.Fatal("anonymous history", r.StatusCode)
	}
	r, data := call(t, h, "GET", path, "", owner, nil)
	var page struct {
		Clips      []CheeredClip `json:"clips"`
		NextCursor string        `json:"next_cursor"`
	}
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" || json.Unmarshal(data, &page) != nil || len(page.Clips) != 20 || page.NextCursor == "" {
		t.Fatalf("history page: %d %s", r.StatusCode, data)
	}
	cursor := page.NextCursor
	r, data = call(t, h, "GET", path+"?cursor="+url.QueryEscape(cursor), "", owner, nil)
	if r.StatusCode != 200 || json.Unmarshal(data, &page) != nil || len(page.Clips) != 1 || page.NextCursor != "" {
		t.Fatalf("history continuation: %d %s", r.StatusCode, data)
	}
	for _, q := range []string{"?user=owner", "?cursor=bad", "?cursor=x&cursor=y"} {
		r, _ := call(t, h, "GET", path+q, "", owner, nil)
		if r.StatusCode != 400 {
			t.Fatal(q, r.StatusCode)
		}
	}
	r, _ = call(t, h, "GET", path+"?cursor="+url.QueryEscape(cursor), "", other, nil)
	if r.StatusCode != 400 {
		t.Fatal("cross-account cursor", r.StatusCode)
	}
	r, data = call(t, h, "GET", path, "", other, nil)
	if r.StatusCode != 200 || json.Unmarshal(data, &page) != nil || len(page.Clips) != 0 {
		t.Fatal("another account inherited history")
	}
	r, data = call(t, h, "HEAD", path, "", owner, nil)
	if r.StatusCode != 200 || len(data) != 0 {
		t.Fatal("HEAD body")
	}
}

type failedHistoryStore struct{ *MemoryStore }

func (s failedHistoryStore) CheeredClips(context.Context, string, *CheerHistoryCursor, int) ([]CheeredClip, error) {
	return nil, errors.New("offline")
}
func TestHistoryDoesNotHideStorageFailure(t *testing.T) {
	s := failedHistoryStore{NewMemoryStore()}
	h := NewHandler(Config{Store: s})
	cookie := registerUser(t, h, "viewer")
	r, data := call(t, h, "GET", "/api/v1/me/cheered-clips", "", cookie, nil)
	if r.StatusCode != 503 || string(data) == `{"clips":[]}` {
		t.Fatal("history failure presented as empty", r.StatusCode, string(data))
	}
}

func TestCheerResolvesEligibilityAndLabelsOnce(t *testing.T) {
	store := NewMemoryStore()
	reads := 0
	h := NewHandler(Config{Store: store,
		DescribeClip: func(context.Context, string) (HistoryClip, error) {
			reads++
			return HistoryClip{ID: "basketball", Title: "Reviewed highlight", Sport: "basketball"}, nil
		},
		ClipExists: func(context.Context, string) (bool, error) {
			t.Fatal("duplicate eligibility lookup")
			return false, nil
		},
	})
	cookie := registerUser(t, h, "viewer")
	bins := make([]int64, CheerBinCount)
	bins[3] = 1
	payload, _ := json.Marshal(map[string]any{"request_id": "1234567890abcdef1234567890abcdef", "bins": bins})
	response, body := call(t, h, "POST", "/api/v1/clips/basketball/cheers", string(payload), cookie, nil)
	if response.StatusCode != 200 || reads != 1 {
		t.Fatal("lookup not shared", response.StatusCode, reads, string(body))
	}
	rows, err := store.CheeredClips(context.Background(), "viewer", nil, 20)
	if err != nil || len(rows) != 1 || rows[0].Clip.Title != "Reviewed highlight" {
		t.Fatal("missing labels", rows, err)
	}
}
