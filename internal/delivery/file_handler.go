package delivery

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Hosi121/rangle/internal/stream"
)

const fileCacheControl = immutableCacheControl + ", no-transform"

type fileResource struct {
	name string
	info os.FileInfo
	etag string
}

// FileHandler serves a validated, read-only publication. It holds a root handle
// and metadata, not file bodies or one open descriptor per asset. Every request
// opens and closes its own file and checks identity, size, and mtime before
// evaluating HTTP preconditions. Concurrent writes are unsupported; publish new
// bytes under a new revision instead of editing a live publication.
type FileHandler struct {
	root        *os.Root
	files       map[string]fileResource
	manifestURL string
	manifest    resource
	logger      *slog.Logger
	closeOnce   sync.Once
	closeError  error
}

// NewFileHandler snapshots the manifest and verifies every advertised file by
// streaming its bytes through SHA256. Asset paths are relative to assetRoot after
// removing their leading slash. Extra files are never exposed. Callers must not
// mutate the manifest or publication during construction or serving.
func NewFileHandler(manifest stream.Manifest, assetRoot string, logger *slog.Logger) (*FileHandler, error) {
	manifest.Representations = append([]stream.Representation(nil), manifest.Representations...)
	for i := range manifest.Representations {
		manifest.Representations[i].Segments = append([]stream.Segment(nil), manifest.Representations[i].Segments...)
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("validate manifest: %w", err)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encode manifest: %w", err)
	}
	root, err := os.OpenRoot(assetRoot)
	if err != nil {
		return nil, fmt.Errorf("open asset root: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	h := &FileHandler{root: root, files: make(map[string]fileResource), manifestURL: manifest.URL(), manifest: newResource(data, "application/json"), logger: logger.With("clip_id", manifest.ClipID, "revision", manifest.Revision)}
	for _, representation := range manifest.Representations {
		if representation.InitURL != "" {
			if err := h.verify(representation.InitURL, representation.InitSizeBytes, ""); err != nil {
				h.Close()
				return nil, err
			}
		}
		for _, segment := range representation.Segments {
			if err := h.verify(segment.URL, segment.SizeBytes, segment.SHA256); err != nil {
				h.Close()
				return nil, err
			}
		}
	}
	return h, nil
}

func (h *FileHandler) verify(url string, size int64, checksum string) error {
	name := strings.TrimPrefix(url, "/")
	// Check the kind before opening so a non-regular publication is rejected
	// without trying to read a directory or a named pipe.
	before, err := h.root.Stat(name)
	if err != nil {
		return fmt.Errorf("stat asset %q: %w", url, err)
	}
	if !before.Mode().IsRegular() || before.Size() != size {
		return fmt.Errorf("asset %q is not a regular file of %d bytes", url, size)
	}
	file, err := h.root.Open(name)
	if err != nil {
		return fmt.Errorf("open asset %q: %w", url, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened asset %q: %w", url, err)
	}
	if !sameFileState(before, opened) {
		return fmt.Errorf("asset %q changed during validation", url)
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.NewSectionReader(file, 0, size))
	if err != nil {
		return fmt.Errorf("hash asset %q: %w", url, err)
	}
	after, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat hashed asset %q: %w", url, err)
	}
	current, err := h.root.Stat(name)
	if err != nil {
		return fmt.Errorf("stat published asset %q: %w", url, err)
	}
	if n != size || !sameFileState(opened, after) || !sameFileState(after, current) {
		return fmt.Errorf("asset %q changed during validation", url)
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	if checksum != "" && !strings.EqualFold(checksum, digest) {
		return fmt.Errorf("asset %q SHA256 does not match manifest", url)
	}
	h.files[url] = fileResource{name: name, info: after, etag: `"sha256-` + digest + `"`}
	return nil
}

func sameFileState(a, b os.FileInfo) bool {
	return a.Mode().IsRegular() && b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// Close releases the root handle. The caller must drain HTTP requests first.
func (h *FileHandler) Close() error {
	h.closeOnce.Do(func() { h.closeError = h.root.Close() })
	return h.closeError
}

func (h *FileHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w = &cacheResponseWriter{ResponseWriter: w, head: r.Method == http.MethodHead, cacheControl: fileCacheControl}
	url := r.URL.EscapedPath()
	asset, exists := h.files[url]
	if (!exists && url != h.manifestURL) || r.URL.RawQuery != "" || r.URL.ForceQuery {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if url == h.manifestURL {
		w.Header().Set("Content-Type", h.manifest.contentType)
		w.Header().Set("ETag", h.manifest.etag)
		http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(h.manifest.data))
		return
	}
	file, err := h.openVerified(asset)
	if err != nil {
		h.unavailable(w, r, err)
		return
	}
	var logOnce sync.Once
	report := func(err error) {
		if err != nil && r.Context().Err() == nil {
			logOnce.Do(func() { h.logIO(r, err) })
		}
	}
	reader := &fileReader{SectionReader: io.NewSectionReader(file, 0, asset.info.Size()), report: report}
	defer func() { report(file.Close()) }()
	// These validators are installed only after metadata has been checked, so a
	// changed file cannot turn If-None-Match into a stale 304 response.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", asset.etag)
	http.ServeContent(w, r, "", asset.info.ModTime(), reader)
}

func (h *FileHandler) openVerified(asset fileResource) (*os.File, error) {
	info, err := h.root.Stat(asset.name)
	if err != nil {
		return nil, fmt.Errorf("stat published asset: %w", err)
	}
	if !sameFileState(asset.info, info) {
		return nil, fmt.Errorf("published asset metadata changed")
	}
	file, err := h.root.Open(asset.name)
	if err != nil {
		return nil, fmt.Errorf("open published asset: %w", err)
	}
	opened, err := file.Stat()
	if err == nil && !sameFileState(asset.info, opened) {
		err = fmt.Errorf("opened asset metadata changed")
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func (h *FileHandler) unavailable(w http.ResponseWriter, r *http.Request, err error) {
	h.logIO(r, err)
	http.Error(w, "asset unavailable", http.StatusServiceUnavailable)
}

func (h *FileHandler) logIO(r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "asset I/O failed", "asset_url", r.URL.EscapedPath(), "error", err)
}

// ServeContent does not return streaming errors, so observe the reader at this
// HTTP boundary. Each request owns its reader and independent seek offset.
type fileReader struct {
	*io.SectionReader
	report func(error)
}

func (r *fileReader) Read(data []byte) (int, error) {
	n, err := r.SectionReader.Read(data)
	if err == io.EOF {
		position, _ := r.SectionReader.Seek(0, io.SeekCurrent)
		if position < r.SectionReader.Size() {
			r.report(io.ErrUnexpectedEOF)
		}
	} else if err != nil {
		r.report(err)
	}
	return n, err
}

func (r *fileReader) Seek(offset int64, whence int) (int64, error) {
	position, err := r.SectionReader.Seek(offset, whence)
	r.report(err)
	return position, err
}
