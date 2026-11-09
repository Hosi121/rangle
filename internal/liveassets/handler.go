// Package liveassets serves immutable, locally generated live models for development.
// Production models go directly from the producer to GCS/CDN.
package liveassets

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

var assetPath = regexp.MustCompile(`^/live-assets/([A-Za-z0-9_-]{1,64})/([0-9]{6})-([0-9a-f]{16})/(scene\.json|model\.rgs)$`)

const maxFiles = 512

type asset struct {
	file     *os.File
	size     int64
	modified time.Time
	etag     string
}

type Handler struct {
	mu     sync.RWMutex
	root   *os.Root
	files  map[string]asset
	closed bool
}

func New(directory string) (*Handler, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	return &Handler{root: root, files: make(map[string]asset)}, nil
}

func (h *Handler) open(target string, match []string) (asset, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return asset{}, fmt.Errorf("live assets closed")
	}
	if a, ok := h.files[target]; ok {
		return a, nil
	}
	if len(h.files) >= maxFiles {
		return asset{}, fmt.Errorf("local live asset limit reached")
	}
	relative := strings.TrimPrefix(target, "/live-assets/")
	parts := strings.Split(relative, "/")
	for i := range parts {
		info, err := h.root.Lstat(strings.Join(parts[:i+1], "/"))
		if err != nil {
			return asset{}, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return asset{}, fmt.Errorf("live assets cannot contain symlinks")
		}
	}
	f, err := h.root.Open(relative)
	if err != nil {
		return asset{}, err
	}
	valid := false
	defer func() {
		if !valid {
			f.Close()
		}
	}()
	info, err := f.Stat()
	if err != nil {
		return asset{}, err
	}
	limit := int64(32 << 20)
	if match[4] == "scene.json" {
		limit = 64 << 10
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > limit {
		return asset{}, fmt.Errorf("invalid live asset size or type")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, io.LimitReader(f, limit+1)); err != nil {
		return asset{}, err
	}
	checksum := hex.EncodeToString(digest.Sum(nil))
	if match[4] == "model.rgs" && !strings.HasPrefix(checksum, match[3]) {
		return asset{}, fmt.Errorf("live model checksum differs from its revision")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return asset{}, err
	}
	a := asset{file: f, size: info.Size(), modified: info.ModTime(), etag: `"` + checksum + `"`}
	h.files[target] = a
	valid = true
	return a, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	match := assetPath.FindStringSubmatch(r.URL.Path)
	if match == nil || r.URL.RawPath != "" {
		http.NotFound(w, r)
		return
	}
	a, err := h.open(r.URL.Path, match)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	info, err := a.file.Stat()
	if err != nil || info.Size() != a.size || !info.ModTime().Equal(a.modified) {
		http.Error(w, "live asset changed", http.StatusServiceUnavailable)
		return
	}
	// SectionReader has an independent offset: Range requests may run concurrently.
	w.Header().Set("ETag", a.etag)
	w.Header().Set("Cache-Control", "public,max-age=31536000,immutable,no-transform")
	contentType := "application/octet-stream"
	if match[4] == "scene.json" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, match[4], a.modified, io.NewSectionReader(a.file, 0, a.size))
}

// Close is called after active HTTP requests have drained.
func (h *Handler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	var result error
	for _, a := range h.files {
		result = errors.Join(result, a.file.Close())
	}
	return errors.Join(result, h.root.Close())
}
