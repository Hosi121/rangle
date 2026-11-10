package generation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/community"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

type countedTokens struct{ calls atomic.Int32 }

func (s *countedTokens) Token() (*oauth2.Token, error) {
	s.calls.Add(1)
	return &oauth2.Token{AccessToken: "runtime-signed"}, nil
}

func remoteFixture(t *testing.T, service *Service) (*Remote, *countedTokens) {
	t.Helper()
	service.Config.Database = "rangle-stg-generation"
	server := httptest.NewUnstartedServer(nil)
	base := "https://" + server.Listener.Addr().String()
	manager, err := newManager(service, ManagerConfig{Audience: base, Caller: managerCaller}, func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
		if token != "runtime-signed" || audience != base {
			return nil, errors.New("signature or audience")
		}
		return managerClaims(service, audience), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer runtime-signed" || r.Header.Get(serviceAuthorizationHeader) != "Bearer runtime-signed" || r.Header.Get("X-Admin") != "" {
			t.Error("public client forwarded browser credentials or failed to supply independently verifiable service credentials")
		}
		manager.ServeHTTP(w, r)
	})
	server.StartTLS()
	t.Cleanup(server.Close)
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	tokens := &countedTokens{}
	return &Remote{base: server.URL, client: client, tokens: tokens, Now: service.now}, tokens
}

func TestRemotePublicFeedDetailAndEngagementSurviveWithdrawal(t *testing.T) {
	service, job, _ := fixturePublication(t, "static")
	remote, _ := remoteFixture(t, service)
	if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); err != nil {
		t.Fatal(err)
	}
	static, err := catalog.Load(strings.NewReader(`{"version":1,"clips":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	feed, err := catalog.NewHandler(static, service.Config.MediaOrigin)
	if err != nil {
		t.Fatal(err)
	}
	feed, err = catalog.WithPublicClips(feed, remote.PublicClips)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := NewRemoteHandler(remote, func(*http.Request) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	accounts := community.NewHandler(community.Config{Store: community.NewMemoryStore(), ClipExists: remote.CompletedClipExists})
	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/", accounts)
	mux.Handle("/api/v1/generation/", jobs)
	mux.Handle("/api/v1/clips/{clip}", jobs.ClipHandler(feed))
	for _, pattern := range []string{"/api/v1/clips/{clip}/engagement", "/api/v1/clips/{clip}/likes", "/api/v1/clips/{clip}/comments"} {
		mux.Handle(pattern, accounts)
	}
	mux.Handle("/api/", feed)
	path := "/api/v1/clips/generated-" + job.ID
	registered := invokeCommunity(mux, "POST", "/api/v1/auth/register", `{"username":"viewer","password":"a long test password 2026"}`, nil)
	if registered.Code != 201 {
		t.Fatal(registered.Code, registered.Body.String())
	}
	viewer := registered.Result().Cookies()[0]
	if response := invokeCommunity(mux, "PUT", path+"/likes", "", viewer); response.Code != 204 {
		t.Fatal("remote generated likes unavailable", response.Code, response.Body.String())
	}
	if response := invokeCommunity(mux, "POST", path+"/comments", `{"text":"Shared angle","request_id":"`+nonce(9)+`"}`, viewer); response.Code != 201 {
		t.Fatal("remote generated comments unavailable", response.Code, response.Body.String())
	}
	for _, published := range []bool{true, false} {
		if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, published); err != nil {
			t.Fatal(err)
		}
		response := invokeCommunity(mux, "GET", "/api/v1/clips", "", nil)
		var page struct{ Clips []catalog.PublicClip }
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &page) != nil || (len(page.Clips) == 1) != published {
			t.Fatal("remote feed membership incorrect", response.Code, response.Body.String())
		}
		for _, method := range []string{"GET", "HEAD"} {
			response = invokeCommunity(mux, method, path, "", nil)
			if response.Code != 200 || (method == "HEAD" && response.Body.Len() != 0) ||
				strings.Contains(response.Body.String(), "input_uri") || strings.Contains(response.Body.String(), "provider_name") || strings.Contains(response.Body.String(), "preset_snapshot") || strings.Contains(response.Body.String(), "owner") {
				t.Fatal("public detail leaked job internals or required a cookie", response.Code, response.Body.String())
			}
		}
		checkGeneratedCounts(t, invokeCommunity(mux, "GET", path+"/engagement", "", viewer), 1, true)
	}
	if response := invokeCommunity(mux, "GET", publicClipsPath, "", nil); response.Code != 404 {
		t.Fatal("manager internals exposed publicly", response.Code)
	}
	if response := invokeCommunity(mux, "GET", "/api/v1/generation/jobs/"+job.ID, "", nil); response.Code != 401 {
		t.Fatal("public metadata enabled private jobs", response.Code)
	}
	if exists, err := remote.CompletedClipExists(context.Background(), "generated-"+nonce(7)); exists || err != nil {
		t.Fatal("missing remote clip accepted", exists, err)
	}
}

func TestRemoteBrowserAuthorizationCSRFAndForwardedOwner(t *testing.T) {
	service, provider := testService()
	remote, tokens := remoteFixture(t, service)
	store := community.NewMemoryStore()
	admin, err := community.NewAdministrator("test administrator password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(context.Background(), admin); err != nil {
		t.Fatal(err)
	}
	accounts := community.NewHandler(community.Config{Store: store})
	jobs, err := NewRemoteHandler(remote, func(r *http.Request) (string, error) {
		user, err := accounts.AdminUser(r)
		if errors.Is(err, community.ErrForbidden) {
			return "", ErrForbidden
		}
		if err != nil || user == nil {
			return "", err
		}
		return user.ID, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/auth/", accounts)
	mux.Handle("/api/v1/generation/", jobs)
	registered := invokeCommunity(mux, "POST", "/api/v1/auth/register", `{"username":"member","password":"a long test password 2026"}`, nil)
	if registered.Code != 201 {
		t.Fatal(registered.Code, registered.Body.String())
	}
	member := registered.Result().Cookies()[0]
	login := invokeCommunity(mux, "POST", "/api/v1/auth/login", `{"username":"admin","password":"test administrator password"}`, nil)
	if login.Code != 200 {
		t.Fatal(login.Code, login.Body.String())
	}
	adminCookie := login.Result().Cookies()[0]
	for _, op := range []struct{ method, path string }{
		{"GET", "/api/v1/generation/presets"}, {"GET", "/api/v1/generation/jobs"}, {"POST", "/api/v1/generation/jobs"},
		{"GET", "/api/v1/generation/jobs/" + nonce(1)}, {"DELETE", "/api/v1/generation/jobs/" + nonce(1)}, {"PUT", "/api/v1/generation/jobs/" + nonce(1) + "/publication"},
	} {
		for _, tc := range []struct {
			cookie *http.Cookie
			code   int
		}{{nil, 401}, {member, 403}} {
			response := invokeCommunity(mux, op.method, op.path, `{}`, tc.cookie)
			if response.Code != tc.code {
				t.Fatalf("browser auth %s %s: %d %s", op.method, op.path, response.Code, response.Body.String())
			}
		}
	}
	if tokens.calls.Load() != 0 {
		t.Fatal("unauthorized browser reached manager")
	}
	request := func(body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://app.rangle.test/api/v1/generation/jobs", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", origin)
		r.Header.Set(ownerHeader, "forged-owner")
		r.Header.Set(serviceAuthorizationHeader, "Bearer forged")
		r.Header.Set("Authorization", "Bearer forged")
		r.Header.Set("X-Admin", "true")
		r.AddCookie(adminCookie)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	body := `{"preset":"vru-frame0","request_id":"` + nonce(1) + `"}`
	if response := request(body, "https://evil.invalid"); response.Code != 403 || tokens.calls.Load() != 0 {
		t.Fatal("cross origin admin request reached manager", response.Code)
	}
	if response := request(strings.TrimSuffix(body, "}")+`,"owner":"forged-owner"}`, "https://app.rangle.test"); response.Code != 400 || provider.submits.Load() != 0 {
		t.Fatal("owner body bypassed manager validation", response.Code, response.Body.String())
	}
	response := request(body, "https://app.rangle.test")
	if response.Code != 202 || provider.submits.Load() != 1 {
		t.Fatal("admin create failed", response.Code, response.Body.String())
	}
	var result struct{ Job Job }
	_ = json.Unmarshal(response.Body.Bytes(), &result)
	stored, err := service.Store.Get(context.Background(), result.Job.ID)
	if err != nil || stored.Owner != admin.User.ID {
		t.Fatal("caller headers replaced authenticated owner", stored.Owner, err)
	}
}
