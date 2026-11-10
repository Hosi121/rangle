package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"google.golang.org/api/idtoken"
)

const serviceAuthorizationHeader = "X-Rangle-Service-Authorization"
const ownerHeader = "X-Rangle-Owner"
const publicClipsPath = "/internal/generation/public-clips"

var tokenSubject = regexp.MustCompile(`^[0-9]{6,32}$`)

// ManagerConfig explicitly pins the one public runtime whose authenticated
// account IDs this service trusts. The runtime receives no ledger or Vertex
// credentials; every operation still runs Service's admission and job policy.
type ManagerConfig struct {
	Audience     string
	Caller       string
	SyncAudience string
}

func (cfg ManagerConfig) Validate(settings Config) error {
	if !managerAudience(cfg.Audience) ||
		!strings.HasSuffix(cfg.Caller, "@"+settings.Project+".iam.gserviceaccount.com") ||
		!identifier.MatchString(strings.Split(cfg.Caller, "@")[0]) ||
		settings.Database != "rangle-stg-generation" ||
		(cfg.SyncAudience != "" && cfg.SyncAudience != cfg.Audience) {
		return fmt.Errorf("generation manager requires an HTTPS base audience, explicit project caller and isolated generation database")
	}
	return nil
}

func managerAudience(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Opaque == "" &&
		u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func validGoogleIdentity(payload *idtoken.Payload, audience, identity string, now time.Time) bool {
	return payload != nil && payload.Audience == audience &&
		(payload.Issuer == "https://accounts.google.com" || payload.Issuer == "accounts.google.com") &&
		tokenSubject.MatchString(payload.Subject) && payload.Expires > now.Unix() &&
		payload.IssuedAt > 0 && payload.IssuedAt <= now.Add(time.Minute).Unix() &&
		payload.Claims["email"] == identity && payload.Claims["email_verified"] == true
}

func bearerToken(r *http.Request, header string) (string, bool) {
	values := r.Header.Values(header)
	if len(values) != 1 || len(values[0]) > 8192 {
		return "", false
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

// NewManager exposes only the existing bounded generation API and public
// metadata. It never accepts provider specifications or database operations.
// Cloud Run IAM is required in addition to this independent signature check.
func NewManager(service *Service, cfg ManagerConfig) (http.Handler, error) {
	return newManager(service, cfg, idtoken.Validate)
}

func newManager(service *Service, cfg ManagerConfig, validate tokenValidator) (http.Handler, error) {
	if service == nil || validate == nil {
		return nil, ErrInvalid
	}
	if err := cfg.Validate(service.Config); err != nil {
		return nil, err
	}
	jobs, err := newPrincipalHandler(service, func(r *http.Request) (Principal, error) {
		return managerPrincipal(r, service.now())
	}, "/api/v1/generation")
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/generation/", jobs)
	grants, err := newPublisherGrantHandler(service, func(r *http.Request) (Principal, error) { return managerPrincipal(r, service.now()) })
	if err != nil {
		return nil, err
	}
	mux.Handle("/api/v1/operations/publishers/{account}/generation/", grants)
	deletion, err := newPublisherDeletionHandler(service, func(r *http.Request) (Principal, error) { return managerPrincipal(r, service.now()) })
	if err != nil {
		return nil, err
	}
	mux.Handle("/api/v1/operations/publishers/{account}/deletion", deletion)
	mux.Handle("/api/v1/clips/{clip}", jobs.ClipHandler(http.NotFoundHandler()))
	mux.HandleFunc("GET "+publicClipsPath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			jobs.fail(w, r, ErrInvalid)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		clips, err := service.PublicClips(ctx)
		if err != nil {
			jobs.fail(w, r, err)
			return
		}
		jobs.write(w, r, 200, map[string]any{"clips": clips})
	})
	protected := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		// Use an application header: Cloud Run can remove signatures from
		// platform authorization headers before forwarding to the container.
		token, ok := bearerToken(r, serviceAuthorizationHeader)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var payload *idtoken.Payload
		var err error
		if ok {
			payload, err = validate(ctx, token, cfg.Audience)
		}
		if !ok || err != nil || !validGoogleIdentity(payload, cfg.Audience, cfg.Caller, service.now()) {
			jobs.write(w, r, http.StatusUnauthorized, map[string]string{"error": "not_authenticated"})
			return
		}
		mux.ServeHTTP(w, r)
	})
	outer := http.NewServeMux()
	outer.Handle("/", protected)
	if cfg.SyncAudience != "" {
		sync, err := newSyncHandler(service, cfg.SyncAudience, validate)
		if err != nil {
			return nil, err
		}
		outer.Handle(SyncPath, sync)
	}
	outer.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	})
	outer.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "ok\n")
		}
	})
	return outer, nil
}
