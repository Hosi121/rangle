package catalog

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// These benchmarks measure handler work, without sockets, TLS, or a CDN.
func BenchmarkCatalogHTTP(b *testing.B) {
	h, err := NewHandler(mustCatalog(b, fixtureJSON(b, 100)), "https://media.example.test")
	if err != nil {
		b.Fatal(err)
	}
	for _, test := range []struct {
		name, path  string
		conditional bool
	}{
		{"feed10", feedPath, false},
		{"feed50", feedPath + "?limit=50", false},
		{"feed304", feedPath, true},
		{"detail", feedPath + "/clip-0", false},
		{"detail304", feedPath + "/clip-0", true},
	} {
		b.Run(test.name, func(b *testing.B) {
			r := httptest.NewRequest(http.MethodGet, test.path, nil)
			wantStatus := http.StatusOK
			if test.conditional {
				warm := httptest.NewRecorder()
				h.ServeHTTP(warm, r)
				response := warm.Result()
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
