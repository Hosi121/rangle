// Package appserver serves a local Web build and an explicitly selected scene.
// It is a development origin, not the production CDN/object-storage data plane.
package appserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"
)

const (
	maxManifestBytes = 1 << 20
	maxOpenFiles     = 2048
)

type Config struct {
	UIRoot       string
	SceneDir     string // Optional; must be supplied together with SceneID.
	SceneID      string
	PreviewPaths []string // Catalog-declared local videos that retain their root URLs.
}

type asset struct {
	file     *os.File
	data     []byte
	name     string
	modified time.Time
	gzip     *gzipAsset
	ui       bool
}

// Handler pins descriptors for its startup allowlist, without loading frame
// bodies into RAM. Replacing a file or introducing a symlink cannot redirect a
// request to a new file; restart to publish a rebuilt UI or changed manifest.
// Existing regular files may still change in place and are revalidated by mtime.
// Precompressed UI representations additionally require byte-identity checks.
// Call Close after all HTTP requests have drained, normally after Server.Shutdown.
type Handler struct {
	assets     map[string]asset
	files      []*os.File
	closeOnce  sync.Once
	closeError error
}

func New(config Config) (*Handler, error) {
	if config.UIRoot == "" {
		return nil, fmt.Errorf("UI root is required")
	}
	if (config.SceneDir == "") != (config.SceneID == "") || config.SceneID != "" && !validID(config.SceneID) {
		return nil, fmt.Errorf("scene directory and valid scene ID must be supplied together")
	}
	h := &Handler{assets: make(map[string]asset)}
	if err := h.loadUI(config.UIRoot, config.PreviewPaths); err != nil {
		h.Close()
		return nil, fmt.Errorf("load UI: %w", err)
	}
	if config.SceneDir != "" {
		if err := h.loadScene(config.SceneDir, config.SceneID); err != nil {
			h.Close()
			return nil, fmt.Errorf("load scene: %w", err)
		}
	}
	return h, nil
}

func (h *Handler) Close() error {
	h.closeOnce.Do(func() {
		for _, file := range h.files {
			h.closeError = errors.Join(h.closeError, file.Close())
		}
	})
	return h.closeError
}

func (h *Handler) loadUI(name string, previewPaths []string) error {
	root, err := os.OpenRoot(name)
	if err != nil {
		return err
	}
	defer root.Close()
	var walk func(*os.File, string) error
	walk = func(directory *os.File, prefix string) error {
		entries, err := directory.ReadDir(-1)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			relative := path.Join(prefix, entry.Name())
			if !validRelative(relative) || strings.HasSuffix(strings.ToLower(entry.Name()), ".gz") || strings.HasSuffix(entry.Name(), ".local.md") || strings.HasSuffix(entry.Name(), ".private.md") || prefix == "" && (entry.Name() == "samples" || entry.Name() == "healthz") {
				continue
			}
			if entry.Type()&os.ModeSymlink != 0 {
				continue
			}
			if entry.IsDir() {
				child, err := openRelative(root, relative, true)
				if err != nil {
					return err
				}
				err = walk(child, relative)
				child.Close()
				if err != nil {
					return err
				}
			} else if entry.Type().IsRegular() {
				url := "/demo/" + relative
				if relative == "landing.html" {
					url = "/"
				}
				if err := h.addFile(root, relative, url); err != nil {
					return err
				}
				item := h.assets[url]
				item.ui = !uiMedia(relative)
				h.assets[url] = item
				if err := h.attachGzip(root, relative, url); err != nil {
					return err
				}
			}
		}
		return nil
	}
	directory, err := root.Open(".")
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := walk(directory, ""); err != nil {
		return err
	}
	index, ok := h.assets["/demo/index.html"]
	if !ok {
		return fmt.Errorf("UI root must contain a regular index.html")
	}
	if _, ok := h.assets["/"]; !ok {
		return fmt.Errorf("UI root must contain a regular landing.html")
	}
	h.assets["/demo/"] = index
	for _, target := range previewPaths {
		relative := strings.TrimPrefix(target, "/")
		if !strings.HasPrefix(target, "/") || !validRelative(relative) {
			return fmt.Errorf("invalid local preview path")
		}
		if strings.HasPrefix(target, "/demo/") {
			continue
		}
		switch strings.ToLower(path.Ext(relative)) {
		case ".webm", ".mp4", ".m4v", ".ogv":
			if item, ok := h.assets["/demo/"+relative]; ok {
				h.assets[target] = item
			}
		}
	}
	return nil
}

func (h *Handler) loadScene(name, id string) error {
	root, err := os.OpenRoot(name)
	if err != nil {
		return err
	}
	defer root.Close()
	manifest, err := openRelative(root, "scene.json", false)
	if err != nil {
		return err
	}
	defer manifest.Close()
	info, err := manifest.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return fmt.Errorf("scene manifest must be a regular file of at most %d bytes", maxManifestBytes)
	}
	data, err := io.ReadAll(io.LimitReader(manifest, maxManifestBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxManifestBytes {
		return fmt.Errorf("scene manifest exceeds size limit")
	}
	var scene struct {
		Version   int      `json:"version"`
		FrameRate float64  `json:"frameRate"`
		Frames    []string `json:"frames"`
	}
	if err := json.Unmarshal(data, &scene); err != nil {
		return fmt.Errorf("parse scene manifest: %w", err)
	}
	if scene.Version != 1 || math.IsNaN(scene.FrameRate) || math.IsInf(scene.FrameRate, 0) || scene.FrameRate <= 0 || len(scene.Frames) == 0 || len(scene.Frames) > maxOpenFiles {
		return fmt.Errorf("invalid scene version, frame rate, or frame count")
	}
	prefix := "/samples/" + id + "/"
	for _, frame := range scene.Frames {
		if !validRelative(frame) || !strings.HasSuffix(frame, ".splat") {
			return fmt.Errorf("invalid relative scene frame path")
		}
		if _, exists := h.assets[prefix+frame]; exists {
			continue
		}
		if err := h.addFile(root, frame, prefix+frame); err != nil {
			return err
		}
	}
	// Keep the small manifest consistent with the startup frame allowlist.
	h.assets[prefix+"scene.json"] = asset{data: data, name: "scene.json", modified: info.ModTime()}
	return nil
}

func (h *Handler) addFile(root *os.Root, relative, url string) error {
	if len(h.files) >= maxOpenFiles {
		return fmt.Errorf("too many published files (limit %d)", maxOpenFiles)
	}
	file, err := openRelative(root, relative, false)
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		if err != nil {
			return err
		}
		return fmt.Errorf("published asset is not a regular file")
	}
	h.files = append(h.files, file)
	h.assets[url] = asset{file: file, name: path.Base(relative)}
	return nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w = errorCacheWriter{w}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/demo" {
		target := "/demo/"
		if r.URL.RawQuery != "" || r.URL.ForceQuery {
			target += "?" + r.URL.RawQuery
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
		return
	}
	if r.URL.Path == "/health" || r.URL.Path == "/healthz" {
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", "3")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			io.WriteString(w, "ok\n")
		}
		return
	}
	item, ok := h.assets[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	if strings.HasSuffix(item.name, ".splat") {
		w.Header().Set("Content-Type", "application/octet-stream")
	}
	if item.file == nil {
		http.ServeContent(w, r, item.name, item.modified, bytes.NewReader(item.data))
		return
	}
	info, err := item.file.Stat()
	if err != nil {
		http.Error(w, "asset unavailable", http.StatusInternalServerError)
		return
	}
	if item.ui {
		serveUIFile(w, r, item, info)
		return
	}
	http.ServeContent(w, r, item.name, info.ModTime(), io.NewSectionReader(item.file, 0, info.Size()))
}

// ServeContent's precondition failures bypass its usual error helper. Discard
// success-body encoding/framing on every error, including empty 412 responses,
// and keep errors no-store. A 416 retains its unsatisfied Content-Range.
type errorCacheWriter struct{ http.ResponseWriter }

func (w errorCacheWriter) WriteHeader(status int) {
	if status >= 400 {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Del("Content-Encoding")
		w.Header().Del("Content-Length")
		w.Header().Del("Accept-Ranges")
		if status != http.StatusRequestedRangeNotSatisfiable {
			w.Header().Del("Content-Range")
		}
	}
	w.ResponseWriter.WriteHeader(status)
}

func validID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

func validRelative(value string) bool {
	if !fs.ValidPath(value) || value == "." || strings.ContainsAny(value, "\\%?#:") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}
