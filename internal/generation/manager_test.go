package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/idtoken"
)

const managerCaller = "rangle-stg-runtime@comin-up.iam.gserviceaccount.com"
const managerURL = "https://generation.rangle.test"

func managerClaims(service *Service, audience string) *idtoken.Payload {
	p := tokenClaims(service)
	p.Audience = audience
	p.Claims["email"] = managerCaller
	return p
}

func managerRequest(handler http.Handler, method, path, body, owner, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, managerURL+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer platform-only")
	if owner != "" {
		r.Header.Set(ownerHeader, owner)
	}
	if token != "" {
		r.Header.Set(serviceAuthorizationHeader, "Bearer "+token)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func managerFixture(t *testing.T, service *Service) http.Handler {
	t.Helper()
	service.Config.Database = "rangle-stg-generation"
	h, err := newManager(service, ManagerConfig{Audience: managerURL, Caller: managerCaller}, func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
		if token != "signed" || audience != managerURL {
			return nil, errors.New("invalid signature or audience")
		}
		return managerClaims(service, audience), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestManagerIndependentlyVerifiesCallerBeforeTrustingOwner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		token  string
		mutate func(*idtoken.Payload)
	}{
		{name: "platform header alone"}, {name: "forged signature", token: "forged"},
		{name: "foreign email", token: "signed", mutate: func(p *idtoken.Payload) { p.Claims["email"] = "other@comin-up.iam.gserviceaccount.com" }},
		{name: "scheduler on runtime API", token: "signed", mutate: func(p *idtoken.Payload) {
			p.Claims["email"] = "rangle-stg-generation-sync@comin-up.iam.gserviceaccount.com"
		}},
		{name: "deployer on runtime API", token: "signed", mutate: func(p *idtoken.Payload) {
			p.Claims["email"] = "rangle-stg-deploy@comin-up.iam.gserviceaccount.com"
		}},
		{name: "unverified email", token: "signed", mutate: func(p *idtoken.Payload) { p.Claims["email_verified"] = false }},
		{name: "string email verification", token: "signed", mutate: func(p *idtoken.Payload) { p.Claims["email_verified"] = "true" }},
		{name: "foreign issuer", token: "signed", mutate: func(p *idtoken.Payload) { p.Issuer = "https://evil.invalid" }},
		{name: "foreign audience", token: "signed", mutate: func(p *idtoken.Payload) { p.Audience = "https://evil.invalid" }},
		{name: "expired", token: "signed", mutate: func(p *idtoken.Payload) { p.Expires = 1 }},
		{name: "future issued", token: "signed", mutate: func(p *idtoken.Payload) { p.IssuedAt += 120 }},
		{name: "missing subject", token: "signed", mutate: func(p *idtoken.Payload) { p.Subject = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, provider := testService()
			service.Config.Database = "rangle-stg-generation"
			h, err := newManager(service, ManagerConfig{Audience: managerURL, Caller: managerCaller}, func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
				if token != "signed" {
					return nil, errors.New("signature")
				}
				payload := managerClaims(service, audience)
				if tc.mutate != nil {
					tc.mutate(payload)
				}
				return payload, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			response := managerRequest(h, "POST", "/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(1)+`"}`, "owner", tc.token)
			if response.Code != 401 || provider.submits.Load() != 0 || service.Store.(*memoryStore).a.Used != 0 {
				t.Fatalf("untrusted owner accepted: %d %s", response.Code, response.Body.String())
			}
		})
	}
	service, _ := testService()
	service.Config.Database = "rangle-stg-generation"
	h, err := NewManager(service, ManagerConfig{Audience: managerURL, Caller: managerCaller})
	if err != nil {
		t.Fatal(err)
	}
	if got := managerRequest(h, "GET", "/api/v1/generation/jobs", "", "owner", "forged").Code; got != 401 {
		t.Fatal("real signature validator accepted forged token", got)
	}
}

func TestManagerRejectsRawSpecsAndLedgerOperations(t *testing.T) {
	for _, field := range []string{"owner", "image", "input_uri", "service_account", "attempts", "deadline", "worker_pool_specs", "provider_name"} {
		t.Run(field, func(t *testing.T) {
			service, provider := testService()
			h := managerFixture(t, service)
			body := fmt.Sprintf(`{"preset":"vru-frame0","request_id":%q,%q:"arbitrary"}`, nonce(1), field)
			response := managerRequest(h, "POST", "/api/v1/generation/jobs", body, "owner", "signed")
			if response.Code != 400 || provider.submits.Load() != 0 || service.Store.(*memoryStore).a.Used != 0 {
				t.Fatalf("raw %s accepted: %d %s", field, response.Code, response.Body.String())
			}
		})
	}
	service, provider := testService()
	h := managerFixture(t, service)
	for _, tc := range []struct {
		path, body, owner string
		code              int
	}{
		{"/internal/generation/documents/generation_control/admission", `{"used":0}`, "owner", 404},
		{"/internal/generation/submit", `{}`, "owner", 404},
		{"/api/v1/generation/jobs", `{"preset":"unreviewed","request_id":"` + nonce(1) + `"}`, "owner", 400},
		{"/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"` + nonce(1) + `"}`, "", 403},
		{"/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"` + nonce(1) + `"}`, "../forged", 403},
	} {
		response := managerRequest(h, "POST", tc.path, tc.body, tc.owner, "signed")
		if response.Code != tc.code {
			t.Fatalf("%s: %d %s", tc.path, response.Code, response.Body.String())
		}
	}
	if provider.submits.Load() != 0 || service.Store.(*memoryStore).a.Used != 0 {
		t.Fatal("rejected manager calls consumed admission")
	}
}

func TestManagerOwnsAdmissionBudgetAndOwnerChecks(t *testing.T) {
	service, provider := testService()
	h := managerFixture(t, service)
	create := func(request int, expected int) Job {
		t.Helper()
		response := managerRequest(h, "POST", "/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(request)+`"}`, "owner", "signed")
		if response.Code != expected {
			t.Fatalf("create %d: %d %s", request, response.Code, response.Body.String())
		}
		var result struct{ Job Job }
		_ = json.Unmarshal(response.Body.Bytes(), &result)
		return result.Job
	}
	first := create(1, 202)
	create(2, 409) // The runtime cannot acquire a second active slot.
	if _, err := service.Store.Transition(context.Background(), first.ID, "", Failed, service.now()); err != nil {
		t.Fatal(err)
	}
	second := create(2, 202)
	if _, err := service.Store.Transition(context.Background(), second.ID, "", Failed, service.now()); err != nil {
		t.Fatal(err)
	}
	create(3, 409) // A fresh owner/request cannot reset the durable budget.
	if provider.submits.Load() != 2 || service.Store.(*memoryStore).a.Used != 2 {
		t.Fatal("manager bypassed or refunded admission")
	}
	// A separate instance retains owner isolation and the same durable ledger.
	restarted := managerFixture(t, service)
	for _, op := range []struct{ method, path, body string }{
		{"GET", "/api/v1/generation/jobs/" + first.ID, ""},
		{"DELETE", "/api/v1/generation/jobs/" + first.ID, ""},
		{"PUT", "/api/v1/generation/jobs/" + first.ID + "/publication", `{"published":false}`},
	} {
		response := managerRequest(restarted, op.method, op.path, op.body, "other-owner", "signed")
		if response.Code != 404 {
			t.Fatalf("foreign job leaked: %d %s", response.Code, response.Body.String())
		}
	}
	response := managerRequest(restarted, "POST", "/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(4)+`"}`, "other-owner", "signed")
	if response.Code != 409 || !strings.Contains(response.Body.String(), "generation_budget_exhausted") {
		t.Fatal("different owner reset global attempts", response.Code, response.Body.String())
	}
}

func TestManagerKeepsDeadlineAndRuntimeCancellation(t *testing.T) {
	service, provider := testService()
	h := managerFixture(t, service)
	job, err := service.Create(context.Background(), "owner", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	service.Now = func() time.Time { return job.CreatedAt.Add(MaxRuntime + time.Second) }
	response := managerRequest(h, "GET", "/api/v1/generation/jobs/"+job.ID, "", "owner", "signed")
	if response.Code != 200 || provider.cancels.Load() != 1 || !strings.Contains(response.Body.String(), `"state":"cancelling"`) {
		t.Fatal("manager lost runtime cancellation", response.Code, response.Body.String())
	}
	service.Now = func() time.Time { return service.Config.Deadline }
	response = managerRequest(h, "POST", "/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(2)+`"}`, "owner", "signed")
	if response.Code != 410 || provider.submits.Load() != 1 {
		t.Fatal("manager extended deadline", response.Code, response.Body.String())
	}
}

func TestManagerSyncUsesBaseAudienceAndDedicatedIdentity(t *testing.T) {
	for _, caller := range []string{managerCaller, "rangle-stg-generation-sync@comin-up.iam.gserviceaccount.com"} {
		t.Run(caller, func(t *testing.T) {
			service, provider := testService()
			service.Config.Database = "rangle-stg-generation"
			job, err := service.Create(context.Background(), "owner", "vru-frame0", nonce(1))
			if err != nil {
				t.Fatal(err)
			}
			provider.state = Succeeded
			h, err := newManager(service, ManagerConfig{Audience: managerURL, Caller: managerCaller, SyncAudience: managerURL}, func(_ context.Context, token, audience string) (*idtoken.Payload, error) {
				if token != "signed" || audience != managerURL {
					return nil, errors.New("signature or audience")
				}
				payload := managerClaims(service, audience)
				payload.Claims["email"] = caller
				return payload, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			response := syncRequest(h, "POST", SyncPath, "{}", "Bearer signed")
			want := 200
			if caller == managerCaller {
				want = 401
			}
			if response.Code != want || provider.submits.Load() != 1 || service.Store.(*memoryStore).a.Used != 1 {
				t.Fatal("scheduler broadened identity or admission", response.Code, response.Body.String())
			}
			stored, _ := service.Store.Get(context.Background(), job.ID)
			if (stored.State == Succeeded) != (want == 200) {
				t.Fatal("scheduler identity did not control reconciliation")
			}
		})
	}
}
