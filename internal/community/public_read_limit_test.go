package community

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type publicReadCalls struct {
	clips, sessions, counts, liked, comments, cheers int64
}

type publicReadStore struct {
	*MemoryStore
	clips, sessions, counts, liked, comments, cheers atomic.Int64
}

func (s *publicReadStore) snapshot() publicReadCalls {
	return publicReadCalls{s.clips.Load(), s.sessions.Load(), s.counts.Load(), s.liked.Load(), s.comments.Load(), s.cheers.Load()}
}

func (s *publicReadStore) Session(ctx context.Context, key string) (Session, error) {
	s.sessions.Add(1)
	return s.MemoryStore.Session(ctx, key)
}

func (s *publicReadStore) Counts(ctx context.Context, clip string) (Counts, error) {
	s.counts.Add(1)
	return s.MemoryStore.Counts(ctx, clip)
}

func (s *publicReadStore) Liked(ctx context.Context, clip, user string) (bool, error) {
	s.liked.Add(1)
	return s.MemoryStore.Liked(ctx, clip, user)
}

func (s *publicReadStore) Comments(ctx context.Context, clip string, after *CommentCursor, limit int) ([]Comment, error) {
	s.comments.Add(1)
	return s.MemoryStore.Comments(ctx, clip, after, limit)
}

func (s *publicReadStore) Cheers(ctx context.Context, clip string) (Cheers, error) {
	s.cheers.Add(1)
	return s.MemoryStore.Cheers(ctx, clip)
}

func newPublicReadHandler(t *testing.T) (*Handler, *publicReadStore, *http.Cookie) {
	t.Helper()
	store := &publicReadStore{MemoryStore: NewMemoryStore()}
	h := NewHandler(Config{
		Store: store, ClipIDs: map[string]bool{"known": true},
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)),
		ClipExists: func(_ context.Context, id string) (bool, error) {
			store.clips.Add(1)
			return id == "dynamic", nil
		},
	})
	if h.readRate.Limit() != 10 || h.readRate.Burst() != 20 {
		t.Fatalf("unexpected default public read budget: %v/s burst %d", h.readRate.Limit(), h.readRate.Burst())
	}
	// Freeze refill so request scheduling and race instrumentation cannot make
	// admission assertions depend on wall-clock speed. Keep the default burst.
	h.readRate.SetLimit(0)
	cookie := &http.Cookie{Name: cookieName, Value: base64.RawURLEncoding.EncodeToString(make([]byte, 32))}
	if err := store.PutSession(context.Background(), sessionKey(cookie.Value), Session{
		User: User{ID: "reader", Name: "Reader"}, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	return h, store, cookie
}

func assertPublicReadLimited(t *testing.T, response *http.Response, body []byte, method string) {
	t.Helper()
	wantBody := "{\"error\":\"rate_limited\"}\n"
	if method == http.MethodHead {
		wantBody = ""
	}
	if response.StatusCode != http.StatusTooManyRequests || string(body) != wantBody ||
		response.Header.Get("Retry-After") != "1" || response.Header.Get("Cache-Control") != "no-store" ||
		response.Header.Get("Content-Type") != "application/json; charset=utf-8" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("%s rejection: status=%d body=%q headers=%v", method, response.StatusCode, body, response.Header)
	}
}

func TestPublicReadLimitSharesBudgetBeforeAllDependencies(t *testing.T) {
	h, store, cookie := newPublicReadHandler(t)
	routes := []struct{ method, clip, endpoint string }{
		{"GET", "known", "engagement"}, {"HEAD", "dynamic", "engagement"},
		{"GET", "known", "comments"}, {"HEAD", "dynamic", "comments"},
		{"GET", "known", "cheers"}, {"HEAD", "dynamic", "cheers"},
	}
	for i := range 20 {
		route := routes[i%len(routes)]
		response, body := call(t, h, route.method, "/api/v1/clips/"+route.clip+"/"+route.endpoint, "", cookie, nil)
		if response.StatusCode != 200 || (route.method == "HEAD" && len(body) != 0) {
			t.Fatalf("admission %d: status=%d body=%q", i, response.StatusCode, body)
		}
	}
	want := publicReadCalls{clips: 10, sessions: 8, counts: 8, liked: 8, comments: 6, cheers: 6}
	if got := store.snapshot(); got != want {
		t.Fatalf("admitted dependency calls: got %+v, want %+v", got, want)
	}
	for i, route := range routes {
		for j, clip := range []string{"known", "dynamic", fmt.Sprintf("unknown-%d", i)} {
			for _, cookieKind := range []string{"absent", "malformed", "valid", "random-valid"} {
				t.Run(fmt.Sprintf("%s/%s/%s/%s", route.method, route.endpoint, clip, cookieKind), func(t *testing.T) {
					r := httptest.NewRequest(route.method, "https://rangle.test/api/v1/clips/"+clip+"/"+route.endpoint+"?cursor=invalid", nil)
					r.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", i*3+j+1)
					r.Header.Set("X-Forwarded-For", fmt.Sprintf("198.51.100.%d", i*3+j+1))
					switch cookieKind {
					case "malformed":
						r.AddCookie(&http.Cookie{Name: cookieName, Value: "invalid"})
					case "valid":
						r.AddCookie(cookie)
					case "random-valid":
						token := make([]byte, 32)
						token[0] = byte(i + 1)
						r.AddCookie(&http.Cookie{Name: cookieName, Value: base64.RawURLEncoding.EncodeToString(token)})
					}
					recorder := httptest.NewRecorder()
					h.ServeHTTP(recorder, r)
					response := recorder.Result()
					defer response.Body.Close()
					assertPublicReadLimited(t, response, recorder.Body.Bytes(), route.method)
				})
			}
		}
	}
	if got := store.snapshot(); got != want {
		t.Fatalf("rejected requests reached dependencies: got %+v, want %+v", got, want)
	}
}

func TestPublicReadLimitIncludesUnknownClipIDs(t *testing.T) {
	h, store, cookie := newPublicReadHandler(t)
	for i := range 20 {
		method := http.MethodGet
		if i%2 == 1 {
			method = http.MethodHead
		}
		response, _ := call(t, h, method, fmt.Sprintf("/api/v1/clips/unknown-%d/engagement", i), "", cookie, nil)
		if response.StatusCode != 404 {
			t.Fatalf("unknown clip %d: status=%d", i, response.StatusCode)
		}
	}
	response, body := call(t, h, "GET", "/api/v1/clips/known/engagement", "", cookie, nil)
	assertPublicReadLimited(t, response, body, "GET")
	if got := store.snapshot(); got != (publicReadCalls{clips: 20}) {
		t.Fatalf("unexpected dependency calls: %+v", got)
	}
}

func TestPublicReadLimitConcurrentRequests(t *testing.T) {
	h, store, cookie := newPublicReadHandler(t)
	const requests = 96
	var admitted, rejected atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range requests {
		wg.Go(func() {
			<-start
			method := http.MethodGet
			if i%2 == 1 {
				method = http.MethodHead
			}
			endpoint := []string{"engagement", "comments", "cheers"}[i%3]
			response, body := call(t, h, method, "/api/v1/clips/dynamic/"+endpoint, "", cookie, nil)
			switch response.StatusCode {
			case 200:
				admitted.Add(1)
				if method == http.MethodHead && len(body) != 0 {
					t.Errorf("HEAD success returned body: %q", body)
				}
			case 429:
				rejected.Add(1)
				assertPublicReadLimited(t, response, body, method)
			default:
				t.Errorf("unexpected concurrent status: %d %s", response.StatusCode, body)
			}
		})
	}
	close(start)
	wg.Wait()
	if admitted.Load() != 20 || rejected.Load() != requests-20 {
		t.Fatalf("concurrent admission: admitted=%d rejected=%d", admitted.Load(), rejected.Load())
	}
	got := store.snapshot()
	if got.clips != 20 || got.counts+got.comments+got.cheers != 20 || got.sessions != got.counts || got.liked != got.counts {
		t.Fatalf("concurrent dependency calls exceeded admitted reads: %+v", got)
	}
}

func TestPublicReadLimitPreservesSuccessfulDataAndFreshness(t *testing.T) {
	h, store, cookie := newPublicReadHandler(t)
	ctx := context.Background()
	if err := store.SetLike(ctx, "known", "reader", true); err != nil {
		t.Fatal(err)
	}
	comment := Comment{ID: strings.Repeat("a", 32), Author: User{ID: "reader", Name: "Reader"}, Text: "hello", CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	if _, err := store.AddComment(ctx, "known", comment); err != nil {
		t.Fatal(err)
	}
	cheer := CheerBatch{ID: "first"}
	cheer.Bins[4] = 3
	if err := store.RecordCheers(ctx, "known", cheer); err != nil {
		t.Fatal(err)
	}
	cheersJSON, _ := json.Marshal(Cheers{Bins: cheer.Bins})
	for endpoint, expected := range map[string]string{
		"engagement": "{\"comments\":1,\"liked\":true,\"likes\":1}\n",
		"comments":   "{\"comments\":[{\"id\":\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",\"author\":{\"id\":\"reader\",\"name\":\"Reader\"},\"text\":\"hello\",\"created_at\":\"2026-01-01T00:00:00Z\"}],\"next_cursor\":\"\"}\n",
		"cheers":     string(cheersJSON) + "\n",
	} {
		response, body := call(t, h, "GET", "/api/v1/clips/known/"+endpoint, "", cookie, nil)
		if response.StatusCode != 200 || string(body) != expected || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("Retry-After") != "" {
			t.Fatalf("%s success changed: status=%d body=%s headers=%v", endpoint, response.StatusCode, body, response.Header)
		}
		head, body := call(t, h, "HEAD", "/api/v1/clips/known/"+endpoint, "", cookie, nil)
		if head.StatusCode != 200 || len(body) != 0 || head.Header.Get("Content-Type") != response.Header.Get("Content-Type") || head.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s HEAD changed: status=%d body=%s", endpoint, head.StatusCode, body)
		}
	}
	if got := store.snapshot(); got != (publicReadCalls{sessions: 2, counts: 2, liked: 2, comments: 2, cheers: 2}) {
		t.Fatalf("admitted GET/HEAD dependency calls: %+v", got)
	}
	if err := store.SetLike(ctx, "known", "reader", false); err != nil {
		t.Fatal(err)
	}
	response, body := call(t, h, "GET", "/api/v1/clips/known/engagement", "", cookie, nil)
	if response.StatusCode != 200 || string(body) != "{\"comments\":1,\"liked\":false,\"likes\":0}\n" {
		t.Fatalf("admitted read returned stale state: %d %s", response.StatusCode, body)
	}
}

func TestPublicReadLimitKeepsAuthWritesAndHealthIndependent(t *testing.T) {
	h, _, _ := newPublicReadHandler(t)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", h)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	cookie := registerUser(t, h, "writer")
	for phase := range 2 {
		response, body := call(t, mux, "GET", "/healthz", "", nil, nil)
		if response.StatusCode != 200 || string(body) != "ok\n" {
			t.Fatalf("health changed: %d %s", response.StatusCode, body)
		}
		response, body = call(t, h, "GET", "/api/v1/auth/me", "", cookie, nil)
		if response.StatusCode != 200 || !strings.Contains(string(body), `"id":"writer"`) {
			t.Fatalf("auth/me changed: %d %s", response.StatusCode, body)
		}
		response, body = call(t, h, "POST", "/api/v1/auth/login", `{"username":"writer","password":"a long test password 2026"}`, nil, nil)
		if response.StatusCode != 200 {
			t.Fatalf("login changed: %d %s", response.StatusCode, body)
		}
		response, body = call(t, h, "PUT", "/api/v1/clips/known/likes", "", cookie, nil)
		if response.StatusCode != 204 {
			t.Fatalf("like changed: %d %s", response.StatusCode, body)
		}
		response, body = call(t, h, "POST", "/api/v1/clips/known/comments", `{"text":"hello","request_id":"0123456789abcdef0123456789abcdef"}`, cookie, nil)
		var comment Comment
		if response.StatusCode != 201 || json.Unmarshal(body, &comment) != nil || comment.Text != "hello" {
			t.Fatalf("comment write changed: %d %s", response.StatusCode, body)
		}
		response, body = call(t, h, "DELETE", "/api/v1/clips/known/comments/"+comment.ID, "", cookie, nil)
		if response.StatusCode != 204 {
			t.Fatalf("comment deletion changed: %d %s", response.StatusCode, body)
		}
		bins := make([]int64, CheerBinCount)
		bins[4] = 1
		cheerBody, _ := json.Marshal(map[string]any{"request_id": "0123456789abcdef0123456789abcdef", "bins": bins})
		response, body = call(t, h, "POST", "/api/v1/clips/known/cheers", string(cheerBody), cookie, nil)
		var cheers Cheers
		if response.StatusCode != 200 || json.Unmarshal(body, &cheers) != nil || cheers.Bins[4] != 1 {
			t.Fatalf("cheer write/retry changed: %d %s", response.StatusCode, body)
		}
		response, _ = call(t, h, "POST", "/api/v1/clips/known/comments", `{"text":"hello","request_id":"0123456789abcdef0123456789abcdef"}`, cookie, map[string]string{"Origin": "https://other.example"})
		if response.StatusCode != 403 {
			t.Fatalf("write CSRF protection changed: %d", response.StatusCode)
		}
		response, _ = call(t, h, "PUT", "/api/v1/clips/known/likes", "", nil, nil)
		if response.StatusCode != 401 {
			t.Fatalf("anonymous write changed: %d", response.StatusCode)
		}
		// The first pass proves unrelated traffic does not consume this budget;
		// the second proves exhausting it does not reject auth, writes or health.
		if phase == 0 {
			for i := range 20 {
				response, body = call(t, h, "GET", "/api/v1/clips/known/engagement", "", nil, nil)
				if response.StatusCode != 200 {
					t.Fatalf("unrelated traffic consumed public read %d: %d %s", i, response.StatusCode, body)
				}
			}
		}
		response, body = call(t, h, "HEAD", "/api/v1/clips/known/comments", "", nil, nil)
		assertPublicReadLimited(t, response, body, "HEAD")
	}
}
