package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/generation"
)

func cheerApplication(t *testing.T, store community.Store, service *generation.Service) *application {
	t.Helper()
	cfg, _, _ := publishedFixture(t)
	cfg.CommunityStore, cfg.GenerationService = store, service
	app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app
}

func cheerRequest(app http.Handler, method, path, body string, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "https://rangle.test"+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}

func cheerMember(t *testing.T, app http.Handler) *http.Cookie {
	t.Helper()
	w := cheerRequest(app, "POST", "/api/v1/auth/register", `{"username":"viewer","password":"a long test password 2026"}`, nil, nil)
	if w.Code != 201 || len(w.Result().Cookies()) != 1 {
		t.Fatalf("register member: %d %s", w.Code, w.Body.String())
	}
	return w.Result().Cookies()[0]
}

func cheerPayload(t *testing.T, bins []int64) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{"request_id": strings.Repeat("a", 32), "bins": bins})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func requireCheerTotals(t *testing.T, w *httptest.ResponseRecorder, want community.Cheers) {
	t.Helper()
	var got community.Cheers
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != 200 || got != want {
		t.Fatalf("cheer totals: status=%d body=%s error=%v want=%v", w.Code, w.Body.String(), err, want)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("ETag") != "" {
		t.Fatal("cheer totals became publicly cacheable", w.Header())
	}
}

func TestApplicationCheersReadWriteAndRetry(t *testing.T) {
	app := cheerApplication(t, community.NewMemoryStore(), nil)
	path := "/api/v1/clips/demo/cheers"
	requireCheerTotals(t, cheerRequest(app, "GET", path, "", nil, nil), community.Cheers{})
	for _, test := range []struct {
		method, path string
		status       int
	}{
		{"HEAD", path, 200}, {"GET", "/api/v1/clips/missing/cheers", 404},
		{"HEAD", "/api/v1/clips/missing/cheers", 404}, {"PUT", path, 405},
		{"POST", path, 401},
	} {
		w := cheerRequest(app, test.method, test.path, "", nil, nil)
		if w.Code != test.status || w.Header().Get("Cache-Control") != "no-store" || test.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatalf("%s %s: %d %s %v", test.method, test.path, w.Code, w.Body.String(), w.Header())
		}
		if test.status == 405 {
			for _, method := range []string{"GET", "HEAD", "POST"} {
				if !strings.Contains(w.Header().Get("Allow"), method) {
					t.Fatalf("missing allowed method %s: %v", method, w.Header())
				}
			}
		}
	}
	cookie := cheerMember(t, app)
	bins := make([]int64, community.CheerBinCount)
	bins[4], bins[30] = 2, 3
	body := cheerPayload(t, bins)
	if w := cheerRequest(app, "POST", path, body, cookie, map[string]string{"Origin": "https://other.test", "Sec-Fetch-Site": "cross-site"}); w.Code != 403 {
		t.Fatalf("cross-origin cheer accepted: %d %s", w.Code, w.Body.String())
	}
	var want community.Cheers
	copy(want.Bins[:], bins)
	for range 2 {
		requireCheerTotals(t, cheerRequest(app, "POST", path, body, cookie, nil), want)
	}
	bins[4]++
	if w := cheerRequest(app, "POST", path, cheerPayload(t, bins), cookie, nil); w.Code != 409 {
		t.Fatalf("changed retry accepted: %d %s", w.Code, w.Body.String())
	}
	bins[4] = 65
	if w := cheerRequest(app, "POST", path, cheerPayload(t, bins), cookie, nil); w.Code != 400 {
		t.Fatalf("oversized cheer accepted: %d %s", w.Code, w.Body.String())
	}
	bins[4], bins[30] = 64, 1
	if w := cheerRequest(app, "POST", path, cheerPayload(t, bins), cookie, nil); w.Code != 400 {
		t.Fatalf("cheer batch exceeded the total limit: %d %s", w.Code, w.Body.String())
	}
	requireCheerTotals(t, cheerRequest(app, "GET", path, "", nil, nil), want)
}

type cheerReadStore struct {
	*community.MemoryStore
	reads int
}

func (s *cheerReadStore) Cheers(ctx context.Context, clip string) (community.Cheers, error) {
	s.reads++
	return s.MemoryStore.Cheers(ctx, clip)
}

func TestApplicationCheersReadLimitPrecedesStorage(t *testing.T) {
	store := &cheerReadStore{MemoryStore: community.NewMemoryStore()}
	app := cheerApplication(t, store, nil)
	limited := 0
	// Allow clock-based refill without asserting an exact admitted count.
	for i := range 64 {
		method := http.MethodGet
		if i%2 != 0 {
			method = http.MethodHead
		}
		before := store.reads
		w := cheerRequest(app, method, "/api/v1/clips/demo/cheers", "", nil, nil)
		switch w.Code {
		case 200:
			if store.reads != before+1 {
				t.Fatal("successful cheer read bypassed storage")
			}
		case 429:
			limited++
			if store.reads != before || w.Header().Get("Retry-After") != "1" || w.Header().Get("Cache-Control") != "no-store" || method == "HEAD" && w.Body.Len() != 0 {
				t.Fatalf("read limit changed or reached storage: %d %s %v", w.Code, w.Body.String(), w.Header())
			}
		default:
			t.Fatalf("unexpected cheer read status %d: %s", w.Code, w.Body.String())
		}
	}
	if limited == 0 {
		t.Fatal("cheer reads bypassed the public read limiter")
	}
}

type cheerJobStore struct {
	generation.Store
	job generation.Job
	err error
}

func (s *cheerJobStore) Get(_ context.Context, id string) (generation.Job, error) {
	if s.err != nil {
		return generation.Job{}, s.err
	}
	if id != s.job.ID {
		return generation.Job{}, generation.ErrNotFound
	}
	return s.job, nil
}

func TestApplicationCheersRequireKnownCompletedGeneratedClip(t *testing.T) {
	id := strings.Repeat("b", 32)
	jobs := &cheerJobStore{job: generation.Job{ID: id, State: generation.Succeeded, Snapshot: generation.Preset{ID: "reviewed"}}}
	settings := generation.Config{
		Project: "comin-up", ProjectNumber: "113704194486", Database: "rangle-stg-generation",
		Image:          "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/trainer@sha256:" + strings.Repeat("c", 64),
		ServiceAccount: "rangle-stg-trainer@comin-up.iam.gserviceaccount.com", OutputBucket: "rangle-stg-media", MediaOrigin: "https://media.rangle.test",
		Deadline: time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC), Attempts: 10,
		Presets: []generation.Preset{{ID: "reviewed", Title: "Reviewed frame", Sport: "basketball", Description: "Reviewed sample", InputURI: "gs://inputs/datasets/sample.npz", InputSHA256: strings.Repeat("d", 64), PreviewURL: "https://media.rangle.test/preview.webm", Source: "https://source.rangle.test/sample", License: "CC BY 4.0"}},
	}
	// Clip eligibility must only read the known job, never submit or list jobs.
	service := &generation.Service{Config: settings, Store: jobs, Provider: struct{ generation.Provider }{}}
	app := cheerApplication(t, community.NewMemoryStore(), service)
	path := "/api/v1/clips/generated-" + id + "/cheers"
	cookie := cheerMember(t, app)
	bins := make([]int64, community.CheerBinCount)
	bins[2] = 64
	var want community.Cheers
	copy(want.Bins[:], bins)
	requireCheerTotals(t, cheerRequest(app, "POST", path, cheerPayload(t, bins), cookie, nil), want)
	requireCheerTotals(t, cheerRequest(app, "GET", path, "", nil, nil), want)
	for _, state := range []generation.State{generation.Running, generation.Failed, generation.Cancelled} {
		jobs.job.State = state
		if w := cheerRequest(app, "GET", path, "", nil, nil); w.Code != 404 {
			t.Fatalf("unfinished generated clip %s accepted: %d %s", state, w.Code, w.Body.String())
		}
	}
	jobs.job.State, jobs.job.Snapshot.Mode = generation.Succeeded, "live"
	if w := cheerRequest(app, "GET", path, "", nil, nil); w.Code != 404 {
		t.Fatalf("LIVE job accepted as a completed clip: %d %s", w.Code, w.Body.String())
	}
	if w := cheerRequest(app, "GET", "/api/v1/clips/generated-"+strings.Repeat("e", 32)+"/cheers", "", nil, nil); w.Code != 404 {
		t.Fatalf("unknown generated clip accepted: %d %s", w.Code, w.Body.String())
	}
	jobs.err = errors.New("generation storage unavailable")
	if w := cheerRequest(app, "GET", path, "", nil, nil); w.Code != 503 {
		t.Fatalf("generated clip lookup outage was hidden: %d %s", w.Code, w.Body.String())
	}
}

func TestApplicationCheersUnavailableWithoutCommunity(t *testing.T) {
	app := cheerApplication(t, nil, nil)
	w := cheerRequest(app, "GET", "/api/v1/clips/demo/cheers", "", nil, nil)
	if w.Code != 503 || !strings.Contains(w.Body.String(), "service_unavailable") {
		t.Fatalf("disabled community became a catalog lookup: %d %s", w.Code, w.Body.String())
	}
}

func TestApplicationCheerHistoryUsesCatalogLabels(t *testing.T) {
	app := cheerApplication(t, community.NewMemoryStore(), nil)
	cookie := cheerMember(t, app)
	bins := make([]int64, community.CheerBinCount)
	bins[8] = 1
	w := cheerRequest(app, "POST", "/api/v1/clips/demo/cheers", cheerPayload(t, bins), cookie, nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = cheerRequest(app, "GET", "/api/v1/me/cheered-clips", "", cookie, nil)
	result := w.Result()
	defer result.Body.Close()
	var page struct {
		Clips []community.CheeredClip `json:"clips"`
	}
	if result.StatusCode != 200 || result.Header.Get("Cache-Control") != "no-store" || json.NewDecoder(result.Body).Decode(&page) != nil || len(page.Clips) != 1 || page.Clips[0].Clip.ID != "demo" || page.Clips[0].Clip.Title == "" || page.Clips[0].Clip.Title == "demo" {
		t.Fatalf("routed history: status=%d body=%s", result.StatusCode, w.Body.String())
	}
}
