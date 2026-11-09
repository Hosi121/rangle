// Package serverlimit bounds API handler work before it reaches dependencies.
package serverlimit

import (
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
)

// MaxConcurrentAPIRequests is a conservative initial guard for the presentation
// instance, not a measured throughput or Cloud Run concurrency recommendation.
const MaxConcurrentAPIRequests = 8

const overloadBody = "{\"error\":\"api_overloaded\"}\n"

// New returns a shared, fail-fast gate. It queues no requests and starts no
// goroutines. The caller selects which routes share this instance of the gate.
func New(next http.Handler, logger *slog.Logger) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	active := make(chan struct{}, MaxConcurrentAPIRequests)
	var reported atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			return
		}
		select {
		case active <- struct{}{}:
			// Keep the slot while a handler is still working, even if its client
			// has canceled. Returning it early could admit more dependency work.
			defer func() { <-active }()
		default:
			// One application warning avoids amplifying an overload with a
			// warning for every rejected request.
			if reported.CompareAndSwap(false, true) {
				logger.WarnContext(r.Context(), "API concurrency limit reached", "max_active_requests", MaxConcurrentAPIRequests)
			}
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Retry-After", "1")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Content-Length", strconv.Itoa(len(overloadBody)))
			w.Header().Del("ETag")
			w.WriteHeader(http.StatusServiceUnavailable)
			if r.Method != http.MethodHead {
				_, _ = io.WriteString(w, overloadBody)
			}
			return
		}
		if r.Context().Err() != nil {
			return
		}
		next.ServeHTTP(w, r)
	})
}
