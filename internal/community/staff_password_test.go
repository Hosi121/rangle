package community

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

const replacementStaffPassword = "a replacement staff password 2026"

func TestStaffPasswordResetRevokesSessionsAndPreservesRole(t *testing.T) {
	h, s, a, _, managerCookie := managedFixture(t)
	response, data := call(t, h, "POST", "/api/v1/publishers/club-a/staff", staffBody("editor", publisher.Editor), managerCookie, nil)
	if response.StatusCode != 201 {
		t.Fatal(response.StatusCode, string(data))
	}
	id := staffFromResponse(t, data).ID
	login, _ := staffLoginRequest(t, h, string(a.ID), "editor", "a long staff password 2026", nil)
	oldCookie := login.Cookies()[0]
	path := "/api/v1/publishers/club-a/staff/" + string(id) + "/password"
	response, data = call(t, h, "PUT", path, `{"password":"`+replacementStaffPassword+`"}`, managerCookie, nil)
	if response.StatusCode != 204 || len(data) != 0 || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(response.StatusCode, string(data))
	}
	_, data = call(t, h, "GET", "/api/v1/publisher-auth/me", "", oldCookie, nil)
	if !strings.Contains(string(data), `"identity":null`) {
		t.Fatal("old session survived reset")
	}
	response, _ = staffLoginRequest(t, h, string(a.ID), "editor", "a long staff password 2026", nil)
	if response.StatusCode != 401 {
		t.Fatal("old password survived reset")
	}
	response, data = staffLoginRequest(t, h, string(a.ID), "editor", replacementStaffPassword, nil)
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode, string(data))
	}
	row, _ := s.Staff(context.Background(), a.ID, id)
	if row.Version != 2 || row.Staff.Role != publisher.Editor || row.Staff.Disabled {
		t.Fatal("reset changed staff privileges")
	}
	for _, secret := range []string{replacementStaffPassword, "salt", "hash", "version"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("private credentials leaked", secret)
		}
	}
}

func TestStaffCanChangeOwnPasswordWithReauthentication(t *testing.T) {
	h, _, a, _, cookie := managedFixture(t)
	second := staffCookie(t, h, string(a.ID))
	path := "/api/v1/publisher-auth/password"
	response, _ := call(t, h, "PUT", path, `{"current_password":"wrong","new_password":"`+replacementStaffPassword+`"}`, cookie, nil)
	if response.StatusCode != 401 {
		t.Fatal("wrong current password accepted")
	}
	response, data := call(t, h, "PUT", path, `{"current_password":"a long staff password 2026","new_password":"`+replacementStaffPassword+`"}`, cookie, nil)
	if response.StatusCode != 204 || len(data) != 0 || len(response.Cookies()) != 1 || response.Cookies()[0].MaxAge != -1 {
		t.Fatal("password change did not expire local cookie", response.StatusCode, string(data))
	}
	for _, old := range []*http.Cookie{cookie, second} {
		_, data = call(t, h, "GET", "/api/v1/publisher-auth/me", "", old, nil)
		if !strings.Contains(string(data), `"identity":null`) {
			t.Fatal("other session survived password change")
		}
	}
	response, data = staffLoginRequest(t, h, string(a.ID), "manager", replacementStaffPassword, nil)
	if response.StatusCode != 200 {
		t.Fatal("changed password cannot sign in", response.StatusCode, string(data))
	}
}

func TestStaffPasswordEndpointsRejectForeignAuthorityAndMalformedInput(t *testing.T) {
	for _, tc := range []struct {
		name, account, body, origin string
		status                      int
	}{
		{"foreign", "club-b", `{"password":"` + replacementStaffPassword + `"}`, "", 404},
		{"weak", "club-a", `{"password":"short"}`, "", 400},
		{"extra fields", "club-a", `{"password":"` + replacementStaffPassword + `","role":"administrator"}`, "", 400},
		{"cross origin", "club-a", `{"password":"` + replacementStaffPassword + `"}`, "https://other.test", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, _, _, row, cookie := managedFixture(t)
			path := "/api/v1/publishers/" + tc.account + "/staff/" + string(row.Staff.ID) + "/password"
			headers := map[string]string{"Origin": tc.origin}
			response, data := call(t, h, "PUT", path, tc.body, cookie, headers)
			if response.StatusCode != tc.status {
				t.Fatal(response.StatusCode, string(data))
			}
		})
	}
	h, s, a, admin, cookie := managedFixture(t)
	_, data := call(t, h, "POST", "/api/v1/publishers/club-a/staff", staffBody("editor", publisher.Editor), cookie, nil)
	editor := staffFromResponse(t, data)
	login, _ := staffLoginRequest(t, h, string(a.ID), "editor", "a long staff password 2026", nil)
	path := "/api/v1/publishers/club-a/staff/" + string(admin.Staff.ID) + "/password"
	response, _ := call(t, h, "PUT", path, `{"password":"`+replacementStaffPassword+`"}`, login.Cookies()[0], nil)
	if response.StatusCode != 403 {
		t.Fatal("editor reset another staff password")
	}
	operator := operatorCookie(t, s)
	response, _ = call(t, h, "PUT", path, `{"password":"`+replacementStaffPassword+`"}`, operator, nil)
	if response.StatusCode != 401 {
		t.Fatal("operator cookie implicitly became staff")
	}
	response, _ = call(t, h, "PUT", "/api/v1/operations/publishers/club-a/staff/"+string(editor.ID)+"/password", `{"password":"`+replacementStaffPassword+`"}`, operator, nil)
	if response.StatusCode != 204 {
		t.Fatal("operator recovery failed", response.StatusCode)
	}
}

type passwordContractStore interface {
	StaffPasswordStore
	StaffManagementStore
}

func staffPasswordStoreContract(t *testing.T, s passwordContractStore) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	a, admin := publisherFixture(t, "password-a")
	if err := s.ProvisionPublisher(ctx, a, admin); err != nil {
		t.Fatal(err)
	}
	m := StaffManager{Principal: publisher.StaffPrincipal{PublisherAccountID: a.ID, StaffAccountID: admin.Staff.ID}, Version: admin.Version}
	editor := admin.clone()
	editor.Staff.ID = "editor"
	editor.Staff.Username = "editor"
	editor.Staff.Role = publisher.Editor
	if err := s.CreateStaff(ctx, m, a.ID, editor); err != nil {
		t.Fatal(err)
	}
	other, otherAdmin := publisherFixture(t, "password-b")
	if err := s.ProvisionPublisher(ctx, other, otherAdmin); err != nil {
		t.Fatal(err)
	}
	password, err := newStaffPassword(replacementStaffPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetStaffPassword(ctx, m, other.ID, otherAdmin.Staff.ID, password); !errors.Is(err, ErrForbidden) {
		t.Fatal("cross tenant password mutation", err)
	}
	row, err := s.ResetStaffPassword(ctx, m, a.ID, editor.Staff.ID, password)
	if err != nil || row.Version != 2 || row.Staff != editor.Staff || !checkPassword(staffPassword(row), replacementStaffPassword) {
		t.Fatal("password reset", err)
	}
	actor := publisher.StaffPrincipal{PublisherAccountID: a.ID, StaffAccountID: editor.Staff.ID}
	if _, err := s.ChangeStaffPassword(ctx, actor, 1, password); !errors.Is(err, ErrForbidden) {
		t.Fatal("stale proof committed password", err)
	}
	var succeeded atomic.Int32
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			_, err := s.ChangeStaffPassword(ctx, actor, 2, password)
			if err == nil {
				succeeded.Add(1)
			} else if !errors.Is(err, ErrForbidden) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if succeeded.Load() != 1 {
		t.Fatal("two concurrent changes accepted the same credential generation")
	}
	row, err = s.UpdateStaff(ctx, m, a.ID, editor.Staff.ID, publisher.Editor, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeStaffPassword(ctx, actor, row.Version, password); !errors.Is(err, ErrForbidden) {
		t.Fatal("disabled staff changed password", err)
	}
	row, err = s.ResetStaffPassword(ctx, m, a.ID, editor.Staff.ID, password)
	if err != nil || !row.Staff.Disabled || row.Staff.Role != publisher.Editor {
		t.Fatal("reset enabled or promoted disabled staff", err)
	}
	if _, err := s.UpdateStaff(ctx, m, a.ID, admin.Staff.ID, publisher.Administrator, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetStaffPassword(ctx, m, a.ID, editor.Staff.ID, password); !errors.Is(err, ErrForbidden) {
		t.Fatal("offboarded administrator reset password", err)
	}
}
func TestMemoryStaffPasswordContract(t *testing.T) { staffPasswordStoreContract(t, NewMemoryStore()) }
func TestFirestoreStaffPasswordContract(t *testing.T) {
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires local emulator")
	}
	s, err := NewFirestoreStore(context.Background(), fmt.Sprintf("demo-password-%x", time.Now().UnixNano()), "community")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	staffPasswordStoreContract(t, s)
}
