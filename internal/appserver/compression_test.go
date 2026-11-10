package appserver

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func gzipFixture(t *testing.T) (Config, string, []byte) {
	t.Helper()
	config := fixture(t)
	body := strings.Repeat("console.log('lossless public UI');\n", 1000)
	write(t, filepath.Join(config.UIRoot, "main.dart.js"), body)
	packed := gzipBytes(t, []byte(body))
	write(t, filepath.Join(config.UIRoot, "main.dart.js.gz"), string(packed))
	return config, body, packed
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var output bytes.Buffer
	writer, err := gzip.NewWriterLevel(&output, 6)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func openCompressed(t *testing.T, config Config) *Handler {
	t.Helper()
	h, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close() })
	return h
}

func gzipRequest(t *testing.T, h *Handler, method, target, accept string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	if accept != "" {
		r.Header.Set("Accept-Encoding", accept)
	}
	for key, value := range headers {
		r.Header.Set(key, value)
	}
	return sentResponse(t, h, r)
}

func TestUIEncodingNegotiationAndExactRepresentations(t *testing.T) {
	config, original, packed := gzipFixture(t)
	h := openCompressed(t, config)
	tests := []struct {
		accept, coding string
		status         int
	}{
		{"", "", 200}, {"gzip", "gzip", 200}, {"GZip; q=1", "gzip", 200},
		{"gzip;q=0", "", 200}, {"gzip;q=0.5", "", 200},
		{"gzip;q=0.5,identity;q=0.1", "gzip", 200}, {"*", "gzip", 200},
		{"*;q=0", "", 406}, {"*;q=0,identity;q=1", "", 200},
		{"gzip;q=0,*;q=1", "", 200}, {"gzip,identity;q=0", "gzip", 200},
		{"br,identity;q=0", "", 406}, {"gzip;q=0,identity;q=0", "", 406},
		{"gzip;q=2", "", 200}, {"gzip;q=.5", "", 200},
		{"gzip;q=1;q=0", "", 200}, {"gzip;q=0,gzip;q=1", "", 200},
		{"*;q=1,gzip;q=0,identity;q=0", "", 406},
	}
	for _, test := range tests {
		t.Run(test.accept, func(t *testing.T) {
			response, body := gzipRequest(t, h, "GET", "/demo/main.dart.js", test.accept, nil)
			if response.StatusCode != test.status || response.Header.Get("Content-Encoding") != test.coding || response.Header.Get("Vary") != "Accept-Encoding" {
				t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
			}
			if test.status == 406 {
				if response.Header.Get("Cache-Control") != "no-store" {
					t.Fatal(response.Header)
				}
				return
			}
			if !strings.Contains(response.Header.Get("Content-Type"), "javascript") {
				t.Fatal("lost original JavaScript Content-Type:", response.Header)
			}
			if response.Header.Get("Cache-Control") != "no-cache" || response.Header.Get("Last-Modified") != testModified.Format(http.TimeFormat) {
				t.Fatal(response.Header)
			}
			if test.coding == "gzip" {
				if body != string(packed) || response.Header.Get("Content-Length") != strconv.Itoa(len(packed)) {
					t.Fatal("gzip bytes/length changed", response.Header)
				}
				reader, err := gzip.NewReader(strings.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(reader)
				reader.Close()
				if err != nil || string(decoded) != original {
					t.Fatal("gzip changed original UI bytes", err)
				}
			} else if body != original {
				t.Fatal("identity bytes changed")
			}
		})
	}
}

func TestUICompressedHEADConditionalAndRanges(t *testing.T) {
	config, original, packed := gzipFixture(t)
	h := openCompressed(t, config)
	response, body := gzipRequest(t, h, "HEAD", "/demo/main.dart.js", "gzip", nil)
	if response.StatusCode != 200 || body != "" || response.Header.Get("Content-Encoding") != "gzip" || response.Header.Get("Content-Length") != strconv.Itoa(len(packed)) {
		t.Fatalf("HEAD status=%d body=%q headers=%v", response.StatusCode, body, response.Header)
	}
	for _, coding := range []string{"gzip", "identity"} {
		response, body = gzipRequest(t, h, "GET", "/demo/main.dart.js", coding, map[string]string{"If-Modified-Since": testModified.Format(http.TimeFormat)})
		if response.StatusCode != 304 || body != "" || response.Header.Get("Vary") != "Accept-Encoding" || response.Header.Get("Content-Length") != "" {
			t.Fatalf("conditional %s: %d %q %v", coding, response.StatusCode, body, response.Header)
		}
	}
	response, body = gzipRequest(t, h, "GET", "/demo/main.dart.js", "gzip", map[string]string{"Range": "bytes=2-5"})
	if response.StatusCode != 206 || body != original[2:6] || response.Header.Get("Content-Encoding") != "" || response.Header.Get("Content-Range") != fmt.Sprintf("bytes 2-5/%d", len(original)) {
		t.Fatalf("identity range: %d %q %v", response.StatusCode, body, response.Header)
	}
	response, body = gzipRequest(t, h, "GET", "/demo/main.dart.js", "gzip", map[string]string{"Range": "bytes=2-5", "If-Range": testModified.Add(-time.Hour).Format(http.TimeFormat)})
	if response.StatusCode != 200 || body != original || response.Header.Get("Content-Encoding") != "" {
		t.Fatalf("If-Range fallback: %d %v", response.StatusCode, response.Header)
	}
	for _, value := range []string{"bytes=2-5", "bytes=0-1,4-5", "bytes=999999-"} {
		response, body = gzipRequest(t, h, "GET", "/demo/main.dart.js", "gzip,identity;q=0", map[string]string{"Range": value})
		if response.StatusCode != 200 || body != string(packed) || response.Header.Get("Content-Encoding") != "gzip" || response.Header.Get("Content-Range") != "" {
			t.Fatalf("gzip-only range %s: %d %v", value, response.StatusCode, response.Header)
		}
	}
	response, _ = gzipRequest(t, h, "GET", "/demo/main.dart.js", "gzip", map[string]string{"Range": "bytes=999999-"})
	if response.StatusCode != 416 || response.Header.Get("Content-Encoding") != "" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(response)
	}
	response, body = gzipRequest(t, h, "HEAD", "/demo/main.dart.js", "*;q=0", nil)
	if response.StatusCode != 406 || body != "" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HEAD 406: %d %q %v", response.StatusCode, body, response.Header)
	}
}

func TestCompressedPreconditionErrorsAreReadableOverHTTP(t *testing.T) {
	config, _, _ := gzipFixture(t)
	h := openCompressed(t, config)
	server := httptest.NewServer(h)
	defer server.Close()
	for _, condition := range []struct{ name, value string }{
		{"If-Match", `"another-representation"`},
		{"If-Unmodified-Since", testModified.Add(-time.Hour).Format(http.TimeFormat)},
	} {
		for _, explicitEncoding := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/explicit-gzip=%t", condition.name, explicitEncoding), func(t *testing.T) {
				request, err := http.NewRequest("GET", server.URL+"/demo/main.dart.js", nil)
				if err != nil {
					t.Fatal(err)
				}
				request.Header.Set(condition.name, condition.value)
				if explicitEncoding {
					request.Header.Set("Accept-Encoding", "gzip")
				}
				// With no explicit header Go negotiates and decodes gzip itself.
				// The other mode exposes the raw framing/encoding headers.
				response, err := server.Client().Do(request)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				if readErr != nil {
					t.Fatalf("could not read 412 body: %v (headers=%v)", readErr, response.Header)
				}
				if response.StatusCode != http.StatusPreconditionFailed || len(body) != 0 ||
					response.ContentLength != 0 || response.Header.Get("Content-Encoding") != "" ||
					response.Header.Get("Content-Range") != "" || response.Header.Get("Cache-Control") != "no-store" ||
					response.Header.Get("Vary") != "Accept-Encoding" {
					t.Fatalf("412 status=%d length=%d body=%q headers=%v", response.StatusCode, response.ContentLength, body, response.Header)
				}
			})
		}
	}
}

func TestSidecarsCannotPublishPathsOrCompressMedia(t *testing.T) {
	config, _, _ := gzipFixture(t)
	for _, name := range []string{"model.rgs", "model.splat", "preview.webm", "font.woff2", ".hidden.js", "samples/private.js", "orphan.js.gz"} {
		write(t, filepath.Join(config.UIRoot, name), strings.Repeat("unpublished gzip fixture", 100))
		write(t, filepath.Join(config.UIRoot, name+".gz"), string(gzipBytes(t, []byte(strings.Repeat("unpublished gzip fixture", 100)))))
	}
	wasm := strings.Repeat("wasm bytes", 1000)
	write(t, filepath.Join(config.UIRoot, "canvaskit/canvaskit.wasm"), wasm)
	write(t, filepath.Join(config.UIRoot, "canvaskit/canvaskit.wasm.gz"), string(gzipBytes(t, []byte(wasm))))
	h := openCompressed(t, config)
	for _, target := range []string{"/demo/main.dart.js.gz", "/main.dart.js.gz", "/demo/.hidden.js", "/demo/.hidden.js.gz", "/demo/samples/private.js", "/demo/samples/private.js.gz", "/demo/orphan.js.gz", "/demo/orphan.js.gz.gz"} {
		response, _ := gzipRequest(t, h, "GET", target, "gzip", nil)
		if response.StatusCode != 404 || response.Header.Get("Content-Encoding") != "" || response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("published auxiliary path %s: %d %v", target, response.StatusCode, response.Header)
		}
	}
	for _, target := range []string{"/demo/model.rgs", "/demo/model.splat", "/demo/preview.webm", "/samples/basketball/frames/000.splat", "/health"} {
		response, _ := gzipRequest(t, h, "GET", target, "gzip,identity;q=0", nil)
		if response.StatusCode != 200 || response.Header.Get("Content-Encoding") != "" || response.Header.Get("Vary") != "" {
			t.Fatalf("compressed non-UI path %s: %d %v", target, response.StatusCode, response.Header)
		}
	}
	response, _ := gzipRequest(t, h, "GET", "/demo/font.woff2", "gzip,identity;q=0", nil)
	if response.StatusCode != 406 {
		t.Fatal("identity-only UI font ignored an encoding refusal", response.StatusCode)
	}
	response, _ = gzipRequest(t, h, "GET", "/demo/canvaskit/canvaskit.wasm", "gzip", nil)
	if response.StatusCode != 200 || response.Header.Get("Content-Encoding") != "gzip" || response.Header.Get("Content-Type") != "application/wasm" {
		t.Fatal(response)
	}
}

func TestInvalidSidecarFallsBackWithoutExposingSymlinks(t *testing.T) {
	for _, kind := range []string{"missing", "invalid gzip", "different content", "truncated", "trailing", "second member", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			config, original, packed := gzipFixture(t)
			filename := filepath.Join(config.UIRoot, "main.dart.js.gz")
			switch kind {
			case "missing":
				os.Remove(filename)
			case "invalid gzip":
				write(t, filename, "not gzip")
			case "different content":
				write(t, filename, string(gzipBytes(t, []byte(strings.Repeat("other bytes", 1000)))))
			case "truncated":
				write(t, filename, string(packed[:len(packed)-1]))
			case "trailing":
				write(t, filename, string(packed)+"trailing")
			case "second member":
				write(t, filename, string(packed)+string(gzipBytes(t, nil)))
			case "symlink":
				outside := filepath.Join(t.TempDir(), "secret.gz")
				write(t, outside, string(packed))
				os.Remove(filename)
				if err := os.Symlink(outside, filename); err != nil {
					t.Fatal(err)
				}
			}
			h := openCompressed(t, config)
			response, body := gzipRequest(t, h, "GET", "/demo/main.dart.js", "gzip", nil)
			if response.StatusCode != 200 || body != original || response.Header.Get("Content-Encoding") != "" {
				t.Fatalf("invalid sidecar served: %d %v", response.StatusCode, response.Header)
			}
			response, _ = gzipRequest(t, h, "GET", "/demo/main.dart.js", "gzip,identity;q=0", nil)
			if response.StatusCode != 406 {
				t.Fatal("ignored identity refusal", response.StatusCode)
			}
		})
	}
}

func TestInPlaceOriginalOrSidecarEditsNeverServeStaleGzip(t *testing.T) {
	for _, changed := range []string{"original", "sidecar"} {
		t.Run(changed, func(t *testing.T) {
			config, original, packed := gzipFixture(t)
			h := openCompressed(t, config)
			filename := filepath.Join(config.UIRoot, "main.dart.js")
			want := original
			if changed == "original" {
				// Restore both size and mtime: ctime must still invalidate gzip.
				want = "X" + original[1:]
				write(t, filename, want)
			} else {
				packed[len(packed)-1] ^= 1
				write(t, filename+".gz", string(packed))
			}
			response, body := gzipRequest(t, h, "GET", "/demo/main.dart.js", "gzip", nil)
			if response.StatusCode != 200 || body != want || response.Header.Get("Content-Encoding") != "" {
				t.Fatalf("served stale gzip: %d %v", response.StatusCode, response.Header)
			}
			response, body = gzipRequest(t, h, "GET", "/demo/main.dart.js", "gzip", map[string]string{"If-Modified-Since": testModified.Format(http.TimeFormat)})
			if response.StatusCode != 200 || body != want || response.Header.Get("Content-Encoding") != "" || response.Header.Get("Last-Modified") != "" {
				t.Fatalf("conditional request reused stale gzip: %d %v", response.StatusCode, response.Header)
			}
		})
	}
}

func TestConcurrentCompressedReadersDoNotChangePinnedOffsets(t *testing.T) {
	config, _, packed := gzipFixture(t)
	h := openCompressed(t, config)
	var group sync.WaitGroup
	for i := 0; i < 20; i++ {
		group.Go(func() {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest("GET", "/demo/main.dart.js", nil)
			request.Header.Set("Accept-Encoding", "gzip")
			h.ServeHTTP(recorder, request)
			if recorder.Code != 200 || !bytes.Equal(recorder.Body.Bytes(), packed) {
				t.Errorf("concurrent gzip status=%d", recorder.Code)
			}
		})
	}
	group.Wait()
}
