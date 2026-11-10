package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/community"
	"golang.org/x/time/rate"
)

func TestCompletedGeneratedClipCommunityIsIndependentOfFeedMembership(t *testing.T) {
	exerciseGeneratedCommunity(t, nil, community.NewMemoryStore())
}

func TestFirestoreSharedGeneratedInteractionsSurviveWithdrawalAndRestart(t *testing.T) {
	host := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if host == "" {
		t.Skip("requires a loopback Firestore emulator")
	}
	if !strings.HasPrefix(host, "127.0.0.1:") {
		t.Fatal("requires loopback emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	project := fmt.Sprintf("demo-shared-generated-%d", time.Now().UnixNano())
	openJobs := func() *FirestoreStore {
		store, err := NewFirestoreStore(ctx, project, "rangle-stg")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	openCommunity := func() *community.FirestoreStore {
		store, err := community.NewFirestoreStore(ctx, project, "rangle-stg")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}
	job, viewer := exerciseGeneratedCommunity(t, openJobs(), openCommunity())
	// Independent clients read the completed shared clip and all reactions after
	// a restart, without a process-local feed membership or community ID cache.
	service := &Service{Store: openJobs()}
	stored, err := service.Store.Get(ctx, job.ID)
	if err != nil || stored.Published {
		t.Fatal("restart did not retain withdrawal", err)
	}
	interactions := community.NewHandler(community.Config{Store: openCommunity(), ClipExists: service.CompletedClipExists})
	response := invokeCommunity(interactions, "GET", "/api/v1/clips/generated-"+job.ID+"/engagement", "", viewer)
	checkGeneratedCounts(t, response, 2, true)
}

func invokeCommunity(handler http.Handler, method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://app.rangle.test"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		request.AddCookie(cookie)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
func checkGeneratedCounts(t *testing.T, response *httptest.ResponseRecorder, comments int, liked bool) {
	t.Helper()
	var counts struct {
		Likes, Comments int
		Liked           bool
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &counts) != nil || counts.Likes != 1 || counts.Comments != comments || counts.Liked != liked {
		t.Fatalf("engagement: %d %s", response.Code, response.Body.String())
	}
}

func exerciseGeneratedCommunity(t *testing.T, jobsStore Store, communityStore community.Store) (Job, *http.Cookie) {
	t.Helper()
	service, job, artifacts := fixturePublication(t, "dynamic")
	if jobsStore != nil {
		stored, created, err := jobsStore.Admit(context.Background(), job.Owner, job.Snapshot, nonce(1), 2, job.CreatedAt)
		if err != nil || !created || stored.ID != job.ID {
			t.Fatalf("admission: %+v %v", stored, err)
		}
		if _, err := jobsStore.Transition(context.Background(), job.ID, "", Succeeded, job.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		service.Store = jobsStore
	}
	interactions := community.NewHandler(community.Config{Store: communityStore, ClipIDs: map[string]bool{"static": true}, ClipExists: service.CompletedClipExists})
	jobs, err := NewHandler(service, func(r *http.Request) (string, error) {
		user, err := interactions.SessionUser(r)
		if err != nil || user == nil {
			return "", err
		}
		return user.ID, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	jobs.limiter = rate.NewLimiter(rate.Inf, 100)
	static, err := catalog.Load(strings.NewReader(`{"version":1,"clips":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	feed, err := catalog.NewHandler(static, service.Config.MediaOrigin)
	if err != nil {
		t.Fatal(err)
	}
	feed, err = catalog.WithPublicClips(feed, service.PublicClips)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/", interactions)
	mux.Handle("/api/v1/generation/", jobs)
	for _, pattern := range []string{"/api/v1/clips/{clip}/engagement", "/api/v1/clips/{clip}/likes", "/api/v1/clips/{clip}/comments"} {
		mux.Handle(pattern, interactions)
	}
	mux.Handle("/api/v1/clips/{clip}", jobs.ClipHandler(feed))
	mux.Handle("/api/", feed)
	invoke := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		return invokeCommunity(mux, method, path, body, cookie)
	}
	register := func(name string) *http.Cookie {
		response := invoke("POST", "/api/v1/auth/register", `{"username":"`+name+`","password":"a long test password 2026"}`, nil)
		if response.Code != 201 {
			t.Fatal(response.Code, response.Body.String())
		}
		return response.Result().Cookies()[0]
	}
	owner, viewer := register("owner"), register("viewer")
	prefix := "/api/v1/clips/generated-" + job.ID
	publication := "/api/v1/generation/jobs/" + job.ID + "/publication"
	checkFeed := func(expected int) {
		t.Helper()
		response := invoke("GET", "/api/v1/clips", "", nil)
		var page struct {
			Clips []catalog.PublicClip `json:"clips"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || len(page.Clips) != expected {
			t.Fatalf("feed: %d %s", response.Code, response.Body.String())
		}
	}
	checkDetail := func() {
		t.Helper()
		response := invoke("GET", prefix, "", nil)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"id":"generated-`+job.ID+`"`) {
			t.Fatalf("shared detail: %d %s", response.Code, response.Body.String())
		}
	}
	manage := func(published bool) {
		t.Helper()
		response := invoke("PUT", publication, fmt.Sprintf(`{"published":%t}`, published), owner)
		if response.Code != 200 {
			t.Fatalf("publication: %d %s", response.Code, response.Body.String())
		}
	}
	react := func(request int) {
		t.Helper()
		if response := invoke("PUT", prefix+"/likes", "", viewer); response.Code != 204 {
			t.Fatalf("like: %d %s", response.Code, response.Body.String())
		}
		response := invoke("POST", prefix+"/comments", `{"text":"Shared generated angle","request_id":"`+nonce(request)+`"}`, viewer)
		if response.Code != 201 {
			t.Fatalf("comment: %d %s", response.Code, response.Body.String())
		}
	}
	checkFeed(0)
	checkDetail()
	react(1)
	checkGeneratedCounts(t, invoke("GET", prefix+"/engagement", "", viewer), 1, true)
	if artifacts.requests.Load() != 0 {
		t.Fatal("shared interactions started artifact verification")
	}
	for _, path := range []string{"/api/v1/generation/jobs/" + job.ID, publication} {
		method, body := "GET", ""
		if path == publication {
			method, body = "PUT", `{"published":true}`
		}
		if response := invoke(method, path, body, viewer); response.Code != 404 {
			t.Fatal("non-owner could inspect/manage job", response.Code)
		}
	}
	history := invoke("GET", "/api/v1/generation/jobs", "", viewer)
	if history.Code != 200 || strings.Contains(history.Body.String(), job.ID) {
		t.Fatal("owner history leaked to another viewer")
	}
	if response := invoke("POST", prefix+"/comments", `{"text":"Anonymous","request_id":"`+nonce(9)+`"}`, nil); response.Code != 401 {
		t.Fatal("anonymous mutation was accepted")
	}
	manage(true)
	checkFeed(1)
	checkGeneratedCounts(t, invoke("GET", prefix+"/engagement", "", nil), 1, false)
	manage(false)
	checkFeed(0)
	checkDetail()
	checkGeneratedCounts(t, invoke("GET", prefix+"/engagement", "", viewer), 1, true)
	react(2)
	checkGeneratedCounts(t, invoke("GET", prefix+"/engagement", "", viewer), 2, true)
	if response := invoke("GET", prefix+"/comments", "", nil); response.Code != 200 || !strings.Contains(response.Body.String(), "Shared generated angle") {
		t.Fatal("withdrawal hid existing comments")
	}
	if response := invoke("PUT", publication, `{"published":false}`, viewer); response.Code != 404 {
		t.Fatal("non-owner withdrawal was accepted")
	}
	manage(true)
	checkFeed(1)
	checkGeneratedCounts(t, invoke("GET", prefix+"/engagement", "", viewer), 2, true)
	pending, _, err := service.Store.Admit(context.Background(), job.Owner, job.Snapshot, nonce(2), 2, service.now())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"generated-" + pending.ID, "generated-" + nonce(999), "generated-not-a-job", "unknown"} {
		for _, route := range []struct{ method, suffix, body string }{{"GET", "/engagement", ""}, {"GET", "/comments", ""}, {"PUT", "/likes", ""}, {"POST", "/comments", `{"text":"Missing","request_id":"` + nonce(8) + `"}`}} {
			response := invoke(route.method, "/api/v1/clips/"+id+route.suffix, route.body, viewer)
			if response.Code != 404 {
				t.Fatalf("unknown/unfinished interaction %s: %d", id, response.Code)
			}
		}
	}
	if response := invoke("GET", "/api/v1/clips/static/engagement", "", nil); response.Code != 200 {
		t.Fatal("static interactions changed")
	}
	manage(false)
	checkFeed(0)
	return job, viewer
}
