package live

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	refreshInterval = time.Second
	readTimeout     = 5 * time.Second
	cacheControl    = "public, max-age=1, must-revalidate"
)

// Config pins one index provider and the only permitted absolute media origin.
// Read must return owned bytes and honor its context. The handler never passes
// client URLs/query parameters to Read and never reads or proxies asset bytes.
type Config struct {
	Read        func(context.Context) ([]byte, error)
	AssetOrigin string
	Logger      *slog.Logger
}

type representation struct {
	body []byte
	etag string
}
type snapshot struct {
	index    representation
	sessions map[string]representation
}
type result struct {
	snapshot *snapshot
	err      error
}
type flight struct {
	done   chan struct{}
	result result
}

type handler struct {
	config   Config
	mu       sync.Mutex
	current  result
	expires  time.Time
	inflight *flight
	now      func() time.Time
	timeout  time.Duration
}

// NewHandler serves GET/HEAD /api/v1/live and /api/v1/live/{id}. It does not read
// at construction; absent configuration is the caller's responsibility.
func NewHandler(config Config) (http.Handler, error) {
	if config.Read == nil {
		return nil, fmt.Errorf("live index reader is required")
	}
	if _, err := validateOrigin(config.AssetOrigin); err != nil {
		return nil, err
	}
	return &handler{config: config, now: time.Now, timeout: readTimeout}, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	id, ok := route(r)
	if !ok {
		writeError(w, r, http.StatusNotFound, "not_found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	value := h.load(r.Context())
	if value.err != nil {
		w.Header().Set("Retry-After", "1")
		writeError(w, r, http.StatusServiceUnavailable, "live_unavailable")
		return
	}
	resource := value.snapshot.index
	if id != "" {
		var exists bool
		resource, exists = value.snapshot.sessions[id]
		if !exists {
			writeError(w, r, http.StatusNotFound, "not_found")
			return
		}
	}
	w.Header().Set("Cache-Control", cacheControl)
	w.Header().Set("ETag", resource.etag)
	if matchesETag(strings.Join(r.Header.Values("If-None-Match"), ","), resource.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(resource.body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(resource.body)
	}
}

func route(r *http.Request) (string, bool) {
	if r.URL.EscapedPath() != r.URL.Path {
		return "", false
	}
	if r.URL.Path == "/api/v1/live" {
		return "", true
	}
	id, ok := strings.CutPrefix(r.URL.Path, "/api/v1/live/")
	return id, ok && validID(id)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, message string) {
	body, _ := json.Marshal(map[string]string{"error": message})
	body = append(body, '\n')
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Del("ETag")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

func (h *handler) load(ctx context.Context) result {
	if err := ctx.Err(); err != nil {
		return result{err: err}
	}
	h.mu.Lock()
	if h.now().Before(h.expires) {
		value := h.current
		h.mu.Unlock()
		return value
	}
	f := h.inflight
	if f == nil {
		f = &flight{done: make(chan struct{})}
		h.inflight = f
		// Every waiter has its own cancellation, including the request that
		// starts the refresh. One disconnected client cannot abort others.
		go h.refresh(context.WithoutCancel(ctx), f)
	}
	h.mu.Unlock()
	select {
	case <-ctx.Done():
		return result{err: ctx.Err()}
	case <-f.done:
		return f.result
	}
}

func (h *handler) refresh(parent context.Context, f *flight) {
	ctx, cancel := context.WithTimeout(parent, h.timeout)
	defer cancel()
	loaded := make(chan result, 1)
	go func() {
		defer func() {
			if recover() != nil {
				loaded <- result{err: fmt.Errorf("live index reader panicked")}
			}
		}()
		data, err := h.config.Read(ctx)
		if err != nil {
			loaded <- result{err: err}
			return
		}
		index, err := ParseIndex(data, h.config.AssetOrigin)
		if err != nil {
			loaded <- result{err: err}
			return
		}
		if err := ctx.Err(); err != nil {
			loaded <- result{err: err}
			return
		}
		value, err := encodeSnapshot(index)
		loaded <- result{snapshot: value, err: err}
	}()
	var value result
	timedOut := false
	select {
	case value = <-loaded:
		if err := ctx.Err(); err != nil {
			value = result{err: err}
		}
	case <-ctx.Done():
		value = result{err: ctx.Err()}
		timedOut = true
	}
	if value.err != nil && h.config.Logger != nil {
		h.config.Logger.ErrorContext(ctx, "live index refresh failed", "error", value.err)
	}
	h.mu.Lock()
	h.current = value
	h.expires = h.now().Add(refreshInterval)
	f.result = value
	if !timedOut {
		h.inflight = nil
	}
	close(f.done)
	h.mu.Unlock()
	if timedOut {
		// A broken provider ignoring cancellation must not create one leaked
		// reader per request/second. Keep its single flight occupied until it
		// actually returns, while all clients receive the shared failure.
		<-loaded
		h.mu.Lock()
		if h.inflight == f {
			h.inflight = nil
		}
		h.mu.Unlock()
	}
}

func encodeSnapshot(index *Index) (*snapshot, error) {
	value, err := encode(index)
	if err != nil {
		return nil, err
	}
	if len(value.body) > MaxIndexBytes {
		return nil, fmt.Errorf("encoded live index exceeds byte limit")
	}
	result := &snapshot{index: value, sessions: make(map[string]representation, len(index.Sessions))}
	for _, session := range index.Sessions {
		value, err := encode(session)
		if err != nil {
			return nil, err
		}
		result.sessions[session.ID] = value
	}
	return result, nil
}

func encode(value any) (representation, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return representation{}, err
	}
	body = append(body, '\n')
	sum := sha256.Sum256(body)
	return representation{body: body, etag: `"` + hex.EncodeToString(sum[:]) + `"`}, nil
}

// Parse quoted entity tags instead of splitting on commas inside opaque tags.
// If-None-Match on GET/HEAD uses weak comparison, including W/ validators.
func matchesETag(header, current string) bool {
	header = strings.TrimSpace(header)
	if header == "*" {
		return true
	}
	matched := false
	for header != "" {
		if strings.HasPrefix(header, "W/") {
			header = header[2:]
		}
		if len(header) < 2 || header[0] != '"' {
			return false
		}
		end := 1
		for end < len(header) && header[end] != '"' {
			if header[end] < 0x21 || header[end] == 0x7f {
				return false
			}
			end++
		}
		if end == len(header) {
			return false
		}
		matched = matched || header[:end+1] == current
		header = strings.TrimSpace(header[end+1:])
		if header == "" {
			return matched
		}
		if header[0] != ',' {
			return false
		}
		header = strings.TrimSpace(header[1:])
		if header == "" {
			return false
		}
	}
	return false
}
