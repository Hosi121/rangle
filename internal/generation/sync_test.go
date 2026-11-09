package generation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/idtoken"
)

type syncProvider struct {
	Provider
	statuses, finds int
	statusError     error
}

func (p *syncProvider) Status(ctx context.Context, name string) (State, error) {
	p.statuses++
	if p.statusError != nil {
		return "", p.statusError
	}
	return p.Provider.Status(ctx, name)
}
func (p *syncProvider) Find(ctx context.Context, id string) (string, error) {
	p.finds++
	return p.Provider.Find(ctx, id)
}

func TestManagedSyncReconcilesWithoutRequestsOrAdditionalAdmission(t *testing.T) {
	service, provider := testService()
	job, err := service.Create(context.Background(), "owner", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	spy := &syncProvider{Provider: provider}
	service.Provider = spy
	provider.state = Succeeded
	result, err := service.SyncActive(context.Background())
	if err != nil || result == nil || result.State != Succeeded {
		t.Fatalf("sync: %+v %v", result, err)
	}
	active, err := service.Store.Active(context.Background())
	if err != nil || active != nil {
		t.Fatal("completed job retained the active slot")
	}
	memory := service.Store.(*memoryStore)
	if memory.a.Used != 1 || provider.submits.Load() != 1 || spy.statuses != 1 {
		t.Fatal("sync submitted or refunded an attempt")
	}
	result, err = service.SyncActive(context.Background())
	if err != nil || result != nil || spy.statuses != 1 {
		t.Fatal("idle sync contacted Vertex")
	}
	persisted, err := service.Store.Get(context.Background(), job.ID)
	if err != nil || persisted.State != Succeeded {
		t.Fatal("completion was not durable")
	}
}
func TestManagedSyncKeepsRuntimeCancellationAndNeverPollsPastDeadline(t *testing.T) {
	service, provider := testService()
	job, err := service.Create(context.Background(), "owner", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	spy := &syncProvider{Provider: provider}
	service.Provider = spy
	service.Now = func() time.Time { return job.CreatedAt.Add(MaxRuntime + time.Second) }
	synced, err := service.SyncActive(context.Background())
	if err != nil || synced.State != Cancelling || provider.cancels.Load() != 1 {
		t.Fatal("runtime timeout was not enforced", err)
	}
	spy.statusError = errors.New("provider unavailable")
	if _, err := service.SyncActive(context.Background()); err == nil {
		t.Fatal("provider failure was hidden")
	}
	memory := service.Store.(*memoryStore)
	if memory.a.Used != 1 || memory.a.Active != job.ID {
		t.Fatal("failed sync refunded or released admission")
	}
	count := spy.statuses
	service.Now = func() time.Time { return service.Config.Deadline }
	if _, err := service.SyncActive(context.Background()); !errors.Is(err, ErrExpired) || spy.statuses != count {
		t.Fatal("deadline sync contacted Vertex", err)
	}
}

const syncAudience = "https://app.rangle.test/internal/generation/sync"

func tokenClaims(service *Service) *idtoken.Payload {
	return &idtoken.Payload{Issuer: "https://accounts.google.com", Audience: syncAudience, Subject: "123456789012345678901", Expires: service.now().Add(time.Hour).Unix(), IssuedAt: service.now().Unix(), Claims: map[string]any{"email": "rangle-stg-generation-sync@comin-up.iam.gserviceaccount.com", "email_verified": true}}
}
func syncRequest(handler http.Handler, method, path, body, authorization string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "https://app.rangle.test"+path, strings.NewReader(body))
	if authorization != "" {
		request.Header.Set("Authorization", authorization)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
func TestSyncOIDCVerifiesFixedIdentityIssuerAudienceAndClaims(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mutate      func(*idtoken.Payload)
		auth        string
		verifyError error
	}{
		{name: "missing authorization"}, {name: "bad signature", auth: "Bearer signed", verifyError: errors.New("signature")},
		{name: "foreign email", auth: "Bearer signed", mutate: func(p *idtoken.Payload) { p.Claims["email"] = "other@comin-up.iam.gserviceaccount.com" }},
		{name: "unverified email", auth: "Bearer signed", mutate: func(p *idtoken.Payload) { p.Claims["email_verified"] = false }},
		{name: "string email verified", auth: "Bearer signed", mutate: func(p *idtoken.Payload) { p.Claims["email_verified"] = "true" }},
		{name: "foreign issuer", auth: "Bearer signed", mutate: func(p *idtoken.Payload) { p.Issuer = "https://evil.invalid" }},
		{name: "foreign audience", auth: "Bearer signed", mutate: func(p *idtoken.Payload) { p.Audience = "https://other.invalid" }},
		{name: "expired", auth: "Bearer signed", mutate: func(p *idtoken.Payload) { p.Expires = 1 }},
		{name: "future issued", auth: "Bearer signed", mutate: func(p *idtoken.Payload) { p.IssuedAt += 120 }},
		{name: "missing subject", auth: "Bearer signed", mutate: func(p *idtoken.Payload) { p.Subject = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, provider := testService()
			spy := &syncProvider{Provider: provider}
			service.Provider = spy
			payload := tokenClaims(service)
			if tc.mutate != nil {
				tc.mutate(payload)
			}
			handler, err := newSyncHandler(service, syncAudience, func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
				if token != "signed" || audience != syncAudience {
					t.Fatal("validator contract changed")
				}
				return payload, tc.verifyError
			})
			if err != nil {
				t.Fatal(err)
			}
			response := syncRequest(handler, "POST", SyncPath, "{}", tc.auth)
			if response.Code != 401 || response.Header().Get("Cache-Control") != "no-store" || provider.submits.Load() != 0 || spy.statuses != 0 {
				t.Fatalf("untrusted sync: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
func TestSyncRejectsForgedTokensWithoutNetworkAndBoundsValidTicks(t *testing.T) {
	service, _ := testService()
	real, err := NewSyncHandler(service, syncAudience)
	if err != nil {
		t.Fatal(err)
	}
	if response := syncRequest(real, "POST", SyncPath, "{}", "Bearer forged"); response.Code != 401 {
		t.Fatal("forged OIDC accepted")
	}
	handler, err := newSyncHandler(service, syncAudience, func(context.Context, string, string) (*idtoken.Payload, error) { return tokenClaims(service), nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", SyncPath, "", 405}, {"POST", SyncPath + "?job=arbitrary", "{}", 400}, {"POST", SyncPath, `{"job":"arbitrary"}`, 400},
	} {
		response := syncRequest(handler, tc.method, tc.path, tc.body, "Bearer signed")
		if response.Code != tc.status {
			t.Fatalf("invalid sync %d %s", response.Code, response.Body.String())
		}
	}
	if response := syncRequest(handler, "POST", SyncPath, "{}", "Bearer signed"); response.Code != 200 {
		t.Fatal(response.Code)
	}
	if response := syncRequest(handler, "POST", SyncPath, "{}", "Bearer signed"); response.Code != 429 || response.Header().Get("Retry-After") != "60" {
		t.Fatal("valid replay was not bounded")
	}
	service.Now = func() time.Time { return service.Config.Deadline }
	if response := syncRequest(handler, "POST", SyncPath, "{}", "Bearer signed"); response.Code != 410 {
		t.Fatal("expired scheduler was accepted")
	}
	for _, audience := range []string{"", "http://app.rangle.test" + SyncPath, syncAudience + "?job=1", syncAudience + "/", "https://user@app.rangle.test" + SyncPath} {
		if _, err := NewSyncHandler(service, audience); err == nil {
			t.Fatal("invalid audience accepted", audience)
		}
	}
}

func TestSyncConfigurationCannotExtendTheAuthorizedWindow(t *testing.T) {
	service, provider := testService()
	service.Config.Deadline = service.Config.Deadline.Add(24 * time.Hour)
	service.Now = func() time.Time { return time.Date(2026, time.November, 14, 15, 0, 0, 0, time.UTC) }
	if _, err := service.SyncActive(context.Background()); !errors.Is(err, ErrExpired) {
		t.Fatal("config extended the fixed window", err)
	}
	verified := false
	handler, err := newSyncHandler(service, syncAudience, func(context.Context, string, string) (*idtoken.Payload, error) {
		verified = true
		return tokenClaims(service), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := syncRequest(handler, "POST", SyncPath, "{}", "Bearer signed"); response.Code != 410 || verified || provider.submits.Load() != 0 {
		t.Fatal("expired sync performed authenticated work")
	}
}
