package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/generation"
	"github.com/Hosi121/rangle/internal/publisher"
)

type publisherReadJobs struct {
	generation.Store
	job   generation.Job
	lists int
}

func (s *publisherReadJobs) Get(_ context.Context, id string) (generation.Job, error) {
	if id != s.job.ID {
		return generation.Job{}, generation.ErrNotFound
	}
	return s.job, nil
}
func (s *publisherReadJobs) List(_ context.Context, owner string) ([]generation.Job, error) {
	s.lists++
	if owner != s.job.Owner {
		return []generation.Job{}, nil
	}
	return []generation.Job{s.job}, nil
}

type publisherLifecycleStore struct {
	*community.MemoryStore
	status publisher.Status
}

func (s *publisherLifecycleStore) Publisher(ctx context.Context, id publisher.ID) (publisher.Account, error) {
	a, err := s.MemoryStore.Publisher(ctx, id)
	if s.status != "" {
		a.Status = s.status
	}
	return a, err
}

func TestApplicationPublisherGenerationUsesCurrentStaffAndRetainsJobs(t *testing.T) {
	ctx := context.Background()
	store := &publisherLifecycleStore{MemoryStore: community.NewMemoryStore()}
	operator, err := community.NewAdministrator("a long operator password 2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateAccount(ctx, operator); err != nil {
		t.Fatal(err)
	}
	settings := generation.Config{
		Project: "comin-up", ProjectNumber: "113704194486", Database: "rangle-stg-generation",
		Image:          "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/trainer@sha256:" + strings.Repeat("a", 64),
		ServiceAccount: "rangle-stg-trainer@comin-up.iam.gserviceaccount.com", OutputBucket: "rangle-stg-media", MediaOrigin: "https://media.rangle.test",
		Deadline: time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC), Attempts: 10,
		Presets: []generation.Preset{{ID: "reviewed", Title: "Reviewed sample", Sport: "basketball", Description: "Reviewed input", InputURI: "gs://inputs/datasets/sample.npz", InputSHA256: strings.Repeat("b", 64), PreviewURL: "https://media.rangle.test/preview.webm", Source: "https://source.rangle.test/sample", License: "CC BY 4.0", PublisherAccounts: []publisher.ID{"club"}}},
	}
	jobs := &publisherReadJobs{job: generation.Job{ID: strings.Repeat("a", 32), Owner: "publisher:club", PublisherAccountID: "club", State: generation.Succeeded, Snapshot: settings.Presets[0], CreatedBy: generation.Actor{Kind: "staff", Generation: 1}}}
	service := &generation.Service{Config: settings, Store: jobs, Provider: struct{ generation.Provider }{}}
	app := cheerApplication(t, store, service)
	request := func(method, path, body string, cookie *http.Cookie, want int) []byte {
		t.Helper()
		w := cheerRequest(app, method, path, body, cookie, map[string]string{"X-Rangle-Owner": "admin", "X-Rangle-Principal": "forged", "X-Admin": "true"})
		r := w.Result()
		defer r.Body.Close()
		data, _ := io.ReadAll(r.Body)
		if r.StatusCode != want || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("%s %s: %d %s", method, path, r.StatusCode, data)
		}
		return data
	}
	login := func(path, body string) *http.Cookie {
		t.Helper()
		r := cheerRequest(app, "POST", path, body, nil, nil).Result()
		defer r.Body.Close()
		if r.StatusCode != 200 || len(r.Cookies()) != 1 {
			t.Fatal("login", r.StatusCode)
		}
		return r.Cookies()[0]
	}
	opCookie := login("/api/v1/auth/login", `{"username":"admin","password":"a long operator password 2026"}`)
	request("POST", "/api/v1/operations/publishers", `{"id":"club","name":"Club","kind":"organization","staff_username":"manager","staff_password":"a long staff password 2026"}`, opCookie, 201)
	managerCookie := login("/api/v1/publisher-auth/login", `{"account_id":"club","username":"manager","password":"a long staff password 2026"}`)
	request("POST", "/api/v1/publishers/club/staff", `{"username":"editor","password":"a long staff password 2026","role":"editor"}`, managerCookie, 201)
	editorCookie := login("/api/v1/publisher-auth/login", `{"account_id":"club","username":"editor","password":"a long staff password 2026"}`)
	editor, err := store.StaffByUsername(ctx, "club", "editor")
	if err != nil {
		t.Fatal(err)
	}
	jobs.job.CreatedBy.ID = string(editor.Staff.ID)
	root := "/api/v1/publishers/club/generation"
	for _, cookie := range []*http.Cookie{nil, opCookie} {
		request("GET", root+"/jobs", "", cookie, 401)
	}
	request("GET", "/api/v1/generation/jobs", "", editorCookie, 401)
	request("GET", "/api/v1/publishers/other/generation/jobs", "", editorCookie, 404)
	if jobs.lists != 0 {
		t.Fatal("unauthorized request reached generation storage")
	}
	data := request("GET", root+"/presets", "", editorCookie, 200)
	if strings.Contains(string(data), "gs://") || strings.Contains(string(data), "publisher_accounts") {
		t.Fatal("private input leaked")
	}
	request("GET", root+"/jobs/"+jobs.job.ID, "", editorCookie, 200)
	data = request("GET", root+"/jobs", "", editorCookie, 200)
	var listed struct{ Jobs []generation.Job }
	if json.Unmarshal(data, &listed) != nil || len(listed.Jobs) != 1 || listed.Jobs[0].ID != jobs.job.ID {
		t.Fatal("organization list", string(data))
	}
	request("PUT", "/api/v1/operations/publishers/club/staff/"+string(editor.Staff.ID), `{"role":"editor","disabled":true}`, opCookie, 200)
	request("GET", root+"/jobs/"+jobs.job.ID, "", editorCookie, 401)
	// Offboarding revokes the old session; another enabled staff member still
	// sees the organization's job, without rewriting the immutable creator.
	request("GET", root+"/jobs/"+jobs.job.ID, "", managerCookie, 200)
	if jobs.job.CreatedBy.ID != string(editor.Staff.ID) {
		t.Fatal("offboarding changed creator")
	}
	store.status = publisher.Suspended
	request("GET", root+"/jobs/"+jobs.job.ID, "", managerCookie, 401)
	if jobs.lists != 1 {
		t.Fatal("revoked session reached generation storage")
	}
}
