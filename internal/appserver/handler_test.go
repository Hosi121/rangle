package appserver

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPFilesAndConditionalRequests(t *testing.T) {
	config := fixture(t)
	handler, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	tests := []struct {
		name, method, target string
		headers              map[string]string
		status               int
		body, cache          string
	}{
		{"product entry", "GET", "/", nil, 200, "<html>rangle product</html>", "no-cache"},
		{"demo index", "GET", "/demo/", nil, 200, "<html>app</html>", "no-cache"},
		{"demo HEAD", "HEAD", "/demo/", nil, 200, "", "no-cache"},
		{"UI JavaScript", "GET", "/demo/assets/app.js", nil, 200, "console.log('app');", "no-cache"},
		{"UI HEAD", "HEAD", "/", nil, 200, "", "no-cache"},
		{"frame", "GET", "/samples/basketball/frames/000.splat", nil, 200, "0123456789", "no-cache"},
		{"frame HEAD", "HEAD", "/samples/basketball/frames/000.splat", nil, 200, "", "no-cache"},
		{"range", "GET", "/samples/basketball/frames/000.splat", map[string]string{"Range": "bytes=2-5"}, 206, "2345", "no-cache"},
		{"suffix range", "GET", "/samples/basketball/frames/000.splat", map[string]string{"Range": "bytes=-3"}, 206, "789", "no-cache"},
		{"invalid range", "GET", "/samples/basketball/frames/000.splat", map[string]string{"Range": "bytes=40-50"}, 416, "", "no-store"},
		{"not modified", "GET", "/samples/basketball/frames/000.splat", map[string]string{"If-Modified-Since": testModified.Format(http.TimeFormat)}, 304, "", "no-cache"},
		{"If-Range miss sends full file", "GET", "/samples/basketball/frames/000.splat", map[string]string{"Range": "bytes=2-5", "If-Range": testModified.Add(-time.Hour).Format(http.TimeFormat)}, 200, "0123456789", "no-cache"},
		{"health", "GET", "/healthz", nil, 200, "ok\n", "no-cache"},
		{"health HEAD", "HEAD", "/healthz", nil, 200, "", "no-cache"},
		{"public health", "GET", "/health", nil, 200, "ok\n", "no-cache"},
		{"public health HEAD", "HEAD", "/health", nil, 200, "", "no-cache"},
		{"private benchmark", "GET", "/samples/basketball/benchmark.json", nil, 404, "", "no-store"},
		{"private raw weights", "GET", "/samples/basketball/frames/raw.npz", nil, 404, "", "no-store"},
		{"unlisted frame", "GET", "/samples/basketball/frames/secret.splat", nil, 404, "", "no-store"},
		{"unknown scene", "GET", "/samples/other/scene.json", nil, 404, "", "no-store"},
		{"hidden UI file", "GET", "/demo/.env", nil, 404, "", "no-store"},
		{"hidden UI directory", "GET", "/demo/.private/key", nil, 404, "", "no-store"},
		{"local UI notes", "GET", "/demo/README.local.md", nil, 404, "", "no-store"},
		{"private UI notes", "GET", "/demo/assets/review.private.md", nil, 404, "", "no-store"},
		{"UI directory listing", "GET", "/demo/assets/", nil, 404, "", "no-store"},
		{"sample directory listing", "GET", "/samples/basketball/frames/", nil, 404, "", "no-store"},
		{"UI samples cannot bypass scene allowlist", "GET", "/samples/private.txt", nil, 404, "", "no-store"},
		{"traversal", "GET", "/samples/basketball/../benchmark.json", nil, 404, "", "no-store"},
		{"encoded traversal", "GET", "/samples/basketball/%2e%2e/benchmark.json", nil, 404, "", "no-store"},
		{"method", "POST", "/", nil, 405, "", "no-store"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.target, nil)
			for key, value := range test.headers {
				request.Header.Set(key, value)
			}
			response, body := sentResponse(t, handler, request)
			if response.StatusCode != test.status || response.Header.Get("Cache-Control") != test.cache {
				t.Fatalf("status=%d headers=%v body=%q", response.StatusCode, response.Header, body)
			}
			if test.status < 400 && body != test.body {
				t.Fatalf("body=%q, want %q", body, test.body)
			}
			if test.name == "range" && response.Header.Get("Content-Range") != "bytes 2-5/10" {
				t.Fatal(response.Header)
			}
			if test.name == "invalid range" && response.Header.Get("Content-Range") != "bytes */10" {
				t.Fatal("media 416 lost its unsatisfied range", response.Header)
			}
			if test.name == "frame HEAD" && response.Header.Get("Content-Length") != "10" {
				t.Fatal(response.Header)
			}
			if test.name == "method" && response.Header.Get("Allow") != "GET, HEAD" {
				t.Fatal(response.Header)
			}
			if test.status == 200 && strings.Contains(test.target, ".splat") && response.Header.Get("Last-Modified") != testModified.Format(http.TimeFormat) {
				t.Fatal(response.Header)
			}
		})
	}
	// The manifest has the same revalidation behavior as the frame files.
	request := httptest.NewRequest("GET", "/samples/basketball/scene.json", nil)
	response, body := sentResponse(t, handler, request)
	if response.StatusCode != 200 || !strings.Contains(body, "frames/000.splat") || response.Header.Get("Last-Modified") != testModified.Format(http.TimeFormat) {
		t.Fatal(response)
	}
	request.Header.Set("If-Modified-Since", response.Header.Get("Last-Modified"))
	response, body = sentResponse(t, handler, request)
	if response.StatusCode != 304 || body != "" {
		t.Fatal(response)
	}
}

func TestMutableFileRevalidationAndPinnedAllowlist(t *testing.T) {
	config := fixture(t)
	handler, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	frame := filepath.Join(config.SceneDir, "frames/000.splat")
	// In-place changes keep the permitted inode and receive its new mtime.
	if err := os.WriteFile(frame, []byte("updated"), 0600); err != nil {
		t.Fatal(err)
	}
	modified := testModified.Add(time.Hour)
	if err := os.Chtimes(frame, modified, modified); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/samples/basketball/frames/000.splat", nil)
	request.Header.Set("If-Modified-Since", testModified.Format(http.TimeFormat))
	response, body := sentResponse(t, handler, request)
	if response.StatusCode != 200 || body != "updated" || response.Header.Get("Last-Modified") != modified.Format(http.TimeFormat) {
		t.Fatal(response)
	}
	// A replacement symlink cannot redirect the pinned descriptor outside root.
	outside := filepath.Join(t.TempDir(), "secret.splat")
	write(t, outside, "outside secret")
	if err := os.Remove(frame); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, frame); err != nil {
		t.Fatal(err)
	}
	_, body = sentResponse(t, handler, request)
	if body != "updated" {
		t.Fatalf("replaced file redirected request: %q", body)
	}
	// Manifest edits do not add new files to the running allowlist.
	write(t, filepath.Join(config.SceneDir, "scene.json"), `{"version":1,"frameRate":25,"frames":["frames/secret.splat"]}`)
	_, body = sentResponse(t, handler, httptest.NewRequest("GET", "/samples/basketball/scene.json", nil))
	if strings.Contains(body, "secret.splat") {
		t.Fatal("manifest escaped its startup snapshot")
	}
}

func TestDemoRoutesAndDeclaredPreviewAliases(t *testing.T) {
	config := fixture(t)
	for _, relative := range []string{"main.dart.js", "flutter_bootstrap.js", "viewer/viewer.js", "viewer/temporal-worker.js", "assets/fonts/font.woff2", "canvaskit/canvaskit.js", "quality-previews/unlisted.webm"} {
		write(t, filepath.Join(config.UIRoot, relative), "demo resource")
	}
	write(t, filepath.Join(config.UIRoot, "quality-previews/clip.webm"), "0123456789")
	config.PreviewPaths = []string{"/quality-previews/clip.webm", "/quality-previews/later.webm", "/main.dart.js"}
	h, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, query := range []string{"", "?", "?clip=one&next=%2Fviewer%2F"} {
			r, _ := sentResponse(t, h, httptest.NewRequest(method, "/demo"+query, nil))
			if r.StatusCode != http.StatusPermanentRedirect || r.Header.Get("Location") != "/demo/"+query {
				t.Fatalf("%s redirect %q: status=%d location=%q", method, query, r.StatusCode, r.Header.Get("Location"))
			}
		}
		for _, target := range []string{"/demo/main.dart.js", "/demo/viewer/viewer.js", "/demo/assets/fonts/font.woff2", "/quality-previews/clip.webm"} {
			r, body := sentResponse(t, h, httptest.NewRequest(method, target, nil))
			if r.StatusCode != 200 || method == http.MethodHead && body != "" {
				t.Fatalf("%s %s: %d %q", method, target, r.StatusCode, body)
			}
		}
	}
	for _, target := range []string{"/index.html", "/landing.html", "/demo/landing.html", "/main.dart.js", "/flutter_bootstrap.js", "/viewer/viewer.js", "/viewer/temporal-worker.js", "/assets/fonts/font.woff2", "/canvaskit/canvaskit.js", "/quality-previews/unlisted.webm", "/demo/healthz", "/demo/samples/private.txt", "/demo/../main.dart.js", "/demo/%2e%2e/main.dart.js"} {
		r, _ := sentResponse(t, h, httptest.NewRequest(http.MethodGet, target, nil))
		if r.StatusCode != 404 || r.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("unexpected static alias %s: %d %v", target, r.StatusCode, r.Header)
		}
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, "/quality-previews/clip.webm", nil)
	rangeRequest.Header.Set("Range", "bytes=2-5")
	r, body := sentResponse(t, h, rangeRequest)
	if r.StatusCode != 206 || body != "2345" || r.Header.Get("Content-Type") != "video/webm" || r.Header.Get("Content-Range") != "bytes 2-5/10" {
		t.Fatalf("preview Range: %d %q %v", r.StatusCode, body, r.Header)
	}
	// A new file cannot become public until restart, even if its URL was declared.
	write(t, filepath.Join(config.UIRoot, "quality-previews/later.webm"), "later media")
	for _, target := range []string{"/quality-previews/later.webm", "/demo/quality-previews/later.webm"} {
		r, _ := sentResponse(t, h, httptest.NewRequest(http.MethodGet, target, nil))
		if r.StatusCode != 404 {
			t.Fatalf("new file escaped startup allowlist: %s", target)
		}
	}
	// In-place updates retain the original permitted descriptor.
	write(t, filepath.Join(config.UIRoot, "main.dart.js"), "updated in place")
	request := httptest.NewRequest(http.MethodGet, "/demo/main.dart.js", nil)
	_, body = sentResponse(t, h, request)
	if body != "updated in place" {
		t.Fatalf("demo revalidation: %q", body)
	}
}

func TestUIRequiresLandingAndValidPreviewPaths(t *testing.T) {
	config := fixture(t)
	if err := os.Remove(filepath.Join(config.UIRoot, "landing.html")); err != nil {
		t.Fatal(err)
	}
	if h, err := New(config); err == nil {
		h.Close()
		t.Fatal("accepted UI without a product entry")
	}
	for _, target := range []string{"https://media.example.test/clip.webm", "//media.example.test/clip.webm", "/clip.webm?token=example", "/clip%2ewebm", "/a/../clip.webm"} {
		config := fixture(t)
		config.PreviewPaths = []string{target}
		if h, err := New(config); err == nil {
			h.Close()
			t.Fatalf("accepted invalid local preview path %q", target)
		}
	}
}

func TestRejectInvalidSceneAndSymlinkPaths(t *testing.T) {
	for _, frame := range []string{"../secret.splat", "/secret.splat", "frames/../secret.splat", ".hidden.splat", "frames/.private/a.splat", "frames/a%2fsplat.splat", "https://example.test/a.splat", "frames\\a.splat", "frames/a.splat?secret", "frames/a.npz"} {
		t.Run(frame, func(t *testing.T) {
			config := fixture(t)
			write(t, filepath.Join(config.SceneDir, "scene.json"), fmt.Sprintf(`{"version":1,"frameRate":25,"frames":[%q]}`, frame))
			if h, err := New(config); err == nil {
				h.Close()
				t.Fatal("accepted invalid frame path")
			}
		})
	}
	for _, location := range []string{"file", "directory", "scene manifest"} {
		t.Run(location, func(t *testing.T) {
			config := fixture(t)
			outside := t.TempDir()
			write(t, filepath.Join(outside, "000.splat"), "secret")
			write(t, filepath.Join(outside, "scene.json"), `{"version":1,"frameRate":25,"frames":["000.splat"]}`)
			write(t, filepath.Join(outside, "index.html"), "secret UI")
			var target, link string
			switch location {
			case "file":
				target, link = filepath.Join(outside, "000.splat"), filepath.Join(config.SceneDir, "frames/000.splat")
			case "directory":
				target, link = outside, filepath.Join(config.SceneDir, "frames")
			case "scene manifest":
				target, link = filepath.Join(outside, "scene.json"), filepath.Join(config.SceneDir, "scene.json")
			}
			if err := os.RemoveAll(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			if h, err := New(config); err == nil {
				h.Close()
				t.Fatal("accepted symlink in published scene/root")
			}
		})
	}
	config := fixture(t)
	if err := os.Symlink(config.SceneDir, filepath.Join(config.UIRoot, "linked")); err != nil {
		t.Fatal(err)
	}
	handler, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	response, _ := sentResponse(t, handler, httptest.NewRequest("GET", "/demo/linked/scene.json", nil))
	if response.StatusCode != 404 {
		t.Fatal("UI symlink exposed scene root")
	}
}

func TestConfigurationAndBoundedManifest(t *testing.T) {
	for _, config := range []Config{{}, {UIRoot: t.TempDir()}, {UIRoot: "absent"}, {UIRoot: t.TempDir(), SceneDir: "somewhere"}, {UIRoot: t.TempDir(), SceneID: "basketball"}, {UIRoot: t.TempDir(), SceneDir: "somewhere", SceneID: "../secret"}} {
		if h, err := New(config); err == nil {
			h.Close()
			t.Fatalf("accepted invalid config: %+v", config)
		}
	}
	for _, data := range []string{`{`, `{"version":2,"frameRate":25,"frames":["frames/000.splat"]}`, `{"version":1,"frameRate":0,"frames":["frames/000.splat"]}`, `{"version":1,"frameRate":25,"frames":[]}`, strings.Repeat(" ", maxManifestBytes+1)} {
		config := fixture(t)
		write(t, filepath.Join(config.SceneDir, "scene.json"), data)
		if h, err := New(config); err == nil {
			h.Close()
			t.Fatal("accepted invalid/oversized manifest")
		}
	}
	config := fixture(t)
	config.SceneDir, config.SceneID = "", ""
	h, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal("Close is not idempotent:", err)
	}
}

func TestConcurrentRangeReaders(t *testing.T) {
	h, err := New(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	server := httptest.NewServer(h)
	defer server.Close()
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func() {
			request, _ := http.NewRequest("GET", server.URL+"/samples/basketball/frames/000.splat", nil)
			request.Header.Set("Range", "bytes=2-5")
			response, err := server.Client().Do(request)
			if err != nil {
				results <- err
				return
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err == nil && (response.StatusCode != 206 || string(body) != "2345") {
				err = fmt.Errorf("range status=%d body=%q", response.StatusCode, body)
			}
			results <- err
		}()
	}
	for i := 0; i < 16; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}

var testModified = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

// Assert the response snapshot sent at WriteHeader, never the recorder's mutable
// Header map. Closing and reading use Result.Body as they do for a real client.
func sentResponse(t *testing.T, handler *Handler, request *http.Request) (*http.Response, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
}

func fixture(t *testing.T) Config {
	t.Helper()
	ui, scene := t.TempDir(), t.TempDir()
	for name, data := range map[string]string{"landing.html": "<html>rangle product</html>", "index.html": "<html>app</html>", "assets/app.js": "console.log('app');", ".env": "private UI secret", ".private/key": "private key", "samples/private.txt": "not a published sample", "README.local.md": "private local notes", "assets/review.private.md": "private review notes"} {
		write(t, filepath.Join(ui, name), data)
	}
	write(t, filepath.Join(scene, "scene.json"), `{"version":1,"frameRate":25,"frames":["frames/000.splat"]}`)
	write(t, filepath.Join(scene, "frames/000.splat"), "0123456789")
	write(t, filepath.Join(scene, "frames/secret.splat"), "private unlisted frame")
	write(t, filepath.Join(scene, "frames/raw.npz"), "private weights")
	write(t, filepath.Join(scene, "benchmark.json"), "private benchmark")
	return Config{UIRoot: ui, SceneDir: scene, SceneID: "basketball"}
}

func write(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(name, testModified, testModified); err != nil {
		t.Fatal(err)
	}
}
