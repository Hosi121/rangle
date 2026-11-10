// Package delivery serves validated, immutable clip revisions from memory or disk.
package delivery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Hosi121/rangle/internal/stream"
)

const immutableCacheControl = "public, max-age=31536000, immutable"

type resource struct {
	data        []byte
	contentType string
	etag        string
}

type handler struct {
	resources map[string]resource
}

// NewHandler validates and snapshots a clip revision and all its advertised
// assets. Callers must not mutate the inputs during construction; later changes
// cannot change the bytes served by the returned handler. It exposes no files.
// Reusing a clip/revision URL for different bytes across handler lifetimes is
// the publisher's responsibility.
func NewHandler(manifest stream.Manifest, assets map[string][]byte) (http.Handler, error) {
	manifest.Representations = append([]stream.Representation(nil), manifest.Representations...)
	for i := range manifest.Representations {
		manifest.Representations[i].Segments = append([]stream.Segment(nil), manifest.Representations[i].Segments...)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate manifest: %w", err)
	}

	snapshot := make(map[string][]byte, len(assets))
	for url, data := range assets {
		snapshot[url] = bytes.Clone(data)
	}
	resources := make(map[string]resource, len(snapshot)+1)
	addAsset := func(url string, size int64, checksum string) error {
		data, ok := snapshot[url]
		if !ok {
			return fmt.Errorf("missing asset %q", url)
		}
		if int64(len(data)) != size {
			return fmt.Errorf("asset %q has %d bytes, want %d", url, len(data), size)
		}
		if checksum != "" {
			want, err := hex.DecodeString(checksum)
			if err != nil {
				return fmt.Errorf("asset %q has invalid SHA256: %w", url, err)
			}
			sum := sha256.Sum256(data)
			if !bytes.Equal(sum[:], want) {
				return fmt.Errorf("asset %q SHA256 does not match manifest", url)
			}
		}
		resources[url] = newResource(data, "application/octet-stream")
		delete(snapshot, url)
		return nil
	}
	for _, representation := range manifest.Representations {
		if representation.InitURL != "" {
			if err := addAsset(representation.InitURL, representation.InitSizeBytes, ""); err != nil {
				return nil, err
			}
		}
		for _, segment := range representation.Segments {
			if err := addAsset(segment.URL, segment.SizeBytes, segment.SHA256); err != nil {
				return nil, err
			}
		}
	}
	for url := range snapshot {
		return nil, fmt.Errorf("unadvertised asset %q", url)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	resources[manifest.URL()] = newResource(data, "application/json")
	return &handler{resources: resources}, nil
}

func newResource(data []byte, contentType string) resource {
	sum := sha256.Sum256(data)
	return resource{data: data, contentType: contentType, etag: `"sha256-` + hex.EncodeToString(sum[:]) + `"`}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w = &cacheResponseWriter{ResponseWriter: w, head: r.Method == http.MethodHead}
	// EscapedPath keeps percent-encoded aliases from exposing a second URL for
	// an advertised resource. No path cleaning, filesystem lookup, or redirect
	// is involved in this exact allowlist lookup.
	asset, ok := h.resources[r.URL.EscapedPath()]
	if !ok || r.URL.RawQuery != "" || r.URL.ForceQuery {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", asset.contentType)
	w.Header().Set("ETag", asset.etag)
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(asset.data))
}

// Keep cache policy independent of ServeContent's error-header behavior (and
// the httpservecontentkeepheaders GODEBUG setting). The ETag must be installed
// before ServeContent evaluates request preconditions.
type cacheResponseWriter struct {
	http.ResponseWriter
	wroteHeader  bool
	head         bool
	cacheControl string
}

func (w *cacheResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	switch status {
	case http.StatusOK, http.StatusPartialContent, http.StatusNotModified:
		policy := w.cacheControl
		if policy == "" {
			policy = immutableCacheControl
		}
		w.Header().Set("Cache-Control", policy)
	default:
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Del("ETag")
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *cacheResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.head {
		return len(data), nil
	}
	return w.ResponseWriter.Write(data)
}
