package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Hosi121/rangle/internal/catalog"
	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

// Remote contains no provider or database capability. Only the manager owns
// those clients. Tokens are sent separately for platform and app verification.
type Remote struct {
	base   string
	client *http.Client
	tokens oauth2.TokenSource
}

func NewRemote(ctx context.Context, base string) (*Remote, error) {
	if !managerAudience(base) {
		return nil, fmt.Errorf("generation manager URL must be an HTTPS base URL")
	}
	tokens, err := idtoken.NewTokenSource(ctx, base)
	if err != nil {
		return nil, fmt.Errorf("generation manager credentials: %w", err)
	}
	return &Remote{base: base, tokens: tokens, client: &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *Remote) call(ctx context.Context, method, path, owner, contentType string, body io.Reader) (int, http.Header, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return 0, nil, nil, err
	}
	token, err := c.tokens.Token()
	if err != nil {
		return 0, nil, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)
	request.Header.Set(serviceAuthorizationHeader, "Bearer "+token.AccessToken)
	if owner != "" {
		request.Header.Set(ownerHeader, owner)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	response, err := c.client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, (128<<10)+1))
	if err != nil || len(payload) > 128<<10 {
		return 0, nil, nil, fmt.Errorf("generation manager response exceeds bounds or could not be read")
	}
	return response.StatusCode, response.Header, payload, nil
}

func (c *Remote) PublicClips(ctx context.Context) ([]catalog.PublicClip, error) {
	code, _, payload, err := c.call(ctx, http.MethodGet, publicClipsPath, "", "", nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Clips []catalog.PublicClip `json:"clips"`
	}
	if code != 200 || json.Unmarshal(payload, &result) != nil || len(result.Clips) > MaxAttempts {
		return nil, fmt.Errorf("generation public registry unavailable")
	}
	for _, clip := range result.Clips {
		if err := clip.ValidateGenerated(); err != nil {
			return nil, err
		}
	}
	return result.Clips, nil
}

func (c *Remote) CompletedClipExists(ctx context.Context, id string) (bool, error) {
	_, err := c.CompletedClip(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (c *Remote) CompletedClip(ctx context.Context, id string) (catalog.PublicClip, error) {
	raw, ok := strings.CutPrefix(id, "generated-")
	if !ok || !requestID.MatchString(raw) {
		return catalog.PublicClip{}, ErrNotFound
	}
	code, _, payload, err := c.call(ctx, http.MethodGet, "/api/v1/clips/"+id, "", "", nil)
	if err != nil {
		return catalog.PublicClip{}, err
	}
	if code == 404 {
		return catalog.PublicClip{}, ErrNotFound
	}
	var clip catalog.PublicClip
	if code != 200 || json.Unmarshal(payload, &clip) != nil || clip.ID != id || clip.ValidateGenerated() != nil {
		return clip, fmt.Errorf("generation completed clip unavailable")
	}
	return clip, nil
}

type RemoteHandler struct {
	remote *Remote
	http.Handler
}

// NewRemoteHandler keeps browser authentication and CSRF at the public edge.
// Caller-supplied headers, cookies and provider fields are never trusted by the
// manager; the forwarded owner is derived only from the refreshed admin account.
func NewRemoteHandler(remote *Remote, user func(*http.Request) (string, error)) (*RemoteHandler, error) {
	if remote == nil || user == nil {
		return nil, ErrInvalid
	}
	h := &RemoteHandler{remote: remote}
	mux := http.NewServeMux()
	forward := func(w http.ResponseWriter, r *http.Request) {
		owner, err := user(r)
		if errors.Is(err, ErrForbidden) {
			remoteError(w, r, 403, "admin_required")
			return
		}
		if err != nil {
			remoteError(w, r, 503, "generation_unavailable")
			return
		}
		if owner == "" {
			remoteError(w, r, 401, "not_authenticated")
			return
		}
		h.forward(w, r, owner)
	}
	for _, route := range []string{"GET /api/v1/generation/presets", "GET /api/v1/generation/jobs", "POST /api/v1/generation/jobs", "GET /api/v1/generation/jobs/{job}", "DELETE /api/v1/generation/jobs/{job}", "PUT /api/v1/generation/jobs/{job}/publication"} {
		mux.HandleFunc(route, forward)
	}
	protected := http.NewCrossOriginProtection().Handler(mux)
	h.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		protected.ServeHTTP(w, r)
	})
	return h, nil
}

func remoteError(w http.ResponseWriter, r *http.Request, code int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
	}
}

func (h *RemoteHandler) forward(w http.ResponseWriter, r *http.Request, owner string) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		remoteError(w, r, 400, "invalid_request")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	code, headers, payload, err := h.remote.call(ctx, r.Method, r.URL.RequestURI(), owner, r.Header.Get("Content-Type"), bytes.NewReader(body))
	if err != nil {
		remoteError(w, r, 503, "generation_unavailable")
		return
	}
	for _, name := range []string{"Content-Type", "Cache-Control", "X-Content-Type-Options", "Allow", "Retry-After"} {
		if value := headers.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.WriteHeader(code)
	if r.Method != http.MethodHead {
		_, _ = w.Write(payload)
	}
}

func (h *RemoteHandler) ClipHandler(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, generated := strings.CutPrefix(r.PathValue("clip"), "generated-")
		if !generated {
			fallback.ServeHTTP(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			remoteError(w, r, 405, "method_not_allowed")
			return
		}
		if !requestID.MatchString(id) || r.URL.RawQuery != "" {
			remoteError(w, r, 404, "not_found")
			return
		}
		h.forward(w, r, "")
	})
}
