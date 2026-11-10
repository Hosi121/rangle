package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"time"

	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/generation"
	"github.com/Hosi121/rangle/internal/publisher"
)

type publisherCleanupManager interface {
	PublisherDeletionStatus(context.Context, string, publisher.ID) (generation.PublisherLifecycle, error)
	StartPublisherDeletion(context.Context, string, publisher.ID, string) (generation.PublisherLifecycle, error)
}

// The public coordinator alone bridges community and generation lifecycles.
// A crash between their commits is resumable using the durable request ID;
// neither runtime acquires the other database's permissions.
func publisherDeletionHandler(store community.Store, manager publisherCleanupManager, operator func(*http.Request) (string, error), logger *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	for _, method := range []string{"GET", "DELETE"} {
		mux.HandleFunc(method+" /api/v1/operations/publishers/{account}/deletion", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			write := func(code int, value any) { w.WriteHeader(code); _ = json.NewEncoder(w).Encode(value) }
			fail := func(code int, name string) { write(code, map[string]string{"error": name}) }
			ctx, cancel := context.WithTimeout(r.Context(), 28*time.Second)
			defer cancel()
			r = r.WithContext(ctx)
			actor, err := operator(r)
			if errors.Is(err, generation.ErrForbidden) {
				fail(403, "admin_required")
				return
			}
			if err != nil {
				fail(503, "service_unavailable")
				return
			}
			if actor == "" {
				fail(401, "not_authenticated")
				return
			}
			account := publisher.ID(r.PathValue("account"))
			if !account.Valid() || r.URL.RawQuery != "" {
				fail(400, "invalid_request")
				return
			}
			s, ok := store.(community.PublisherDeletionStore)
			if !ok || manager == nil {
				fail(503, "service_unavailable")
				return
			}
			d, err := s.PublisherDeletion(ctx, actor, account)
			if errors.Is(err, community.ErrNotFound) {
				fail(404, "not_found")
				return
			}
			if errors.Is(err, community.ErrForbidden) {
				fail(403, "admin_required")
				return
			}
			if err != nil {
				fail(503, "service_unavailable")
				return
			}
			if r.Method == "DELETE" {
				var body struct {
					Confirmation string `json:"confirmation"`
				}
				typ, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
				decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
				decoder.DisallowUnknownFields()
				if e != nil || typ != "application/json" || decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || body.Confirmation != string(account) {
					fail(400, "confirmation_required")
					return
				}
				// Do not strand the community account when cleanup is not configured.
				if _, err := manager.PublisherDeletionStatus(ctx, actor, account); err != nil {
					fail(503, "service_unavailable")
					return
				}
				started, err := s.BeginPublisherDeletion(ctx, actor, account, time.Now().UTC())
				if err != nil {
					fail(409, "deletion_conflict")
					return
				}
				d = &started
			}
			if d == nil {
				a, err := s.Publisher(ctx, account)
				if err != nil {
					fail(503, "service_unavailable")
					return
				}
				write(200, map[string]any{"state": a.Status, "requested": a.DeletionRequested})
				return
			}
			// Reusing Start is intentional: it repairs a crash after the community
			// fence but before the manager's begin transaction, without a new ID.
			progress, err := manager.StartPublisherDeletion(ctx, actor, account, d.RequestID)
			if err != nil || progress.Account != account || progress.RequestID != d.RequestID {
				fail(503, "deletion_pending")
				return
			}
			if progress.State == publisher.Deleted {
				if progress.Stage != "complete" || len(progress.Jobs) > generation.MaxAttempts {
					fail(503, "deletion_pending")
					return
				}
				clips := make([]string, 0, len(progress.Jobs))
				for _, id := range progress.Jobs {
					clips = append(clips, "generated-"+id)
				}
				if err := s.FinishPublisherDeletion(ctx, actor, account, d.RequestID, clips, time.Now().UTC()); err != nil {
					fail(503, "deletion_pending")
					return
				}
			}
			if logger != nil {
				logger.InfoContext(ctx, "publisher deletion progress", "publisher_account_id", account, "actor_id", actor, "state", progress.State, "stage", progress.Stage)
			}
			write(200, map[string]any{"state": progress.State, "stage": progress.Stage, "prefix": progress.Prefix, "prefix_total": 3 * len(progress.Jobs)})
		})
	}
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		protected.ServeHTTP(w, r)
	})
}
