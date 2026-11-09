package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/community"
)

type previewAccountStore struct {
	community.Store
	mu                       sync.Mutex
	admin                    bool
	failSession, failAccount bool
	accountCalls             int
}

func (s *previewAccountStore) Account(ctx context.Context, id string) (community.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountCalls++
	if s.failAccount {
		return community.Account{}, errors.New("account storage unavailable")
	}
	return community.Account{User: community.User{ID: id, Name: "Administrator", Admin: s.admin}}, ctx.Err()
}

func (s *previewAccountStore) Session(ctx context.Context, id string) (community.Session, error) {
	s.mu.Lock()
	failed := s.failSession
	s.mu.Unlock()
	if failed {
		return community.Session{}, errors.New("session storage unavailable")
	}
	return s.Store.Session(ctx, id)
}

func privateSession(t *testing.T, store community.Store) (*http.Cookie, string) {
	t.Helper()
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	hash := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(hash[:])
	err := store.PutSession(context.Background(), key, community.Session{
		User: community.User{ID: "admin", Name: "Administrator", Admin: true}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "rangle_session", Value: token}, key
}

type previewResponse struct {
	Code    int
	Headers http.Header
	Body    *bytes.Buffer
}

func previewCall(t *testing.T, app http.Handler, method, path string, cookie *http.Cookie, headers map[string]string) *previewResponse {
	t.Helper()
	r := httptest.NewRequest(method, path, nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	result := w.Result()
	body, readErr := io.ReadAll(result.Body)
	if err := errors.Join(readErr, result.Body.Close()); err != nil {
		t.Fatal(err)
	}
	if result.Header.Get("Cache-Control") != "private, no-store" || result.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("private policy missing: status=%d headers=%v", result.StatusCode, result.Header)
	}
	return &previewResponse{Code: result.StatusCode, Headers: result.Header, Body: bytes.NewBuffer(body)}
}

func TestPrivatePreviewApplicationRefreshesRoleForEveryRequest(t *testing.T) {
	cfg, _, _ := publishedFixture(t)
	store := &previewAccountStore{Store: community.NewMemoryStore(), admin: true}
	cfg.CommunityStore = store
	cfg.PrivateSceneRoot = t.TempDir()
	model := bytes.Repeat([]byte{42}, 64)
	writeFile(t, filepath.Join(cfg.PrivateSceneRoot, "scene-a/frame-000.splat"), model)
	writeFile(t, filepath.Join(cfg.PrivateSceneRoot, "scene-a/scene.json"), []byte(`{"frames":[]}`))
	app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close()
	cookie, sessionKey := privateSession(t, store)
	path := "/api/v1/private-scenes/scene-a/frame-000.splat"
	if w := previewCall(t, app, "GET", path, nil, map[string]string{"Range": "bytes=0-3"}); w.Code != 401 {
		t.Fatal("anonymous snapshot access")
	}
	w := previewCall(t, app, "GET", path, cookie, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), model) {
		t.Fatalf("admin GET: %d", w.Code)
	}
	modified := w.Headers.Get("Last-Modified")
	for _, tc := range []struct {
		method  string
		headers map[string]string
		want    int
	}{
		{"HEAD", nil, 200}, {"GET", map[string]string{"Range": "bytes=1-4"}, 206},
		{"GET", map[string]string{"If-Modified-Since": modified}, 304},
		{"GET", map[string]string{"Range": "bytes=999-"}, 416},
	} {
		if w := previewCall(t, app, tc.method, path, cookie, tc.headers); w.Code != tc.want {
			t.Fatalf("%v status=%d", tc, w.Code)
		}
	}
	store.mu.Lock()
	if store.accountCalls != 5 {
		t.Fatalf("account refresh count=%d", store.accountCalls)
	}
	store.admin = false
	store.mu.Unlock()
	// The stored session still says admin=true. Current-account revocation wins
	// before Range/preconditions and cannot return old snapshot bytes or a 304.
	for _, method := range []string{"GET", "HEAD"} {
		if w := previewCall(t, app, method, path, cookie, map[string]string{"Range": "bytes=0-3", "If-Modified-Since": modified}); w.Code != 403 || bytes.Contains(w.Body.Bytes(), model[:4]) {
			t.Fatal("revoked administrator retained access")
		}
	}
	// Ordinary accounts also get 403 rather than retaining the session role.
	if err := store.PutSession(context.Background(), sessionKey, community.Session{User: community.User{ID: "member"}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if w := previewCall(t, app, "GET", path, cookie, nil); w.Code != 403 {
		t.Fatal("member gained access")
	}
	store.mu.Lock()
	store.failAccount = true
	store.mu.Unlock()
	if w := previewCall(t, app, "GET", path, cookie, nil); w.Code != 503 {
		t.Fatal("account failure not closed")
	}
	store.mu.Lock()
	store.failAccount = false
	store.failSession = true
	store.mu.Unlock()
	if w := previewCall(t, app, "GET", path, cookie, nil); w.Code != 503 {
		t.Fatal("session failure not closed")
	}
	store.mu.Lock()
	store.failSession = false
	store.mu.Unlock()
	if err := store.PutSession(context.Background(), sessionKey, community.Session{User: community.User{ID: "admin", Admin: true}, ExpiresAt: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if w := previewCall(t, app, "GET", path, cookie, nil); w.Code != 401 {
		t.Fatal("expired session retained access")
	}
	if err := store.PutSession(context.Background(), sessionKey, community.Session{User: community.User{ID: "admin", Admin: true}, ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(context.Background(), sessionKey); err != nil {
		t.Fatal(err)
	}
	if w := previewCall(t, app, "GET", path, cookie, nil); w.Code != 401 {
		t.Fatal("deleted session replayed")
	}
	// This private input does not become a public catalog entry.
	public := httptest.NewRecorder()
	app.ServeHTTP(public, httptest.NewRequest("GET", "/api/v1/clips", nil))
	publicResult := public.Result()
	publicBody, readErr := io.ReadAll(publicResult.Body)
	if err := errors.Join(readErr, publicResult.Body.Close()); err != nil {
		t.Fatal(err)
	}
	if publicResult.StatusCode != 200 || strings.Contains(string(publicBody), "private-scenes") || strings.Contains(string(publicBody), "scene-a") {
		t.Fatal("private preview leaked into public catalog")
	}
}

func TestPrivatePreviewApplicationMissingRootAndUncleanPaths(t *testing.T) {
	for _, root := range []string{"", filepath.Join(t.TempDir(), "not-mounted")} {
		cfg, _, _ := publishedFixture(t)
		store := &previewAccountStore{Store: community.NewMemoryStore(), admin: true}
		cfg.CommunityStore, cfg.PrivateSceneRoot = store, root
		app, err := newApplication(cfg, slog.New(slog.NewJSONHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal("optional source broke startup:", err)
		}
		cookie, _ := privateSession(t, store)
		for _, path := range []string{"/api/v1/private-scenes", "/api/v1/private-scenes/scene-a/../scene.json", "/api/v1/private-scenes/scene-a//scene.json", "/api/v1/private-scenes/scene-a/scene.json?"} {
			if w := previewCall(t, app, "GET", path, nil, nil); w.Code != 401 {
				t.Fatalf("mux bypassed authorization for %s: %d", path, w.Code)
			}
			if w := previewCall(t, app, "GET", path, cookie, nil); w.Code != 404 || w.Headers.Get("Location") != "" {
				t.Fatalf("private redirect for %s: %d", path, w.Code)
			}
		}
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
		result := w.Result()
		if err := result.Body.Close(); err != nil {
			t.Fatal(err)
		}
		if result.StatusCode != 200 {
			t.Fatal("optional source broke health")
		}
		app.Close()
	}
}
