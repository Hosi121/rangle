package appserver

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// Opt-in local processing benchmark using an existing built UI asset. It creates
// no network requests and measures handler CPU/disk work, not download latency.
// Set RANGLE_UI_BENCHMARK_ASSET to a built canvaskit.wasm before running.
func BenchmarkUICompression(b *testing.B) {
	filename := os.Getenv("RANGLE_UI_BENCHMARK_ASSET")
	if filename == "" {
		b.Skip("requires an existing RANGLE_UI_BENCHMARK_ASSET")
	}
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxPrecompressedBytes {
		b.Fatal("benchmark input must be a regular UI asset within 32 MiB")
	}
	original, err := os.ReadFile(filename)
	if err != nil {
		b.Fatal(err)
	}
	root := b.TempDir()
	for name, body := range map[string][]byte{"landing.html": []byte("public UI"), "index.html": []byte("demo"), "canvaskit.wasm": original} {
		if err := os.WriteFile(filepath.Join(root, name), body, 0600); err != nil {
			b.Fatal(err)
		}
	}
	// Exercise the same producer as the container build, outside timed work.
	builder := exec.Command("node", "../../scripts/precompress-web.mjs", root)
	if output, err := builder.CombinedOutput(); err != nil {
		b.Fatalf("precompression: %s: %v", output, err)
	}
	packed, err := os.ReadFile(filepath.Join(root, "canvaskit.wasm.gz"))
	if err != nil {
		b.Fatal(err)
	}
	h, err := New(Config{UIRoot: root})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { h.Close() })
	// Verify actual served bytes and headers before recording processing cost.
	for coding, expected := range map[string][]byte{"identity": original, "gzip": packed} {
		r := httptest.NewRequest("GET", "/demo/canvaskit.wasm", nil)
		r.Header.Set("Accept-Encoding", coding)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), expected) ||
			w.Header().Get("Content-Length") != strconv.Itoa(len(expected)) ||
			w.Header().Get("Content-Type") != "application/wasm" || w.Header().Get("Vary") != "Accept-Encoding" {
			b.Fatal("served benchmark body/header differs", w.Code, w.Header())
		}
		if coding == "gzip" {
			if w.Header().Get("Content-Encoding") != "gzip" {
				b.Fatal("missing gzip encoding")
			}
			reader, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
			if err != nil {
				b.Fatal(err)
			}
			decoded, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || sha256.Sum256(decoded) != sha256.Sum256(original) {
				b.Fatal("gzip decoding changed benchmark bytes", err)
			}
		}
	}
	b.Logf("verified source=%d bytes, gzip=%d bytes; local handler processing only", len(original), len(packed))
	for _, coding := range []string{"identity", "gzip"} {
		b.Run(coding, func(b *testing.B) {
			r := httptest.NewRequest("GET", "/demo/canvaskit.wasm", nil)
			r.Header.Set("Accept-Encoding", coding)
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				w := discardUIResponse{header: make(http.Header)}
				h.ServeHTTP(&w, r)
				if w.status != 200 {
					b.Fatal("unexpected handler status", w.status)
				}
			}
		})
	}
}

type discardUIResponse struct {
	header http.Header
	status int
}

func (w *discardUIResponse) Header() http.Header    { return w.header }
func (w *discardUIResponse) WriteHeader(status int) { w.status = status }
func (w *discardUIResponse) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return len(p), nil
}
