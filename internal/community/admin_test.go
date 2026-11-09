package community

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAdministratorPasswordPolicy(t *testing.T) {
	for _, tt := range []struct {
		name     string
		password string
		valid    bool
	}{
		{"empty", "", false},
		{"legacy short password", "demo-pass", false},
		{"ASCII below minimum", strings.Repeat("a", 14), false},
		{"ASCII minimum", strings.Repeat("a", 15), true},
		{"ASCII maximum", strings.Repeat("a", 128), true},
		{"ASCII above maximum", strings.Repeat("a", 129), false},
		{"Unicode below minimum", strings.Repeat("界", 14), false},
		{"Unicode minimum", strings.Repeat("界", 15), true},
		{"Unicode maximum", strings.Repeat("界", 128), true},
		{"Unicode above maximum", strings.Repeat("界", 129), false},
		{"512 byte maximum", strings.Repeat("😀", 128), true},
		{"513 bytes", strings.Repeat("😀", 128) + "a", false},
		{"invalid UTF-8", strings.Repeat("a", 14) + "\xff", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewAdministrator(tt.password)
			if !tt.valid {
				if err == nil {
					t.Fatal("invalid administrator password accepted")
				}
				if a.User != (User{}) || len(a.Salt) != 0 || len(a.Hash) != 0 || a.HashVersion != 0 {
					t.Fatal("rejected password returned account credentials")
				}
				return
			}
			if err != nil || !a.User.Admin || a.User.ID != "admin" || !checkPassword(a, tt.password) {
				t.Fatalf("valid administrator password rejected: %v", err)
			}
		})
	}
}

func TestAdministratorProvisioningAndCurrentRole(t *testing.T) {
	h, store := testHandler()
	a, err := NewAdministrator("test administrator password")
	if err != nil || !a.User.Admin || a.User.ID != "admin" || !checkPassword(a, "test administrator password") {
		t.Fatalf("administrator provisioning: %v", err)
	}
	if err := store.CreateAccount(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	replacement, err := NewAdministrator("replacement administrator password")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a.Salt, replacement.Salt) {
		t.Fatal("administrators shared a password salt")
	}
	if err := store.CreateAccount(context.Background(), replacement); !errors.Is(err, ErrConflict) {
		t.Fatal("bootstrap must not replace an existing account")
	}
	stored, err := store.Account(context.Background(), "admin")
	if err != nil || stored.User != a.User || !bytes.Equal(stored.Salt, a.Salt) || !bytes.Equal(stored.Hash, a.Hash) || stored.HashVersion != a.HashVersion {
		t.Fatal("bootstrap modified existing credentials")
	}
	response, body := call(t, h, "POST", "/api/v1/auth/login", `{"username":"admin","password":"test administrator password"}`, nil, nil)
	if response.StatusCode != 200 || len(response.Cookies()) != 1 {
		t.Fatalf("login: %d %s", response.StatusCode, body)
	}
	var login struct {
		User User `json:"user"`
	}
	if err := json.Unmarshal(body, &login); err != nil || !login.User.Admin {
		t.Fatalf("login did not publish the trusted role: %s", body)
	}
	for _, private := range []string{"salt", "hash", "test administrator password"} {
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

func TestExistingAdministratorCredentialsAndSessionsRemainUsable(t *testing.T) {
	h, store := testHandler()
	ctx := context.Background()
	// Model an account and session stored before the provisioning policy changed.
	a := newAccount("admin", "管理者", "demo-pass")
	a.User.Admin = true
	if err := store.CreateAccount(ctx, a); err != nil {
		t.Fatal(err)
	}
	cookie := h.cookie(strings.Repeat("A", 43), 3600)
	if err := store.PutSession(ctx, sessionKey(cookie.Value), Session{a.User, time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodGet, "https://rangle.test/api/v1/generation/jobs", nil)
	r.AddCookie(cookie)
	if user, err := h.AdminUser(r); err != nil || user == nil || !user.Admin {
		t.Fatalf("existing administrator session denied: %v", err)
	}
	response, body := call(t, h, "POST", "/api/v1/auth/login", `{"username":"admin","password":"demo-pass"}`, nil, nil)
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 {
		t.Fatalf("existing administrator login denied: %d %s", response.StatusCode, body)
	}
	r = httptest.NewRequest(http.MethodGet, "https://rangle.test/api/v1/generation/jobs", nil)
	r.AddCookie(response.Cookies()[0])
	if user, err := h.AdminUser(r); err != nil || user == nil || !user.Admin {
		t.Fatalf("existing administrator's new session denied: %v", err)
	}
	stored, err := store.Account(ctx, "admin")
	if err != nil || !bytes.Equal(stored.Salt, a.Salt) || !bytes.Equal(stored.Hash, a.Hash) || stored.HashVersion != a.HashVersion {
		t.Fatal("login changed existing administrator credentials")
	}
}

func TestAdministratorProvisioningCannotElevateExistingAccount(t *testing.T) {
	h, store := testHandler()
	ctx := context.Background()
	existing := newAccount("admin", "Existing member", "existing member password")
	if err := store.CreateAccount(ctx, existing); err != nil {
		t.Fatal(err)
	}
	admin, err := NewAdministrator("test administrator password")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(ctx, admin); !errors.Is(err, ErrConflict) {
		t.Fatal("bootstrap must not elevate an existing account")
	}
	stored, err := store.Account(ctx, "admin")
	if err != nil || stored.User != existing.User || !bytes.Equal(stored.Salt, existing.Salt) || !bytes.Equal(stored.Hash, existing.Hash) || stored.HashVersion != existing.HashVersion {
		t.Fatal("bootstrap modified an existing non-administrator")
	}
	response, body := call(t, h, "POST", "/api/v1/auth/login", `{"username":"admin","password":"existing member password"}`, nil, nil)
	if response.StatusCode != http.StatusOK || len(response.Cookies()) != 1 {
		t.Fatalf("existing member login: %d %s", response.StatusCode, body)
	}
	r := httptest.NewRequest(http.MethodGet, "https://rangle.test/api/v1/generation/jobs", nil)
	r.AddCookie(response.Cookies()[0])
	if _, err := h.AdminUser(r); !errors.Is(err, ErrForbidden) {
		t.Fatal("reserved account ID gained administrator privileges")
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
}
