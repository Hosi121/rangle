package generation

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/community"
)

func TestForgedBrowserRoleCannotAuthorizeGeneration(t *testing.T) {
	store := community.NewMemoryStore()
	accounts := community.NewHandler(community.Config{Store: store})
	service, provider := testService()
	jobs, err := NewHandler(service, func(r *http.Request) (string, error) {
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
	request := func(method, path, body string, cookie *http.Cookie) *http.Response {
		r := httptest.NewRequest(method, "https://app.rangle.test"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "https://app.rangle.test")
		r.Header.Set("X-Admin", "true")
		r.Header.Set("X-Test-User", "admin")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		response := w.Result()
		t.Cleanup(func() { response.Body.Close() })
		return response
	}
	registered := request("POST", "/api/v1/auth/register", `{"username":"member","password":"a long test password 2026"}`, nil)
	if registered.StatusCode != 201 || len(registered.Cookies()) != 1 {
		t.Fatal("ordinary account registration failed")
	}
	cookie := registered.Cookies()[0]
	for _, op := range []struct{ method, path string }{
		{"GET", "/api/v1/generation/presets"},
		{"GET", "/api/v1/generation/jobs"},
		{"POST", "/api/v1/generation/jobs"},
		{"GET", "/api/v1/generation/jobs/" + nonce(1)},
		{"DELETE", "/api/v1/generation/jobs/" + nonce(1)},
		{"PUT", "/api/v1/generation/jobs/" + nonce(1) + "/publication"},
	} {
		response := request(op.method, op.path, `{"is_admin":true}`, cookie)
		body, _ := io.ReadAll(response.Body)
		if response.StatusCode != 403 || !strings.Contains(string(body), "admin_required") {
			t.Fatalf("forged role %s %s: %d %s", op.method, op.path, response.StatusCode, body)
		}
	}
	forged := *cookie
	forged.Value = base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if got := request("GET", "/api/v1/generation/presets", "", &forged).StatusCode; got != 401 {
		t.Fatalf("forged session: %d", got)
	}
	admin, err := community.NewAdministrator("test administrator password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(context.Background(), admin); err != nil {
		t.Fatal(err)
	}
	login := request("POST", "/api/v1/auth/login", `{"username":"admin","password":"test administrator password"}`, nil)
	if login.StatusCode != 200 || len(login.Cookies()) != 1 {
		t.Fatal("trusted administrator login failed")
	}
	if got := request("GET", "/api/v1/generation/presets", "", login.Cookies()[0]).StatusCode; got != 200 {
		t.Fatalf("trusted administrator: %d", got)
	}
	if provider.submits.Load() != 0 {
		t.Fatal("authorization probes started a GPU job")
	}
}

func TestAllGenerationOperationsRequireAdministrator(t *testing.T) {
	s, provider := testService()
	h, err := NewHandler(s, func(*http.Request) (string, error) { return "", ErrForbidden })
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ method, path string }{
		{"GET", "/api/v1/generation/presets"},
		{"GET", "/api/v1/generation/jobs"},
		{"POST", "/api/v1/generation/jobs"},
		{"GET", "/api/v1/generation/jobs/" + nonce(1)},
		{"DELETE", "/api/v1/generation/jobs/" + nonce(1)},
		{"PUT", "/api/v1/generation/jobs/" + nonce(1) + "/publication"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			r := call(t, h, op.method, op.path, `{}`, "", "")
			if r.StatusCode != 403 {
				t.Fatalf("got %d", r.StatusCode)
			}
			if !strings.Contains(string(decode(t, r)["error"]), "admin_required") {
				t.Fatal("wrong access error")
			}
		})
	}
	if provider.submits.Load() != 0 {
		t.Fatal("denied request started a GPU job")
	}
	public, _, _ := testHandler(t)
	if got := call(t, public, "GET", "/api/v1/generation/presets", "", "", "").StatusCode; got != 401 {
		t.Fatalf("anonymous presets: %d", got)
	}
}
