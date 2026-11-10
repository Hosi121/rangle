package community

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

func staffLoginRequest(t *testing.T, h *Handler, org, name, password string, previous *http.Cookie) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"account_id": org, "username": name, "password": password})
	return call(t, h, "POST", "/api/v1/publisher-auth/login", string(b), previous, nil)
}
func staffCookie(t *testing.T, h *Handler, org string) *http.Cookie {
	t.Helper()
	r, data := staffLoginRequest(t, h, org, "manager", "a long staff password 2026", nil)
	if r.StatusCode != 200 || len(r.Cookies()) != 1 {
		t.Fatalf("staff login: %d %s", r.StatusCode, data)
	}
	return r.Cookies()[0]
}
func operatorCookie(t *testing.T, s *MemoryStore) *http.Cookie {
	t.Helper()
	a := newAccount("admin", "Operator", "a long operator password 2026")
	a.User.Admin = true
	if err := s.CreateAccount(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("o", 32)))
	if err := s.PutSession(context.Background(), sessionKey(token), Session{User: a.User, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: cookieName, Value: token}
}
func TestStaffAuthenticationIsOrganizationScopedAndPrivate(t *testing.T) {
	h, s := testHandler()
	a, staff := publisherFixture(t, "club-a")
	if err := s.ProvisionPublisher(context.Background(), a, staff); err != nil {
		t.Fatal(err)
	}
	b, other := publisherFixture(t, "club-b")
	other.Staff.ID = staff.Staff.ID
	otherPassword := newAccount("manager", "", "a different staff password 2026")
	other.Salt, other.Hash = otherPassword.Salt, otherPassword.Hash
	if err := s.ProvisionPublisher(context.Background(), b, other); err != nil {
		t.Fatal(err)
	}
	cookie := staffCookie(t, h, "club-a")
	if cookie.Name != staffCookieName || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("unsafe staff cookie", cookie)
	}
	r, data := call(t, h, "GET", "/api/v1/publisher-auth/me", "", cookie, nil)
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(data), `"id":"club-a"`) {
		t.Fatal(r.StatusCode, string(data))
	}
	for _, secret := range []string{"password", "salt", "hash", "version", "status", "is_admin"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("private field leaked", secret)
		}
	}
	if _, ok := s.staffSessions[cookie.Value]; ok {
		t.Fatal("staff bearer stored in plaintext")
	}
	for _, attempt := range []struct{ org, password string }{{"club-b", "a long staff password 2026"}, {"missing", "a long staff password 2026"}, {"club-a", "incorrect"}} {
		r, data := staffLoginRequest(t, h, attempt.org, "manager", attempt.password, nil)
		if r.StatusCode != 401 || !strings.Contains(string(data), "invalid_credentials") {
			t.Fatal("credential/account enumeration", r.StatusCode, string(data))
		}
	}
	r, data = staffLoginRequest(t, h, "club-b", "manager", "a different staff password 2026", nil)
	if r.StatusCode != 200 || !strings.Contains(string(data), `"id":"club-b"`) {
		t.Fatal("local credentials not isolated", r.StatusCode, string(data))
	}
	// Renaming the cookie cannot cross the two session collections.
	copied := &http.Cookie{Name: cookieName, Value: cookie.Value}
	_, data = call(t, h, "GET", "/api/v1/auth/me", "", copied, nil)
	if !strings.Contains(string(data), `"user":null`) {
		t.Fatal("staff impersonated a viewer", string(data))
	}
	request := httptest.NewRequest("GET", "https://rangle.test/", nil)
	request.AddCookie(cookie)
	if user, err := h.AdminUser(request); err != nil || user != nil {
		t.Fatal("organization admin became a platform operator", user, err)
	}
}
func TestStaffSessionsRefreshPrivilegesAndCannotReviveAfterOffboarding(t *testing.T) {
	h, s := testHandler()
	a, staff := publisherFixture(t, "club")
	_ = s.ProvisionPublisher(context.Background(), a, staff)
	cookie := staffCookie(t, h, "club")
	row := s.staff[a.ID][staff.Staff.ID]
	row.Staff.Role = publisher.Editor
	s.staff[a.ID][staff.Staff.ID] = row
	request := httptest.NewRequest("GET", "https://rangle.test/", nil)
	request.AddCookie(cookie)
	current, err := h.CurrentStaff(request)
	if err != nil || current == nil || current.Record.Staff.Role != publisher.Editor {
		t.Fatal("cached role used", current, err)
	}
	row.Staff.Disabled = true
	row.Version++
	s.staff[a.ID][staff.Staff.ID] = row
	if current, _ := h.CurrentStaff(request); current != nil {
		t.Fatal("disabled staff remained logged in")
	}
	row.Staff.Disabled = false
	s.staff[a.ID][staff.Staff.ID] = row
	if current, _ := h.CurrentStaff(request); current != nil {
		t.Fatal("old login revived after offboarding")
	}
	cookie = staffCookie(t, h, "club")
	previous := cookie.Value
	r, _ := staffLoginRequest(t, h, "club", "manager", "a long staff password 2026", cookie)
	cookie = r.Cookies()[0]
	if cookie.Value == previous {
		t.Fatal("login did not rotate staff token")
	}
	if _, ok := s.staffSessions[sessionKey(previous)]; ok {
		t.Fatal("previous staff session remained after rotation")
	}
	r, _ = call(t, h, "POST", "/api/v1/publisher-auth/logout", "", cookie, nil)
	if r.StatusCode != 204 || r.Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not clear staff cookie")
	}
	_, data := call(t, h, "GET", "/api/v1/publisher-auth/me", "", cookie, nil)
	if !strings.Contains(string(data), `"identity":null`) {
		t.Fatal("logout replay accepted")
	}
	cookie = staffCookie(t, h, "club")
	session := s.staffSessions[sessionKey(cookie.Value)]
	session.ExpiresAt = time.Now().Add(-time.Second)
	s.staffSessions[sessionKey(cookie.Value)] = session
	_, data = call(t, h, "GET", "/api/v1/publisher-auth/me", "", cookie, nil)
	if !strings.Contains(string(data), `"identity":null`) {
		t.Fatal("expired session accepted before TTL cleanup")
	}
}
func TestOnlyRangleOperatorCanBootstrapPublisher(t *testing.T) {
	h, s := testHandler()
	operator := operatorCookie(t, s)
	viewer := registerUser(t, h, "viewer")
	body := `{"id":"club","name":"Example Club","kind":"organization","staff_username":"manager","staff_password":"a long staff password 2026"}`
	for _, test := range []struct {
		cookie  *http.Cookie
		headers map[string]string
		status  int
	}{{nil, nil, 401}, {viewer, nil, 403}, {operator, map[string]string{"Origin": "https://other.test"}, 403}} {
		r, _ := call(t, h, "POST", "/api/v1/operations/publishers", body, test.cookie, test.headers)
		if r.StatusCode != test.status {
			t.Fatal("operator boundary", r.StatusCode, test.status)
		}
	}
	r, data := call(t, h, "POST", "/api/v1/operations/publishers", body, operator, nil)
	if r.StatusCode != 201 || !strings.Contains(string(data), `"role":"administrator"`) || strings.Contains(string(data), "password") {
		t.Fatal("bootstrap", r.StatusCode, string(data))
	}
	r, _ = call(t, h, "POST", "/api/v1/operations/publishers", body, operator, nil)
	if r.StatusCode != 409 {
		t.Fatal("existing organization overwritten")
	}
	cookie := staffCookie(t, h, "club")
	r, _ = call(t, h, "POST", "/api/v1/operations/publishers", strings.Replace(body, `"club"`, `"another"`, 1), cookie, nil)
	if r.StatusCode != 401 {
		t.Fatal("staff cookie granted operator permission", r.StatusCode)
	}
	row := s.accounts["admin"]
	row.User.Admin = false
	s.accounts["admin"] = row
	r, _ = call(t, h, "POST", "/api/v1/operations/publishers", body, operator, nil)
	if r.StatusCode != 403 {
		t.Fatal("stale operator session privilege used")
	}
}
func TestStaffLoginRejectsCrossOriginUnknownFieldsAndSuspendedAccount(t *testing.T) {
	h, s := testHandler()
	a, staff := publisherFixture(t, "club")
	_ = s.ProvisionPublisher(context.Background(), a, staff)
	body := `{"account_id":"club","username":"manager","password":"a long staff password 2026"}`
	r, _ := call(t, h, "POST", "/api/v1/publisher-auth/login", body, nil, map[string]string{"Origin": "https://other.test"})
	if r.StatusCode != 403 {
		t.Fatal("cross-origin staff login accepted")
	}
	r, _ = call(t, h, "POST", "/api/v1/publisher-auth/login", strings.TrimSuffix(body, "}")+`,"is_admin":true}`, nil, nil)
	if r.StatusCode != 400 {
		t.Fatal("caller-supplied role accepted")
	}
	a.Status = publisher.Suspended
	s.publishers[a.ID] = a
	r, _ = staffLoginRequest(t, h, "club", "manager", "a long staff password 2026", nil)
	if r.StatusCode != 401 {
		t.Fatal("suspended organization login accepted")
	}
	data, _ := json.Marshal(staff)
	if string(data) != "{}" {
		t.Fatal("private credentials can be serialized", fmt.Sprint(data))
	}
}
