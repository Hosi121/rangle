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

	"github.com/Hosi121/rangle/internal/sports"
)

const feedPath = "/api/v1/clips"
const catalogSHAHeader = "X-Rangle-Catalog-SHA256"

type PublicClip struct {
	Context     *sports.Context `json:"context,omitempty" firestore:"context,omitempty"`
	ID          string          `json:"id"`
	Revision    string          `json:"revision"`
	Title       string          `json:"title"`
	Sport       string          `json:"sport"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags,omitempty"`
	Codec       string          `json:"codec,omitempty"`
	ManifestURL string          `json:"manifest_url,omitempty"`
	PreviewURL  string          `json:"preview_url,omitempty"`
	SceneURL    string          `json:"scene_url,omitempty"`
}

type clipView = PublicClip

type handler struct {
	extra         []jsonDocument
	extraSearch   []searchEntry
	clips         []jsonDocument
	byID          map[string]int
	snapshot      string
	catalogSHA256 string
	search        []searchEntry
}

type jsonDocument struct {
	data []byte
	etag string
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
	h := &handler{clips: make([]jsonDocument, len(c.clips)), byID: make(map[string]int, len(c.clips)), catalogSHA256: c.SourceSHA256()}
	snapshot := sha256.Sum256([]byte(c.snapshot + "\n" + assetOrigin))
	h.snapshot = hex.EncodeToString(snapshot[:])
	for i, entry := range c.clips {
		tags := append([]string(nil), entry.Tags...)
		if len(tags) == 0 && validTag(entry.Sport) {
			tags = []string{strings.ToLower(entry.Sport)}
		}
		view := clipView{
			ID: entry.Manifest.ClipID, Revision: entry.Manifest.Revision,
			Title: entry.Title, Sport: entry.Sport, Description: entry.Description,
			Codec: entry.Manifest.Codec, ManifestURL: resolve(entry.Manifest.URL()),
			PreviewURL: resolve(entry.PreviewURL), SceneURL: resolve(entry.SceneURL),
			Tags: tags, Context: entry.Context,
		}
		data, err := json.Marshal(view)
		if err != nil {
			return nil, fmt.Errorf("encode clip %q: %w", entry.Manifest.ClipID, err)
		}
		h.clips[i] = newJSONDocument(data)
		h.byID[entry.Manifest.ClipID] = i
		h.search = append(h.search, newSearchEntry(view))
	}
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w = cacheWriter{w}
	w.Header().Set(catalogSHAHeader, h.catalogSHA256)
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
		selection, snapshot, err := h.selectClips(query)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_query")
			return
		}
		limit, start, err := h.page(query, snapshot, selection.len())
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_pagination")
			return
		}
		end := min(start+limit, selection.len())
		writeDocument(w, r, h.feed(selection, snapshot, start, end))
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
		writeDocument(w, r, h.clips[i])
	default:
		writeError(w, r, http.StatusNotFound, "not_found")
	}
}

// Reuse the startup JSON without caching arbitrary page/limit combinations.
// Memory stays proportional to catalog metadata, rather than request history.
func (h *handler) feed(clips clipSelection, snapshot string, start, end int) jsonDocument {
	cursor := ""
	size := len(`{"clips":[]}`) + max(0, end-start-1)
	for index := start; index < end; index++ {
		size += len(clips.at(index).data)
	}
	if end < clips.len() {
		cursor = base64.RawURLEncoding.EncodeToString([]byte(snapshot + ":" + strconv.Itoa(end)))
		size += len(`,"next_cursor":""`) + len(cursor)
	}
	data := make([]byte, 0, size)
	data = append(data, `{"clips":[`...)
	for index := start; index < end; index++ {
		if index > start {
			data = append(data, ',')
		}
		data = append(data, clips.at(index).data...)
	}
	data = append(data, ']')
	if cursor != "" {
		data = append(data, `,"next_cursor":"`...)
		data = append(data, cursor...)
		data = append(data, '"')
	}
	data = append(data, '}')
	return newJSONDocument(data)
}

// Two shared spans avoid copying the static catalog per generated-feed request.
type clipSelection struct{ first, second []jsonDocument }

func (s clipSelection) len() int { return len(s.first) + len(s.second) }
func (s clipSelection) at(index int) jsonDocument {
	if index < len(s.first) {
		return s.first[index]
	}
	return s.second[index-len(s.first)]
}

func (h *handler) page(query url.Values, snapshot string, count int) (limit, start int, err error) {
	limit = 10
	for key, values := range query {
		if (key != "limit" && key != "cursor" && key != "q" && key != "tag" && key != "publisher") || len(values) != 1 || values[0] == "" {
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
		cursorSnapshot, offset, ok := strings.Cut(string(decoded), ":")
		start, err = strconv.Atoi(offset)
		if !ok || cursorSnapshot != snapshot || err != nil || start < 1 || start >= count {
			return 0, 0, fmt.Errorf("invalid or stale cursor")
		}
	}
	return limit, start, nil
}

func newJSONDocument(data []byte) jsonDocument {
	sum := sha256.Sum256(data)
	return jsonDocument{data: data, etag: `"sha256-` + hex.EncodeToString(sum[:]) + `"`}
}

func writeDocument(w http.ResponseWriter, r *http.Request, document jsonDocument) {
	w.Header().Set("ETag", document.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(document.data))
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
