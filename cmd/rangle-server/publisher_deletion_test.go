package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/generation"
	"github.com/Hosi121/rangle/internal/publisher"
)

type cleanupManagerStub struct {
	fail     bool
	rid      string
	calls    int
	complete bool
}

func (m *cleanupManagerStub) PublisherDeletionStatus(context.Context, string, publisher.ID) (generation.PublisherLifecycle, error) {
	if m.fail {
		return generation.PublisherLifecycle{}, errors.New("disabled")
	}
	return generation.PublisherLifecycle{Account: "club", State: publisher.Active}, nil
}
func (m *cleanupManagerStub) StartPublisherDeletion(_ context.Context, _ string, id publisher.ID, rid string) (generation.PublisherLifecycle, error) {
	m.calls++
	if m.rid != "" && m.rid != rid {
		return generation.PublisherLifecycle{}, errors.New("request changed")
	}
	m.rid = rid
	d := generation.PublisherLifecycle{Account: id, State: publisher.Deleting, RequestID: rid, Stage: "objects", Jobs: []string{strings.Repeat("a", 32)}}
	if m.complete {
		d.State = publisher.Deleted
		d.Stage = "complete"
		d.Prefix = 3
	}
	return d, nil
}
func TestPublisherDeletionCoordinatorResumesAndFinalizesOnlyConfirmedCleanup(t *testing.T) {
	s := community.NewMemoryStore()
	op, err := community.NewAdministrator("a long operator password 2026")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAccount(context.Background(), op); err != nil {
		t.Fatal(err)
	}
	app := cheerApplication(t, s, nil)
	login := cheerRequest(app, "POST", "/api/v1/auth/login", `{"username":"admin","password":"a long operator password 2026"}`, nil, nil).Result()
	defer login.Body.Close()
	provision := cheerRequest(app, "POST", "/api/v1/operations/publishers", `{"id":"club","name":"Club","kind":"organization","staff_username":"manager","staff_password":"a long staff password 2026"}`, login.Cookies()[0], nil).Result()
	provision.Body.Close()
	if provision.StatusCode != 201 {
		t.Fatal(provision.StatusCode)
	}
	m := &cleanupManagerStub{fail: true}
	h := publisherDeletionHandler(s, m, func(*http.Request) (string, error) { return "admin", nil }, nil)
	path := "/api/v1/operations/publishers/club/deletion"
	request := func(method, body string, headers map[string]string) int {
		t.Helper()
		result := cheerRequest(h, method, path, body, nil, headers).Result()
		defer result.Body.Close()
		_, _ = io.Copy(io.Discard, result.Body)
		if result.Header.Get("Cache-Control") != "no-store" {
			t.Error("cacheable deletion response")
		}
		return result.StatusCode
	}
	if code := request("DELETE", `{"confirmation":"wrong"}`, nil); code != 400 {
		t.Fatal(code)
	}
	if code := request("DELETE", `{"confirmation":"club"}`, nil); code != 503 {
		t.Fatal(code)
	}
	a, _ := s.Publisher(context.Background(), "club")
	if a.Status != publisher.Active {
		t.Fatal("disabled cleanup stranded organization")
	}
	m.fail = false
	if code := request("DELETE", `{"confirmation":"club"}`, nil); code != 200 {
		t.Fatal(code)
	}
	a, _ = s.Publisher(context.Background(), "club")
	if a.Status != publisher.Deleting {
		t.Fatal("deleted before cleanup")
	}
	if code := request("GET", "", nil); code != 200 || m.calls != 2 {
		t.Fatal("retry did not resume", code, m.calls)
	}
	m.complete = true
	if code := request("GET", "", nil); code != 200 {
		t.Fatal(code)
	}
	a, _ = s.Publisher(context.Background(), "club")
	if a.Status != publisher.Deleted {
		t.Fatal("cleanup did not finalize")
	}
	markers, err := s.DeletedClips(context.Background(), []string{"generated-" + strings.Repeat("a", 32)})
	if err != nil || len(markers) != 1 {
		t.Fatal(markers, err)
	}
}
func TestPublisherDeletionCoordinatorRejectsViewerAndCrossOrigin(t *testing.T) {
	path := "/api/v1/operations/publishers/club/deletion"
	for _, tc := range []struct {
		actor string
		err   error
		want  int
	}{{"", nil, 401}, {"", generation.ErrForbidden, 403}} {
		h := publisherDeletionHandler(community.NewMemoryStore(), &cleanupManagerStub{}, func(*http.Request) (string, error) { return tc.actor, tc.err }, nil)
		r := cheerRequest(h, "GET", path, "", nil, nil).Result()
		r.Body.Close()
		if r.StatusCode != tc.want {
			t.Fatal(r.StatusCode)
		}
	}
	h := publisherDeletionHandler(community.NewMemoryStore(), &cleanupManagerStub{}, func(*http.Request) (string, error) { t.Error("CSRF reached authorization"); return "admin", nil }, nil)
	r := cheerRequest(h, "DELETE", path, `{"confirmation":"club"}`, nil, map[string]string{"Origin": "https://foreign.test", "Sec-Fetch-Site": "cross-site"}).Result()
	defer r.Body.Close()
	if r.StatusCode != 403 {
		t.Fatal(r.StatusCode)
	}
}
