package main

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/Hosi121/rangle/internal/community"
)

func TestApplicationRoutesOrganizationStaffManagement(t *testing.T) {
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
	if login.StatusCode != 200 {
		t.Fatal(login.StatusCode)
	}
	opCookie := login.Cookies()[0]
	body := `{"id":"club","name":"Club","kind":"organization","staff_username":"manager","staff_password":"a long staff password 2026"}`
	provision := cheerRequest(app, "POST", "/api/v1/operations/publishers", body, opCookie, nil).Result()
	defer provision.Body.Close()
	if provision.StatusCode != 201 {
		t.Fatal(provision.StatusCode)
	}
	staff := cheerRequest(app, "POST", "/api/v1/publisher-auth/login", `{"account_id":"club","username":"manager","password":"a long staff password 2026"}`, nil, nil).Result()
	defer staff.Body.Close()
	if staff.StatusCode != 200 {
		t.Fatal(staff.StatusCode)
	}
	cookie := staff.Cookies()[0]
	root := "/api/v1/publishers/club/staff"
	create := cheerRequest(app, "POST", root, `{"username":"editor","password":"a long staff password 2026","role":"editor"}`, cookie, nil).Result()
	defer create.Body.Close()
	var created struct {
		Staff struct {
			ID string `json:"id"`
		} `json:"staff"`
	}
	if create.StatusCode != 201 || json.NewDecoder(create.Body).Decode(&created) != nil || created.Staff.ID == "" {
		t.Fatal("create staff route", create.StatusCode)
	}
	for _, op := range []struct {
		method, path, body string
		status             int
	}{
		{"GET", root, "", 200}, {"HEAD", root, "", 200},
		{"PUT", root + "/" + created.Staff.ID, `{"role":"editor","disabled":true}`, 200},
		{"GET", "/api/v1/operations/publishers/club/staff", "", 401},
	} {
		result := cheerRequest(app, op.method, op.path, op.body, cookie, nil).Result()
		data, _ := io.ReadAll(result.Body)
		result.Body.Close()
		if result.StatusCode != op.status || result.Header.Get("Cache-Control") != "no-store" || op.method == "HEAD" && len(data) != 0 {
			t.Fatal(op.path, result.StatusCode, string(data))
		}
	}
	recovery := cheerRequest(app, "POST", "/api/v1/operations/publishers/club/staff", `{"username":"recovery","password":"a long staff password 2026","role":"administrator"}`, opCookie, nil).Result()
	defer recovery.Body.Close()
	if recovery.StatusCode != 201 {
		t.Fatal("operator recovery route", recovery.StatusCode)
	}
}
