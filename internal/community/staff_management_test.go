package community

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/publisher"
)

func managedFixture(t *testing.T) (*Handler, *MemoryStore, publisher.Account, StaffRecord, *http.Cookie) {
	t.Helper()
	h, s := testHandler()
	a, admin := publisherFixture(t, "club-a")
	if err := s.ProvisionPublisher(context.Background(), a, admin); err != nil {
		t.Fatal(err)
	}
	return h, s, a, admin, staffCookie(t, h, string(a.ID))
}
func staffBody(name string, role publisher.Role) string {
	return fmt.Sprintf(`{"username":%q,"password":"a long staff password 2026","role":%q}`, name, role)
}
func staffFromResponse(t *testing.T, data []byte) staffView {
	t.Helper()
	var result struct {
		Staff staffView `json:"staff"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	return result.Staff
}
func TestStaffManagementAPIIsTenantScopedAndDoesNotExposeCredentials(t *testing.T) {
	h, s, a, _, cookie := managedFixture(t)
	path := "/api/v1/publishers/club-a/staff"
	b, other := publisherFixture(t, "club-b")
	_ = s.ProvisionPublisher(context.Background(), b, other)
	r, data := call(t, h, "POST", path, staffBody("editor", publisher.Editor), cookie, nil)
	if r.StatusCode != 201 {
		t.Fatal("create staff", r.StatusCode, string(data))
	}
	editor := staffFromResponse(t, data)
	r, data = call(t, h, "GET", path, "", cookie, nil)
	var page struct {
		Staff  []staffView `json:"staff"`
		Cursor string      `json:"next_cursor"`
	}
	if r.StatusCode != 200 || r.Header.Get("Cache-Control") != "no-store" || json.Unmarshal(data, &page) != nil || len(page.Staff) != 2 {
		t.Fatal("staff list", r.StatusCode, string(data))
	}
	for _, secret := range []string{"password", "salt", "hash", "version", "is_admin"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("private data in list", secret)
		}
	}
	login, _ := staffLoginRequest(t, h, "club-a", "editor", "a long staff password 2026", nil)
	editorCookie := login.Cookies()[0]
	for _, attempt := range []struct {
		method, path, body string
		cookie             *http.Cookie
		status             int
	}{
		{"GET", path, "", editorCookie, 403},
		{"POST", path, staffBody("escalated", publisher.Administrator), editorCookie, 403},
		{"PUT", path + "/" + string(editor.ID), `{"role":"administrator","disabled":false}`, editorCookie, 403},
		{"GET", "/api/v1/publishers/club-b/staff", "", cookie, 404},
		{"POST", "/api/v1/publishers/club-b/staff", staffBody("foreign", publisher.Editor), cookie, 404},
		{"POST", path, strings.TrimSuffix(staffBody("bad", publisher.Editor), "}") + `,"account_id":"club-b"}`, cookie, 400},
		{"PUT", path + "/" + string(editor.ID), `{"role":"administrator"}`, cookie, 400},
		{"POST", path, staffBody("csrf", publisher.Editor), cookie, 403},
	} {
		var headers map[string]string
		if strings.Contains(attempt.body, `"csrf"`) {
			headers = map[string]string{"Origin": "https://other.test"}
		}
		r, data := call(t, h, attempt.method, attempt.path, attempt.body, attempt.cookie, headers)
		if r.StatusCode != attempt.status {
			t.Fatalf("%s %s: %d %s", attempt.method, attempt.path, r.StatusCode, data)
		}
	}
	if _, err := s.StaffByUsername(context.Background(), a.ID, "escalated"); !errors.Is(err, ErrNotFound) {
		t.Fatal("editor created a privileged account")
	}
	operator := operatorCookie(t, s)
	r, _ = call(t, h, "GET", path, "", operator, nil)
	if r.StatusCode != 401 {
		t.Fatal("operator cookie implicitly became tenant staff", r.StatusCode)
	}
	r, _ = call(t, h, "GET", "/api/v1/operations/publishers/club-a/staff", "", editorCookie, nil)
	if r.StatusCode != 401 {
		t.Fatal("tenant staff implicitly became operator", r.StatusCode)
	}
}
func TestOffboardingInvalidatesSessionsAndOperatorCanRecoverLastAdministrator(t *testing.T) {
	h, s, a, admin, cookie := managedFixture(t)
	operator := operatorCookie(t, s)
	path := "/api/v1/publishers/club-a/staff/" + string(admin.Staff.ID)
	r, data := call(t, h, "PUT", path, `{"role":"administrator","disabled":true}`, cookie, nil)
	if r.StatusCode != 200 || !staffFromResponse(t, data).Disabled {
		t.Fatal("offboard last administrator", r.StatusCode, string(data))
	}
	r, data = call(t, h, "GET", "/api/v1/publisher-auth/me", "", cookie, nil)
	if r.StatusCode != 200 || !strings.Contains(string(data), `"identity":null`) {
		t.Fatal("old staff session survived offboarding")
	}
	if current, err := s.Publisher(context.Background(), a.ID); err != nil || current != a {
		t.Fatal("organization changed on staff offboarding", err)
	}
	r, data = call(t, h, "POST", "/api/v1/operations/publishers/club-a/staff", staffBody("replacement", publisher.Administrator), operator, nil)
	if r.StatusCode != 201 {
		t.Fatal("operator recovery", r.StatusCode, string(data))
	}
	login, _ := staffLoginRequest(t, h, "club-a", "replacement", "a long staff password 2026", nil)
	r, data = call(t, h, "GET", "/api/v1/publishers/club-a/staff", "", login.Cookies()[0], nil)
	if r.StatusCode != 200 {
		t.Fatal("replacement could not manage organization", r.StatusCode, string(data))
	}
	// Re-enabling an existing staff ID must not resurrect its old session.
	r, _ = call(t, h, "PUT", "/api/v1/operations/publishers/club-a/staff/"+string(admin.Staff.ID), `{"role":"administrator","disabled":false}`, operator, nil)
	if r.StatusCode != 200 {
		t.Fatal("re-enable", r.StatusCode)
	}
	_, data = call(t, h, "GET", "/api/v1/publisher-auth/me", "", cookie, nil)
	if !strings.Contains(string(data), `"identity":null`) {
		t.Fatal("old session revived after re-enable")
	}
	fresh := staffCookie(t, h, "club-a")
	r, _ = call(t, h, "PUT", "/api/v1/operations/publishers/club-a/staff/"+string(admin.Staff.ID), `{"role":"administrator","disabled":false}`, operator, nil)
	if r.StatusCode != 200 {
		t.Fatal(r.StatusCode)
	}
	_, data = call(t, h, "GET", "/api/v1/publisher-auth/me", "", fresh, nil)
	if strings.Contains(string(data), `"identity":null`) {
		t.Fatal("idempotent update invalidated current login")
	}
}
func TestStaffCursorIsBoundToOrganization(t *testing.T) {
	h, s, a, admin, cookie := managedFixture(t)
	manager := StaffManager{Principal: publisher.StaffPrincipal{PublisherAccountID: a.ID, StaffAccountID: admin.Staff.ID}, Version: 1}
	for i := range 21 {
		row := admin.clone()
		row.Staff.ID = publisher.StaffID(fmt.Sprintf("member-%02d", i))
		row.Staff.Username = fmt.Sprintf("member_%02d", i)
		if err := s.CreateStaff(context.Background(), manager, a.ID, row); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/publishers/club-a/staff"
	_, data := call(t, h, "GET", path, "", cookie, nil)
	var page struct {
		Staff  []staffView `json:"staff"`
		Cursor string      `json:"next_cursor"`
	}
	if json.Unmarshal(data, &page) != nil || len(page.Staff) != 20 || page.Cursor == "" {
		t.Fatal("first page", string(data))
	}
	cursor := page.Cursor
	r, data := call(t, h, "GET", path+"?cursor="+cursor, "", cookie, nil)
	if r.StatusCode != 200 || json.Unmarshal(data, &page) != nil || len(page.Staff) != 2 || page.Cursor != "" {
		t.Fatal("continuation", r.StatusCode, string(data))
	}
	b, other := publisherFixture(t, "club-b")
	_ = s.ProvisionPublisher(context.Background(), b, other)
	otherCookie := staffCookie(t, h, "club-b")
	r, _ = call(t, h, "GET", "/api/v1/publishers/club-b/staff?cursor="+cursor, "", otherCookie, nil)
	if r.StatusCode != 400 {
		t.Fatal("foreign cursor accepted", r.StatusCode)
	}
}

type revokeBeforeCreate struct {
	*MemoryStore
	account publisher.ID
	staff   publisher.StaffID
}

func (s revokeBeforeCreate) CreateStaff(ctx context.Context, m StaffManager, a publisher.ID, row StaffRecord) error {
	s.mu.Lock()
	current := s.MemoryStore.staff[s.account][s.staff]
	current.Staff.Disabled = true
	current.Version++
	s.MemoryStore.staff[s.account][s.staff] = current
	s.mu.Unlock()
	return s.MemoryStore.CreateStaff(ctx, m, a, row)
}

func TestStaffMutationRechecksActorAfterRequestAuthorization(t *testing.T) {
	_, memory, a, admin, _ := managedFixture(t)
	store := revokeBeforeCreate{memory, a.ID, admin.Staff.ID}
	h := NewHandler(Config{Store: store})
	cookie := staffCookie(t, h, "club-a")
	r, data := call(t, h, "POST", "/api/v1/publishers/club-a/staff", staffBody("blocked", publisher.Editor), cookie, nil)
	if r.StatusCode != 403 {
		t.Fatal("revocation during request was ignored", r.StatusCode, string(data))
	}
	if _, err := memory.StaffByUsername(context.Background(), a.ID, "blocked"); !errors.Is(err, ErrNotFound) {
		t.Fatal("post-revocation mutation committed")
	}
}
