package generation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
	"golang.org/x/time/rate"
)

type Handler struct {
	service   *Service
	principal func(*http.Request) (Principal, error)
	limiter   *rate.Limiter
	http.Handler
}

// NewHandler preserves the operator-only API. The callback must refresh the
// operator account; browser headers or body fields are not authority.
func NewHandler(service *Service, user func(*http.Request) (string, error)) (*Handler, error) {
	if user == nil {
		return nil, ErrInvalid
	}
	return newPrincipalHandler(service, func(r *http.Request) (Principal, error) {
		id, err := user(r)
		if id == "" {
			return Principal{}, err
		}
		return OperatorPrincipal(id), err
	}, "/api/v1/generation")
}

// NewPublisherHandler serves only the authenticated staff member's account.
// The authenticator must load current account/staff state on every request.
func NewPublisherHandler(service *Service, auth func(*http.Request) (Principal, error)) (*Handler, error) {
	if auth == nil {
		return nil, ErrInvalid
	}
	return newPrincipalHandler(service, publisherAuthority(auth), "/api/v1/publishers/{account}/generation")
}

func publisherAuthority(auth func(*http.Request) (Principal, error)) func(*http.Request) (Principal, error) {
	return func(r *http.Request) (Principal, error) {
		p, err := auth(r)
		if err != nil || p.Kind == "" {
			return p, err
		}
		if p.Kind != "staff" || !p.valid() {
			return Principal{}, ErrPublisherForbidden
		}
		if string(p.Account.ID) != r.PathValue("account") {
			return Principal{}, ErrNotFound
		}
		return p, nil
	}
}

func newHandlerBase(service *Service, auth func(*http.Request) (Principal, error)) (*Handler, error) {
	if service == nil || auth == nil {
		return nil, ErrInvalid
	}
	if err := service.Config.Validate(); err != nil {
		return nil, err
	}
	if service.Store == nil || service.Provider == nil {
		return nil, ErrInvalid
	}
	return &Handler{service: service, principal: auth, limiter: rate.NewLimiter(rate.Every(time.Second), 4)}, nil
}

func newPrincipalHandler(service *Service, auth func(*http.Request) (Principal, error), prefix string) (*Handler, error) {
	h, err := newHandlerBase(service, auth)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+prefix+"/presets", h.presets)
	mux.HandleFunc("POST "+prefix+"/jobs", h.create)
	mux.HandleFunc("GET "+prefix+"/jobs", h.list)
	mux.HandleFunc("GET "+prefix+"/jobs/{job}", h.get)
	mux.HandleFunc("DELETE "+prefix+"/jobs/{job}", h.cancel)
	mux.HandleFunc("PUT "+prefix+"/jobs/{job}/publication", h.publication)
	protected := http.NewCrossOriginProtection().Handler(mux)
	h.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		protected.ServeHTTP(w, r)
	})
	return h, nil
}

func (h *Handler) write(w http.ResponseWriter, r *http.Request, code int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(value)
	}
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	code, message := http.StatusServiceUnavailable, "generation_unavailable"
	switch {
	case errors.Is(err, ErrPublisherForbidden):
		code, message = 403, "publisher_access_denied"
	case errors.Is(err, ErrForbidden):
		code, message = 403, "admin_required"
	case errors.Is(err, ErrNotFound):
		code, message = 404, "not_found"
	case errors.Is(err, ErrArtifacts):
		code, message = 409, "artifacts_not_ready"
	case errors.Is(err, ErrInvalid):
		code, message = 400, "invalid_request"
	case errors.Is(err, ErrBusy):
		code, message = 409, "generation_busy"
	case errors.Is(err, ErrConflict):
		code, message = 409, "request_conflict"
	case errors.Is(err, ErrBudget):
		code, message = 409, "generation_budget_exhausted"
	case errors.Is(err, ErrExpired):
		code, message = 410, "generation_window_ended"
	default:
		if h.service.Logger != nil {
			h.service.Logger.ErrorContext(r.Context(), "generation request failed", "error", err, "path", r.URL.Path)
		}
	}
	h.write(w, r, code, map[string]string{"error": message})
}

func (h *Handler) authority(w http.ResponseWriter, r *http.Request, permission publisher.Permission) (Principal, bool) {
	p, err := h.principal(r)
	if err != nil {
		h.fail(w, r, err)
		return Principal{}, false
	}
	if p.Kind == "" {
		h.write(w, r, 401, map[string]string{"error": "not_authenticated"})
		return Principal{}, false
	}
	if !p.allows(permission) {
		h.fail(w, r, ErrPublisherForbidden)
		return Principal{}, false
	}
	if !h.limiter.Allow() {
		w.Header().Set("Retry-After", "1")
		h.write(w, r, 429, map[string]string{"error": "rate_limited"})
		return Principal{}, false
	}
	return p, true
}

func (h *Handler) presets(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authority(w, r, publisher.ReadPresets)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, ErrInvalid)
		return
	}
	rows := make([]map[string]string, 0, len(h.service.Config.Presets))
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	presets, err := h.service.PresetsFor(ctx, principal)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	for _, p := range presets {
		rows = append(rows, map[string]string{"id": p.ID, "mode": p.ModeName(), "title": p.Title, "sport": p.Sport, "description": p.Description, "source": p.Source, "license": p.License})
	}
	h.write(w, r, 200, map[string]any{"available": true, "presets": rows, "limits": map[string]any{"concurrent_jobs": 1, "runtime_seconds": int(MaxRuntime / time.Second), "attempts": h.service.Config.Attempts}})
}

type jobView struct {
	Progress  *Progress `json:"progress,omitempty"`
	Published bool      `json:"published"`
	ID        string    `json:"id"`
	Preset    string    `json:"preset"`
	Mode      string    `json:"mode"`
	State     State     `json:"state"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func viewJob(j Job) jobView {
	return jobView{
		Progress:  j.Progress,
		Published: j.Published, ID: j.ID, Preset: j.Preset, Mode: j.Snapshot.ModeName(), State: j.State,
		CreatedAt: j.CreatedAt, UpdatedAt: j.UpdatedAt,
	}
}

func (h *Handler) response(job Job) map[string]any {
	v := map[string]any{"job": viewJob(job)}
	if job.Snapshot.ModeName() == "live" {
		v["viewer_url"] = "/demo/?live=" + job.ID
	} else if job.State == Succeeded {
		v["viewer_url"] = "/demo/?clip=generated-" + job.ID
		v["scene_url"] = h.service.Config.MediaOrigin + "/generated/" + job.ID + "/scene.json"
	}
	return v
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authority(w, r, publisher.CreateGeneration)
	if !ok {
		return
	}
	contentType, _, contentTypeError := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.URL.RawQuery != "" || contentTypeError != nil || contentType != "application/json" {
		h.fail(w, r, ErrInvalid)
		return
	}
	var input struct {
		Preset    string `json:"preset"`
		RequestID string `json:"request_id"`
	}
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if d.Decode(&input) != nil || d.Decode(new(any)) != io.EOF {
		h.fail(w, r, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	job, err := h.service.CreateFor(ctx, principal, input.Preset, input.RequestID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.write(w, r, 202, h.response(job))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authority(w, r, publisher.ReadGeneration)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	jobs, err := h.service.ListFor(ctx, principal)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	rows := make([]jobView, 0, len(jobs))
	for _, job := range jobs {
		rows = append(rows, viewJob(job))
	}
	h.write(w, r, 200, map[string]any{"jobs": rows})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authority(w, r, publisher.ReadGeneration)
	if !ok {
		return
	}
	if !requestID.MatchString(r.PathValue("job")) || r.URL.RawQuery != "" {
		h.fail(w, r, ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	job, err := h.service.GetFor(ctx, principal, r.PathValue("job"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.write(w, r, 200, h.response(job))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authority(w, r, publisher.CancelGeneration)
	if !ok {
		return
	}
	if !requestID.MatchString(r.PathValue("job")) || r.URL.RawQuery != "" {
		h.fail(w, r, ErrNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	job, err := h.service.CancelFor(ctx, principal, r.PathValue("job"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.write(w, r, 202, h.response(job))
}

// ClipHandler exposes only completed, reviewed-preset output metadata. It adds
// deep links for generated previews without mutating the immutable public feed.
func (h *Handler) ClipHandler(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, generated := strings.CutPrefix(r.PathValue("clip"), "generated-")
		if !generated {
			fallback.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			h.write(w, r, 405, map[string]string{"error": "method_not_allowed"})
			return
		}
		if !requestID.MatchString(id) || r.URL.RawQuery != "" {
			h.fail(w, r, ErrNotFound)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		clip, err := h.service.CompletedClip(ctx, "generated-"+id)
		if err != nil {
			h.fail(w, r, err)
			return
		}
		h.write(w, r, 200, clip)
	})
}

func (h *Handler) publication(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authority(w, r, publisher.ReadGeneration)
	if !ok {
		return
	}
	if !requestID.MatchString(r.PathValue("job")) || r.URL.RawQuery != "" {
		h.fail(w, r, ErrNotFound)
		return
	}
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		h.fail(w, r, ErrInvalid)
		return
	}
	var input struct {
		Published *bool `json:"published"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || decoder.Decode(new(any)) != io.EOF || input.Published == nil {
		h.fail(w, r, ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	job, err := h.service.SetPublicationFor(ctx, principal, r.PathValue("job"), *input.Published)
	if err != nil {
		if errors.Is(err, ErrArtifacts) {
			if h.service.Logger != nil {
				h.service.Logger.WarnContext(ctx, "generation publication validation failed", "job_id", r.PathValue("job"), "error", err)
			}
			h.write(w, r, 409, map[string]string{"error": "publication_unavailable"})
			return
		}
		if !errors.Is(err, ErrPublisherForbidden) && !errors.Is(err, ErrNotFound) && !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrBusy) && !errors.Is(err, ErrBudget) && !errors.Is(err, ErrExpired) {
			if h.service.Logger != nil {
				h.service.Logger.ErrorContext(ctx, "generation publication failed", "job_id", r.PathValue("job"), "error", err)
			}
			h.write(w, r, 503, map[string]string{"error": "publication_unavailable"})
			return
		}
		h.fail(w, r, err)
		return
	}
	h.write(w, r, 200, h.response(job))
}
