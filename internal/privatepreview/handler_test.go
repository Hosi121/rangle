package privatepreview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func handlerFixture(t *testing.T) (*Handler, string, []byte) {
	t.Helper()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "scene-a"), 0700); err != nil {
		t.Fatal(err)
	}
	model := make([]byte, 64)
	for i := range model {
		model[i] = byte(i)
	}
	for name, data := range map[string][]byte{"scene.json": []byte(`{"version":1}`), "frame-000.splat": model} {
		if err := os.WriteFile(filepath.Join(dir, "scene-a", name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	h, err := New(Config{Directory: dir, Authorize: func(*http.Request) error { return nil }, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return h, dir, model
}

type recordedResponse struct {
	Code    int
	Headers http.Header
	Body    *bytes.Buffer
}

func request(t *testing.T, h http.Handler, method, target string, headers map[string]string) *recordedResponse {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	result := w.Result()
	body, readErr := io.ReadAll(result.Body)
	if err := errors.Join(readErr, result.Body.Close()); err != nil {
		t.Fatal(err)
	}
	if got := result.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("%s cache policy=%q", target, got)
	}
	if got := result.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("%s nosniff=%q", target, got)
	}
	return &recordedResponse{Code: result.StatusCode, Headers: result.Header, Body: bytes.NewBuffer(body)}
}

func TestAuthorizationPrecedesMethodPathRangeAndConditionals(t *testing.T) {
	h, _, _ := handlerFixture(t)
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{{"anonymous", ErrUnauthenticated, 401}, {"member_or_revoked", ErrForbidden, 403}, {"auth_storage_failed", errors.New("storage unavailable"), 503}} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			h.cfg.Authorize = func(r *http.Request) error {
				calls++
				deadline, ok := r.Context().Deadline()
				if !ok || time.Until(deadline) > 5*time.Second {
					t.Fatal("authorization lacks bounded context")
				}
				return tc.err
			}
			for _, path := range []string{PathPrefix + "scene-a/frame-000.splat", PathPrefix + "../.secret?token=not-accepted", PathPrefix + "scene-a/scene.json"} {
				for _, method := range []string{"GET", "HEAD", "POST"} {
					w := request(t, h, method, path, map[string]string{"Range": "bytes=999-", "If-Modified-Since": time.Now().Add(time.Hour).UTC().Format(http.TimeFormat)})
					if w.Code != tc.want || w.Headers.Get("Content-Range") != "" || w.Headers.Get("Last-Modified") != "" {
						t.Fatalf("authorization bypass: %s %s status=%d headers=%v", method, path, w.Code, w.Headers)
					}
				}
			}
			if calls != 9 {
				t.Fatalf("authorize calls=%d", calls)
			}
		})
	}
}

func TestAuthorizedContentHeadRangeConditionalAndErrorsRemainPrivate(t *testing.T) {
	h, _, model := handlerFixture(t)
	calls := 0
	h.cfg.Authorize = func(*http.Request) error { calls++; return nil }
	path := PathPrefix + "scene-a/frame-000.splat"
	w := request(t, h, "GET", path, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), model) || w.Headers.Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("full content: %d %v", w.Code, w.Headers)
	}
	modified := w.Headers.Get("Last-Modified")
	for _, tc := range []struct {
		method  string
		headers map[string]string
		want    int
		body    []byte
	}{{"HEAD", nil, 200, nil}, {"GET", map[string]string{"Range": "bytes=1-4"}, 206, model[1:5]}, {"GET", map[string]string{"Range": "bytes=-4"}, 206, model[60:]}, {"GET", map[string]string{"If-Modified-Since": modified}, 304, nil}, {"GET", map[string]string{"Range": "bytes=64-"}, 416, nil}, {"GET", map[string]string{"If-Match": `"absent"`}, 412, nil}} {
		w := request(t, h, tc.method, path, tc.headers)
		if w.Code != tc.want || tc.want < 400 && !bytes.Equal(w.Body.Bytes(), tc.body) {
			t.Fatalf("%s %v: status=%d body=%q", tc.method, tc.headers, w.Code, w.Body.Bytes())
		}
	}
	if calls != 7 {
		t.Fatalf("conditional/range requests skipped auth: calls=%d", calls)
	}
	w = request(t, h, "GET", PathPrefix+"scene-a/scene.json", nil)
	if w.Code != 200 || w.Headers.Get("Content-Type") != "application/json" {
		t.Fatal("scene JSON MIME")
	}
	if w := request(t, h, "POST", path, nil); w.Code != 405 || w.Headers.Get("Allow") != "GET, HEAD" {
		t.Fatal("unsupported method")
	}
}

func TestNarrowPathsRejectQueriesEscapesAndSymlinks(t *testing.T) {
	h, dir, _ := handlerFixture(t)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, bytes.Repeat([]byte("s"), 32), 0600); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"frame-001.splat": outside, "frame-002.splat": "frame-000.splat"} {
		if err := os.Symlink(target, filepath.Join(dir, "scene-a", name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "scene-a"), filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		PathPrefix, PathPrefix + "scene-a", PathPrefix + "scene-a/", PathPrefix + "scene-a/scene.json/extra",
		PathPrefix + "scene-a/frame-00.splat", PathPrefix + "scene-a/frame-0000.splat", PathPrefix + "scene-a/model.rgs",
		PathPrefix + "scene-a/.hidden", PathPrefix + ".hidden/scene.json", PathPrefix + "Scene-a/scene.json", PathPrefix + "_scene/scene.json",
		PathPrefix + strings.Repeat("a", 65) + "/scene.json", PathPrefix + "scene-a/../scene-a/scene.json",
		PathPrefix + "scene-a//scene.json", PathPrefix + "scene-a/%73cene.json", PathPrefix + "scene-a%2fscene.json",
		PathPrefix + "scene-a/scene.json?download=1", PathPrefix + "scene-a/scene.json?",
		PathPrefix + "scene-a/frame-001.splat", PathPrefix + "scene-a/frame-002.splat", PathPrefix + "linked/scene.json",
	}
	for _, path := range paths {
		if w := request(t, h, "GET", path, nil); w.Code != 404 {
			t.Fatalf("accepted %s: %d", path, w.Code)
		}
	}
	// A configured root symlink is rejected too.
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	h.cfg.Directory = link
	if w := request(t, h, "GET", PathPrefix+"scene-a/scene.json", nil); w.Code != 404 {
		t.Fatal("root symlink accepted")
	}
}

func TestMissingDisabledAndLateMountedRoot(t *testing.T) {
	h, _, _ := handlerFixture(t)
	for _, root := range []string{"", filepath.Join(t.TempDir(), "not-mounted")} {
		h.cfg.Directory = root
		if w := request(t, h, "GET", PathPrefix+"scene-a/scene.json", nil); w.Code != 404 {
			t.Fatal("missing optional source should be 404")
		}
	}
	if err := os.MkdirAll(filepath.Join(h.cfg.Directory, "scene-a"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.cfg.Directory, "scene-a/scene.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := request(t, h, "GET", PathPrefix+"scene-a/scene.json", nil); w.Code != 200 {
		t.Fatal("new mount needs restart")
	}
}

func TestSizeTypeAndSplatRecordBoundaries(t *testing.T) {
	h, dir, _ := handlerFixture(t)
	for _, tc := range []struct {
		name string
		size int64
		want int
	}{
		{"scene.json", 64 << 10, 200}, {"scene.json", 64<<10 + 1, 404},
		{"frame-000.splat", 32 << 20, 206}, {"frame-000.splat", 32<<20 + 32, 404},
		{"frame-000.splat", 31, 404}, {"frame-000.splat", 0, 404}, {"frame-000.splat", 32, 206},
	} {
		p := filepath.Join(dir, "scene-a", tc.name)
		if err := os.Truncate(p, tc.size); err != nil {
			t.Fatal(err)
		}
		headers := map[string]string{}
		if strings.HasSuffix(tc.name, ".splat") {
			headers["Range"] = "bytes=0-0"
		}
		if w := request(t, h, "GET", PathPrefix+"scene-a/"+tc.name, headers); w.Code != tc.want {
			t.Fatalf("%s size %d status=%d want=%d", tc.name, tc.size, w.Code, tc.want)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "scene-a/frame-005.splat"), 0700); err != nil {
		t.Fatal(err)
	}
	if w := request(t, h, "GET", PathPrefix+"scene-a/frame-005.splat", nil); w.Code != 404 {
		t.Fatal("directory accepted as asset")
	}
}

func TestCanceledContentDoesNotReadOrSeek(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &contextContent{ctx: ctx, ReadSeeker: bytes.NewReader([]byte("private"))}
	if _, err := c.Read(make([]byte, 1)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Seek(0, io.SeekStart); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := New(Config{}); err == nil {
		t.Fatal("missing auth accepted")
	}
}
