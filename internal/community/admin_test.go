package community

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdministratorProvisioningAndCurrentRole(t *testing.T) {
	h, store := testHandler()
	a, err := NewAdministrator("demo-pass")
	if err != nil || !a.User.Admin || a.User.ID != "admin" || !checkPassword(a, "demo-pass") {
		t.Fatalf("administrator provisioning: %v", err)
	}
	if err := store.CreateAccount(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(context.Background(), a); !errors.Is(err, ErrConflict) {
		t.Fatal("bootstrap must not replace an existing account")
	}
	response, body := call(t, h, "POST", "/api/v1/auth/login", `{"username":"admin","password":"demo-pass"}`, nil, nil)
	if response.StatusCode != 200 || len(response.Cookies()) != 1 {
		t.Fatalf("login: %d %s", response.StatusCode, body)
	}
	var login struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(body, &login); err != nil || !login.User.Admin {
		t.Fatalf("login did not publish the trusted role: %s", body)
	}
	for _, private := range []string{"salt", "hash", "demo-pass"} {
		if strings.Contains(string(body), private) {
			t.Fatal("login exposed private credentials")
		}
	}
	r := httptest.NewRequest("GET", "https://rangle.test/api/v1/generation/jobs", nil)
	r.AddCookie(response.Cookies()[0])
	if user, err := h.AdminUser(r); err != nil || user == nil || user.ID != "admin" {
		t.Fatalf("administrator denied: %v", err)
	}
	// A session containing the old role cannot keep privileges after revocation.
	store.mu.Lock()
	a.User.Admin = false
	store.accounts[a.User.ID] = a
	store.mu.Unlock()
	if _, err := h.AdminUser(r); !errors.Is(err, ErrForbidden) {
		t.Fatal("session snapshot retained revoked privileges")
	}
	_, body = call(t, h, "GET", "/api/v1/auth/me", "", response.Cookies()[0], nil)
	// Decode into a fresh value because false roles are omitted from JSON.
	var current struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(body, &current); err != nil || current.User.Admin {
		t.Fatal("me returned a stale role")
	}
}

func TestPublicRegistrationCannotClaimAdministrator(t *testing.T) {
	h, _ := testHandler()
	for _, body := range []string{
		`{"username":" ADMIN ","password":"a long test password 2026"}`,
		`{"username":"member","password":"a long test password 2026","is_admin":true}`,
	} {
		r, _ := call(t, h, "POST", "/api/v1/auth/register", body, nil, nil)
		if r.StatusCode != 400 && r.StatusCode != 409 {
			t.Fatalf("privileged registration: %d", r.StatusCode)
		}
	}
	cookie := registerUser(t, h, "member")
	r := httptest.NewRequest(http.MethodGet, "https://rangle.test/api/v1/generation/jobs", nil)
	r.AddCookie(cookie)
	if _, err := h.AdminUser(r); !errors.Is(err, ErrForbidden) {
		t.Fatal("ordinary account gained privileges")
	}
	if validPassword("demo-pass") {
		t.Fatal("public password policy was weakened")
	}
}
