package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/api/idtoken"
)

const SyncPath = "/internal/generation/sync"

// SyncActive reconciles only the one durable active slot. It never calls
// Admit/Submit and cannot reserve attempts, choose a job, or refund the budget.
func (s *Service) SyncActive(ctx context.Context) (*Job, error) {
	if !s.now().Before(s.syncDeadline()) {
		return nil, ErrExpired
	}
	active, err := s.Store.Active(ctx)
	if err != nil || active == nil {
		return nil, err
	}
	if active.State.Terminal() {
		return active, nil
	}
	updated, err := s.refresh(ctx, *active)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// The scheduler cannot extend this presentation release's authorized window.
func (s *Service) syncDeadline() time.Time {
	cutoff := time.Date(2026, time.November, 14, 15, 0, 0, 0, time.UTC)
	if s.Config.Deadline.Before(cutoff) {
		return s.Config.Deadline
	}
	return cutoff
}

type tokenValidator func(context.Context, string, string) (*idtoken.Payload, error)

func NewSyncHandler(service *Service, audience string) (http.Handler, error) {
	return newSyncHandler(service, audience, idtoken.Validate)
}
func newSyncHandler(service *Service, audience string, validate tokenValidator) (http.Handler, error) {
	if service == nil || service.Store == nil || service.Provider == nil || validate == nil {
		return nil, ErrInvalid
	}
	if err := service.Config.Validate(); err != nil {
		return nil, err
	}
	target, err := url.Parse(audience)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.Opaque != "" || target.RawQuery != "" || target.ForceQuery || target.Fragment != "" || (target.Path != "" && (target.Path != SyncPath || target.EscapedPath() != SyncPath)) {
		return nil, fmt.Errorf("generation sync audience must be the HTTPS service base URL or exact sync endpoint")
	}
	identity := "rangle-stg-generation-sync@" + service.Config.Project + ".iam.gserviceaccount.com"
	limiter := rate.NewLimiter(rate.Every(time.Minute), 1)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		write := func(code int, value any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(value) }
		fail := func(code int, message string) { write(code, map[string]string{"error": message}) }
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "POST")
			fail(405, "method_not_allowed")
			return
		}
		if r.URL.Path != SyncPath || r.URL.RawQuery != "" {
			fail(400, "invalid_request")
			return
		}
		if !service.now().Before(service.syncDeadline()) {
			fail(410, "generation_window_ended")
			return
		}
		auth := r.Header.Values("Authorization")
		if len(auth) != 1 || len(auth[0]) > 8192 {
			fail(401, "not_authenticated")
			return
		}
		parts := strings.Fields(auth[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			fail(401, "not_authenticated")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		payload, err := validate(ctx, parts[1], audience)
		now := service.now()
		if err != nil || !validGoogleIdentity(payload, audience, identity, now) {
			fail(401, "not_authenticated")
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64))
		if err != nil || (strings.TrimSpace(string(body)) != "" && strings.TrimSpace(string(body)) != "{}") {
			fail(400, "invalid_request")
			return
		}
		if !limiter.Allow() {
			w.Header().Set("Retry-After", "60")
			fail(429, "rate_limited")
			return
		}
		job, err := service.SyncActive(ctx)
		if err == nil {
			err = service.syncPublisherCleanup(ctx)
		}
		if err != nil {
			if errors.Is(err, ErrExpired) {
				fail(410, "generation_window_ended")
				return
			}
			if service.Logger != nil {
				service.Logger.ErrorContext(ctx, "generation active sync failed", "error", err)
			}
			fail(503, "generation_unavailable")
			return
		}
		if job == nil {
			write(200, map[string]any{"active": false})
			return
		}
		write(200, map[string]any{"active": !job.State.Terminal(), "job_id": job.ID, "state": job.State})
	}), nil
}
