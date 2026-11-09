package main

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/Hosi121/rangle/internal/generation"
	"github.com/Hosi121/rangle/internal/serverlimit"
)

func limitAPIRequests(next http.Handler, logger *slog.Logger) http.Handler {
	limited := serverlimit.New(next, logger)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == generation.SyncPath {
			limited.ServeHTTP(w, r)
			return
		}
		// Health, UI, manifests, and immutable media keep their existing route,
		// range, conditional-request, and cache behavior outside the API gate.
		next.ServeHTTP(w, r)
	})
}
