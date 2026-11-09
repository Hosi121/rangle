package live

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func benchmarkHandler(b *testing.B) *handler {
	b.Helper()
	data, err := json.Marshal(validIndex())
	if err != nil {
		b.Fatal(err)
	}
	httpHandler, err := NewHandler(Config{Read: func(context.Context) ([]byte, error) { return data, nil }})
	if err != nil {
		b.Fatal(err)
	}
	h := httpHandler.(*handler)
	// Keep every iteration on the same warm snapshot, including slow machines.
	now := time.Now()
	h.now = func() time.Time { return now }
	if value := h.load(context.Background()); value.err != nil {
		b.Fatal(value.err)
	}
	return h
}

// These benchmarks measure warm handler work, without sockets, TLS, or a CDN.
func BenchmarkLiveHTTP(b *testing.B) {
	h := benchmarkHandler(b)
	for _, test := range []struct {
		name, method, path string
		conditional        bool
	}{
		{"index", http.MethodGet, "/api/v1/live", false},
		{"indexHEAD", http.MethodHead, "/api/v1/live", false},
		{"index304", http.MethodGet, "/api/v1/live", true},
		{"session", http.MethodGet, "/api/v1/live/basketball-live", false},
		{"session304", http.MethodGet, "/api/v1/live/basketball-live", true},
	} {
		b.Run(test.name, func(b *testing.B) {
			r := httptest.NewRequest(test.method, test.path, nil)
			wantStatus := http.StatusOK
			if test.conditional {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				response := w.Result()
				r.Header.Set("If-None-Match", response.Header.Get("ETag"))
				response.Body.Close()
				wantStatus = http.StatusNotModified
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, r)
				if w.Code != wantStatus {
					b.Fatalf("status = %d, want %d", w.Code, wantStatus)
				}
			}
		})
	}
}

func BenchmarkLiveHotLoad(b *testing.B) {
	h := benchmarkHandler(b)
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if value := h.load(ctx); value.err != nil || value.snapshot == nil {
			b.Fatal("warm snapshot unavailable")
		}
	}
}
