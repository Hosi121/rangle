package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const feedPath = "/api/v1/clips"

type clipView struct {
	ID          string `json:"id"`
	Revision    string `json:"revision"`
	Title       string `json:"title"`
	Sport       string `json:"sport"`
	Description string `json:"description"`
	Codec       string `json:"codec"`
	ManifestURL string `json:"manifest_url"`
	PreviewURL  string `json:"preview_url,omitempty"`
	SceneURL    string `json:"scene_url,omitempty"`
}

type handler struct {
	clips    []clipView
	byID     map[string]int
	snapshot string
}

// NewHandler serves a public, read-only feed and clip details. assetOrigin is an
// optional HTTP(S) origin (no path, credentials, query, or fragment), normally a
// CDN. Relative manifest URLs inside the transport document keep their identity.
func NewHandler(c *Catalog, assetOrigin string) (http.Handler, error) {
	if c == nil {
		return nil, fmt.Errorf("catalog is required")
	}
	if assetOrigin != "" {
		u, err := url.Parse(assetOrigin)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil ||
			(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			return nil, fmt.Errorf("asset origin must be an HTTP(S) origin without a path or credentials")
		}
		assetOrigin = u.Scheme + "://" + u.Host
	}
	resolve := func(value string) string {
		if value != "" && strings.HasPrefix(value, "/") {
			return assetOrigin + value
		}
		return value
	}
	h := &handler{clips: make([]clipView, len(c.clips)), byID: make(map[string]int, len(c.clips))}
	snapshot := sha256.Sum256([]byte(c.snapshot + "\n" + assetOrigin))
	h.snapshot = hex.EncodeToString(snapshot[:])
	for i, entry := range c.clips {
		h.clips[i] = clipView{
			ID: entry.Manifest.ClipID, Revision: entry.Manifest.Revision,
			Title: entry.Title, Sport: entry.Sport, Description: entry.Description,
			Codec: entry.Manifest.Codec, ManifestURL: resolve(entry.Manifest.URL()),
			PreviewURL: resolve(entry.PreviewURL), SceneURL: resolve(entry.SceneURL),
		}
		h.byID[entry.Manifest.ClipID] = i
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w = cacheWriter{w}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_query")
		return
	}
	switch {
	case r.URL.EscapedPath() == feedPath:
		limit, start, err := h.page(query)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_pagination")
			return
		}
		end := min(start+limit, len(h.clips))
		page := struct {
			Clips      []clipView `json:"clips"`
			NextCursor string     `json:"next_cursor,omitempty"`
		}{Clips: h.clips[start:end]}
		if end < len(h.clips) {
			page.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(h.snapshot + ":" + strconv.Itoa(end)))
		}
		writeJSON(w, r, page)
	case strings.HasPrefix(r.URL.EscapedPath(), feedPath+"/"):
		id := strings.TrimPrefix(r.URL.EscapedPath(), feedPath+"/")
		i, ok := h.byID[id]
		if !ok {
			writeError(w, r, http.StatusNotFound, "clip_not_found")
			return
		}
		if len(query) != 0 {
			writeError(w, r, http.StatusBadRequest, "invalid_query")
			return
		}
		writeJSON(w, r, h.clips[i])
	default:
		writeError(w, r, http.StatusNotFound, "not_found")
	}
}

func (h *handler) page(query url.Values) (limit, start int, err error) {
	limit = 10
	for key, values := range query {
		if (key != "limit" && key != "cursor") || len(values) != 1 || values[0] == "" {
			return 0, 0, fmt.Errorf("invalid pagination query")
		}
	}
	if value := query.Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 50 {
			return 0, 0, fmt.Errorf("limit must be between 1 and 50")
		}
	}
	if value := query.Get("cursor"); value != "" {
		if len(value) > 128 {
			return 0, 0, fmt.Errorf("invalid cursor")
		}
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid cursor")
		}
		snapshot, offset, ok := strings.Cut(string(decoded), ":")
		start, err = strconv.Atoi(offset)
		if !ok || snapshot != h.snapshot || err != nil || start < 1 || start >= len(h.clips) {
			return 0, 0, fmt.Errorf("invalid or stale cursor")
		}
	}
	return limit, start, nil
}

func writeJSON(w http.ResponseWriter, r *http.Request, value any) {
	// The validated snapshot contains only JSON-safe values and bounded strings.
	data, err := json.Marshal(value)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "encode_failed")
		return
	}
	sum := sha256.Sum256(data)
	w.Header().Set("ETag", `"sha256-`+hex.EncodeToString(sum[:])+`"`)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(data))
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code string) {
	data, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{code})
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}

type cacheWriter struct{ http.ResponseWriter }

func (w cacheWriter) WriteHeader(status int) {
	if status == http.StatusOK || status == http.StatusPartialContent || status == http.StatusNotModified {
		w.Header().Set("Cache-Control", "public, max-age=15, stale-while-revalidate=30")
	} else {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Del("ETag")
	}
	w.ResponseWriter.WriteHeader(status)
}
