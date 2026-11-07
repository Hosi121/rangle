package delivery_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/delivery"
	"github.com/Hosi121/rangle/internal/stream"
)

func TestFileHandlerGetHeadAndManifestSnapshot(t *testing.T) {
	manifest, assets, root := diskFixture(t)
	h := mustFileHandler(t, manifest, root, nil)
	manifestURL := manifest.URL()
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Codec = "caller mutation"
	manifest.Representations[0].Segments[0].URL = "/changed"
	for url, data := range map[string][]byte{manifestURL: manifestData, "/assets/demo/synthetic-v1/low/init.bin": assets["/assets/demo/synthetic-v1/low/init.bin"], "/assets/demo/synthetic-v1/low/segment-0.bin": assets["/assets/demo/synthetic-v1/low/segment-0.bin"]} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(method+url, func(t *testing.T) {
				response := request(t, h, method, url, nil)
				body := readBody(t, response)
				if response.StatusCode != 200 {
					t.Fatalf("status=%d body=%q", response.StatusCode, body)
				}
				assertFileImmutable(t, response, data)
				if response.Header.Get("Content-Length") != strconv.Itoa(len(data)) || response.Header.Get("Accept-Ranges") != "bytes" {
					t.Fatal(response.Header)
				}
				wantType := "application/octet-stream"
				if url == manifestURL {
					wantType = "application/json"
				}
				if response.Header.Get("Content-Type") != wantType {
					t.Fatal(response.Header)
				}
				if method == http.MethodHead && len(body) != 0 || method == http.MethodGet && !bytes.Equal(body, data) {
					t.Fatalf("body=%q", body)
				}
			})
		}
	}
}

func TestFileHandlerRangeAndConditions(t *testing.T) {
	manifest, assets, root := diskFixture(t)
	h := mustFileHandler(t, manifest, root, nil)
	url := manifest.Representations[0].Segments[0].URL
	data := assets[url]
	etag := request(t, h, http.MethodHead, url, nil).Header.Get("ETag")
	tests := []struct {
		name, method string
		headers      map[string]string
		status       int
		body         []byte
	}{
		{"range", "GET", map[string]string{"Range": "bytes=1-4"}, 206, data[1:5]},
		{"suffix", "GET", map[string]string{"Range": "bytes=-3"}, 206, data[len(data)-3:]},
		{"range HEAD", "HEAD", map[string]string{"Range": "bytes=1-4"}, 206, nil},
		{"not modified", "GET", map[string]string{"If-None-Match": etag}, 304, nil},
		{"weak match", "GET", map[string]string{"If-None-Match": "W/" + etag}, 304, nil},
		{"not modified HEAD", "HEAD", map[string]string{"If-None-Match": etag}, 304, nil},
		{"modified since", "GET", map[string]string{"If-Modified-Since": diskModified.Format(http.TimeFormat)}, 304, nil},
		{"If-Match", "GET", map[string]string{"If-Match": etag}, 200, data},
		{"If-Range match", "GET", map[string]string{"If-Range": etag, "Range": "bytes=1-4"}, 206, data[1:5]},
		{"If-Range miss", "GET", map[string]string{"If-Range": `"other"`, "Range": "bytes=1-4"}, 200, data},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, h, test.method, url, test.headers)
			body := readBody(t, response)
			if response.StatusCode != test.status || !bytes.Equal(body, test.body) {
				t.Fatalf("status=%d body=%q", response.StatusCode, body)
			}
			assertFileImmutable(t, response, data)
			if test.status == 206 && test.name != "suffix" && response.Header.Get("Content-Range") != fmt.Sprintf("bytes 1-4/%d", len(data)) {
				t.Fatal(response.Header)
			}
		})
	}
	for _, target := range []string{manifest.URL(), manifest.Representations[0].InitURL} {
		etag := request(t, h, "HEAD", target, nil).Header.Get("ETag")
		response := request(t, h, "GET", target, map[string]string{"If-None-Match": etag})
		if response.StatusCode != 304 || len(readBody(t, response)) != 0 || response.Header.Get("Cache-Control") != immutableCacheControl+", no-transform" {
			t.Fatal(response)
		}
	}
}

func TestFileHandlerErrorsUseSentNoStoreHeaders(t *testing.T) {
	t.Setenv("GODEBUG", "httpservecontentkeepheaders=1")
	manifest, _, root := diskFixture(t)
	h := mustFileHandler(t, manifest, root, nil)
	url := manifest.Representations[0].Segments[0].URL
	privateURL := "/assets/demo/synthetic-v1/low/private.npz"
	writeDisk(t, root, privateURL, []byte("unadvertised private data"))
	tests := []struct {
		method, target string
		headers        map[string]string
		status         int
	}{
		{"GET", privateURL, nil, 404},
		{"GET", "/assets/demo/synthetic-v1/low/", nil, 404},
		{"GET", "/assets/../private.npz", nil, 404},
		{"GET", "/etc/passwd", nil, 404},
		{"GET", strings.Replace(url, "/assets/", "/%61ssets/", 1), nil, 404},
		{"GET", url + "?token=private", nil, 404},
		{"HEAD", url + "?", nil, 404},
		{"POST", url, nil, 405},
		{"HEAD", url, map[string]string{"Range": "bytes=bad"}, 416},
		{"GET", url, map[string]string{"Range": "bytes=999999-"}, 416},
		{"GET", url, map[string]string{"If-Match": `"other"`}, 412},
	}
	for _, test := range tests {
		response := request(t, h, test.method, test.target, test.headers)
		body := readBody(t, response)
		if response.StatusCode != test.status || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("ETag") != "" {
			t.Fatalf("status=%d headers=%v body=%q", response.StatusCode, response.Header, body)
		}
		if test.method == http.MethodHead && len(body) != 0 {
			t.Fatal("HEAD error sent a body")
		}
		if test.status == 405 && response.Header.Get("Allow") != "GET, HEAD" {
			t.Fatal(response.Header)
		}
	}
}

func TestFileHandlerDetectsChangedPublicationBefore304(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, string, []byte)
	}{
		{"size", func(t *testing.T, name string, _ []byte) {
			if err := os.WriteFile(name, []byte("short"), 0600); err != nil {
				t.Fatal(err)
			}
		}},
		{"mtime", func(t *testing.T, name string, _ []byte) {
			modified := diskModified.Add(time.Hour)
			if err := os.Chtimes(name, modified, modified); err != nil {
				t.Fatal(err)
			}
		}},
		{"same-size write", func(t *testing.T, name string, data []byte) {
			data = bytes.Repeat([]byte{0xff}, len(data))
			if err := os.WriteFile(name, data, 0600); err != nil {
				t.Fatal(err)
			}
			modified := diskModified.Add(time.Hour)
			if err := os.Chtimes(name, modified, modified); err != nil {
				t.Fatal(err)
			}
		}},
		{"inode with identical bytes size and mtime", func(t *testing.T, name string, data []byte) {
			replacement := name + ".replacement"
			if err := os.WriteFile(replacement, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(replacement, diskModified, diskModified); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, name); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing", func(t *testing.T, name string, _ []byte) {
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
		}},
		{"outside symlink", func(t *testing.T, name string, data []byte) {
			outside := filepath.Join(t.TempDir(), "private.bin")
			if err := os.WriteFile(outside, data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, name); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, assets, root := diskFixture(t)
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil)).With("service", "file-handler-test")
			h := mustFileHandler(t, manifest, root, logger)
			url := manifest.Representations[0].Segments[0].URL
			etag := request(t, h, "HEAD", url, nil).Header.Get("ETag")
			test.change(t, diskPath(root, url), assets[url])
			response := request(t, h, "HEAD", url, map[string]string{"If-None-Match": etag, "If-Modified-Since": diskModified.Format(http.TimeFormat)})
			if response.StatusCode != 503 || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("ETag") != "" || len(readBody(t, response)) != 0 {
				t.Fatalf("changed asset: status=%d headers=%v", response.StatusCode, response.Header)
			}
			var event struct {
				Level, Service string
				AssetURL       string `json:"asset_url"`
				ClipID         string `json:"clip_id"`
				Revision       string
				Error          string
			}
			if err := json.Unmarshal(logs.Bytes(), &event); err != nil {
				t.Fatal(err)
			}
			if event.Level != "ERROR" || event.Service != "file-handler-test" || event.AssetURL != url || event.ClipID != manifest.ClipID || event.Revision != manifest.Revision || event.Error == "" {
				t.Fatalf("I/O log: %s", logs.Bytes())
			}
		})
	}
	// Init assets have no declared SHA field, but their validated identity is
	// enforced just like a segment and their computed ETag is not reused on change.
	manifest, _, root := diskFixture(t)
	h := mustFileHandler(t, manifest, root, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	url := manifest.Representations[0].InitURL
	etag := request(t, h, "HEAD", url, nil).Header.Get("ETag")
	if err := os.Remove(diskPath(root, url)); err != nil {
		t.Fatal(err)
	}
	if response := request(t, h, "GET", url, map[string]string{"If-None-Match": etag}); response.StatusCode != 503 {
		t.Fatalf("changed init returned %d", response.StatusCode)
	}
}

func TestNewFileHandlerRejectsInvalidPublications(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *stream.Manifest, map[string][]byte, string)
	}{
		{"invalid path", func(_ *testing.T, m *stream.Manifest, _ map[string][]byte, _ string) {
			m.Representations[0].Segments[0].URL = "/assets/../private.bin"
		}},
		{"encoded path", func(_ *testing.T, m *stream.Manifest, _ map[string][]byte, _ string) {
			m.Representations[0].Segments[0].URL = "/assets/demo/synthetic-v1/low/%2e%2e"
		}},
		{"missing segment", func(t *testing.T, m *stream.Manifest, _ map[string][]byte, root string) {
			if err := os.Remove(diskPath(root, m.Representations[0].Segments[0].URL)); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing init", func(t *testing.T, m *stream.Manifest, _ map[string][]byte, root string) {
			if err := os.Remove(diskPath(root, m.Representations[0].InitURL)); err != nil {
				t.Fatal(err)
			}
		}},
		{"wrong size", func(t *testing.T, m *stream.Manifest, _ map[string][]byte, root string) {
			writeDisk(t, root, m.Representations[0].Segments[0].URL, []byte("short"))
		}},
		{"wrong hash", func(t *testing.T, m *stream.Manifest, assets map[string][]byte, root string) {
			url := m.Representations[0].Segments[0].URL
			data := bytes.Clone(assets[url])
			data[0] ^= 0xff
			writeDisk(t, root, url, data)
		}},
		{"wrong init size", func(t *testing.T, m *stream.Manifest, _ map[string][]byte, root string) {
			writeDisk(t, root, m.Representations[0].InitURL, []byte("short"))
		}},
		{"directory", func(t *testing.T, m *stream.Manifest, _ map[string][]byte, root string) {
			name := diskPath(root, m.Representations[0].Segments[0].URL)
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(name, 0700); err != nil {
				t.Fatal(err)
			}
		}},
		{"outside symlink", func(t *testing.T, m *stream.Manifest, assets map[string][]byte, root string) {
			url := m.Representations[0].Segments[0].URL
			outside := filepath.Join(t.TempDir(), "private.bin")
			if err := os.WriteFile(outside, assets[url], 0600); err != nil {
				t.Fatal(err)
			}
			name := diskPath(root, url)
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, name); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, assets, root := diskFixture(t)
			test.change(t, &manifest, assets, root)
			if h, err := delivery.NewFileHandler(manifest, root, nil); err == nil {
				h.Close()
				t.Fatal("accepted invalid publication")
			}
		})
	}
	manifest, _, root := diskFixture(t)
	manifest.Representations[0].Segments[0].SHA256 = strings.ToUpper(manifest.Representations[0].Segments[0].SHA256)
	h := mustFileHandler(t, manifest, root, nil)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if h, err := delivery.NewFileHandler(manifest, filepath.Join(root, "missing"), nil); err == nil {
		h.Close()
		t.Fatal("accepted missing root")
	}
}

func TestFileHandlerConcurrentRangesHaveIndependentOffsets(t *testing.T) {
	manifest, assets, root := diskFixture(t)
	h := mustFileHandler(t, manifest, root, nil)
	url := manifest.Representations[0].Segments[0].URL
	data := assets[url]
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		go func(index int) {
			start := index % 4
			req := httptest.NewRequest(http.MethodGet, url, nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, start+3))
			recorder := httptest.NewRecorder()
			h.ServeHTTP(recorder, req)
			response := recorder.Result()
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err == nil && (response.StatusCode != 206 || !bytes.Equal(body, data[start:start+4])) {
				err = fmt.Errorf("range %d status=%d body=%q", index, response.StatusCode, body)
			}
			results <- err
		}(i)
	}
	for i := 0; i < 16; i++ {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
}

func TestFileHandlerMultipartConsumerCancellation(t *testing.T) {
	// Exercise ServeContent's multipart pipe goroutine rather than the runtime's
	// default range-count fallback. A client that stops consuming must not keep
	// the request open or turn normal cancellation into an asset I/O error log.
	t.Setenv("GODEBUG", "httpservecontentmaxranges=200")
	manifest, _, root := diskFixture(t)
	var logs logCounter
	h := mustFileHandler(t, manifest, root, slog.New(slog.NewJSONHandler(&logs, nil)))
	url := manifest.Representations[0].Segments[0].URL
	for i := 0; i < 100; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		req := httptest.NewRequest(http.MethodGet, url, nil).WithContext(ctx)
		req.Header.Set("Range", "bytes=0-3,8-11")
		consumer := &stoppedConsumer{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
		done := make(chan struct{})
		go func() { h.ServeHTTP(consumer, req); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("multipart request did not return after consumer stopped")
		}
		cancel()
		response := consumer.Result()
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil || response.StatusCode != http.StatusPartialContent || len(body) != 0 || !strings.HasPrefix(response.Header.Get("Content-Type"), "multipart/byteranges;") || !consumer.stopped {
			t.Fatalf("multipart cancellation: status=%d headers=%v stopped=%t body=%q error=%v", response.StatusCode, response.Header, consumer.stopped, body, err)
		}
	}
	if logs.count.Load() != 0 {
		t.Fatal("request cancellation emitted an asset I/O error")
	}
}

type stoppedConsumer struct {
	*httptest.ResponseRecorder
	cancel  context.CancelFunc
	stopped bool
}

func (w *stoppedConsumer) Write([]byte) (int, error) {
	w.stopped = true
	w.cancel()
	return 0, io.ErrClosedPipe
}

type logCounter struct{ count atomic.Int64 }

func (w *logCounter) Write(data []byte) (int, error) {
	w.count.Add(1)
	return len(data), nil
}

var diskModified = time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)

func diskPath(root, url string) string {
	return filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(url, "/")))
}

func writeDisk(t *testing.T, root, url string, data []byte) {
	t.Helper()
	name := diskPath(root, url)
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(name, diskModified, diskModified); err != nil {
		t.Fatal(err)
	}
}

func diskFixture(t *testing.T) (stream.Manifest, map[string][]byte, string) {
	t.Helper()
	manifest, assets := delivery.DemoFixture()
	root := t.TempDir()
	for url, data := range assets {
		writeDisk(t, root, url, data)
	}
	return manifest, assets, root
}

func mustFileHandler(t *testing.T, manifest stream.Manifest, root string, logger *slog.Logger) *delivery.FileHandler {
	t.Helper()
	h, err := delivery.NewFileHandler(manifest, root, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Error(err)
		}
	})
	return h
}

func assertFileImmutable(t *testing.T, response *http.Response, data []byte) {
	t.Helper()
	if response.Header.Get("Cache-Control") != immutableCacheControl+", no-transform" {
		t.Fatal(response.Header)
	}
	sum := sha256.Sum256(data)
	want := `"sha256-` + hex.EncodeToString(sum[:]) + `"`
	if response.Header.Get("ETag") != want {
		t.Fatalf("ETag=%q, want %q", response.Header.Get("ETag"), want)
	}
}
