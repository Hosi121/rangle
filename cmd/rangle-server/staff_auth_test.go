package main

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/Hosi121/rangle/internal/community"
)

func TestApplicationRoutesOperatorProvisioningAndSeparateStaffAuth(t *testing.T) {
	store := community.NewMemoryStore()
	operator, err := community.NewAdministrator("a long operator password 2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(context.Background(), operator); err != nil {
		t.Fatal(err)
	}
	app := cheerApplication(t, store, nil)
	login := cheerRequest(app, "POST", "/api/v1/auth/login", `{"username":"admin","password":"a long operator password 2026"}`, nil, nil).Result()
	defer login.Body.Close()
	if login.StatusCode != 200 || len(login.Cookies()) != 1 {
		t.Fatal("operator login", login.StatusCode)
	}
	body := `{"id":"club","name":"Club","kind":"organization","staff_username":"manager","staff_password":"a long staff password 2026"}`
	provision := cheerRequest(app, "POST", "/api/v1/operations/publishers", body, login.Cookies()[0], nil).Result()
	defer provision.Body.Close()
	if provision.StatusCode != 201 {
		data, _ := io.ReadAll(provision.Body)
		t.Fatal("provision route", provision.StatusCode, string(data))
	}
	staff := cheerRequest(app, "POST", "/api/v1/publisher-auth/login", `{"account_id":"club","username":"manager","password":"a long staff password 2026"}`, nil, nil).Result()
	defer staff.Body.Close()
	if staff.StatusCode != 200 || len(staff.Cookies()) != 1 {
		t.Fatal("staff login route", staff.StatusCode)
	}
	for _, test := range []struct {
		method, path, body string
		status             int
	}{
		{http.MethodGet, "/api/v1/publisher-auth/me", "", 200},
		{http.MethodPut, "/api/v1/clips/demo/likes", "", 401},
		{http.MethodPost, "/api/v1/operations/publishers", body, 401},
		{http.MethodPost, "/api/v1/publisher-auth/logout", "", 204},
	} {
		result := cheerRequest(app, test.method, test.path, test.body, staff.Cookies()[0], nil).Result()
		data, _ := io.ReadAll(result.Body)
		result.Body.Close()
		if result.StatusCode != test.status || result.Header.Get("Cache-Control") != "no-store" {
			t.Fatal(test.path, result.StatusCode, string(data), result.Header)
		}
	}
}
