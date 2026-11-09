package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/community"
)

func TestPublishedGeneratedClipSupportsPersistentCommunityActions(t *testing.T) {
	service, job, _ := fixturePublication(t, "dynamic")
	interactions := community.NewHandler(community.Config{Store: community.NewMemoryStore(), ClipIDs: map[string]bool{"static": true}, ClipExists: service.PublishedClipExists})
	invoke := func(method, path, body string, cookie *http.Cookie) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, "https://app.rangle.test"+path, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if cookie != nil {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		interactions.ServeHTTP(response, request)
		return response
	}
	registered := invoke("POST", "/api/v1/auth/register", `{"username":"owner","password":"a long test password 2026"}`, nil)
	if registered.Code != 201 {
		t.Fatal(registered.Code, registered.Body.String())
	}
	cookie := registered.Result().Cookies()[0]
	prefix := "/api/v1/clips/generated-" + job.ID
	if response := invoke("GET", prefix+"/engagement", "", nil); response.Code != 404 {
		t.Fatal("unlisted generated community clip existed")
	}
	if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); err != nil {
		t.Fatal(err)
	}
	if response := invoke("PUT", prefix+"/likes", "", cookie); response.Code != 204 {
		t.Fatalf("like: %d %s", response.Code, response.Body.String())
	}
	if response := invoke("POST", prefix+"/comments", `{"text":"Generated angle","request_id":"`+nonce(1)+`"}`, cookie); response.Code != 201 {
		t.Fatalf("comment: %d %s", response.Code, response.Body.String())
	}
	check := func() {
		t.Helper()
		response := invoke("GET", prefix+"/engagement", "", cookie)
		var counts struct {
			Likes, Comments int
			Liked           bool
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &counts) != nil || counts.Likes != 1 || counts.Comments != 1 || !counts.Liked {
			t.Fatalf("engagement: %d %s", response.Code, response.Body.String())
		}
	}
	check()
	if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, false); err != nil {
		t.Fatal(err)
	}
	if response := invoke("GET", prefix+"/comments", "", nil); response.Code != 404 {
		t.Fatal("unpublished generated interaction stayed writable")
	}
	if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); err != nil {
		t.Fatal(err)
	}
	check() // Stable clip IDs retain existing likes/comments across membership changes.
	if response := invoke("GET", "/api/v1/clips/static/engagement", "", nil); response.Code != 200 {
		t.Fatal("static interactions changed")
	}
}
