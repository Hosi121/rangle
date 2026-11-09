// Package privatepreview serves small scene snapshots after current administrator
// authorization. It is independent of the public clip catalog and media origin.
package privatepreview

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"time"
)

const (
	PathPrefix       = "/api/v1/private-scenes/"
	assetNamePattern = `scene\.json|frame-[0-9]{3}\.splat`
)

var (
	ErrUnauthenticated = errors.New("not authenticated")
	ErrForbidden       = errors.New("forbidden")
	errInvalidAsset    = errors.New("invalid private asset")
	assetPath          = regexp.MustCompile(`^/api/v1/private-scenes/([a-z0-9][a-z0-9-]{0,63})/(` + assetNamePattern + `)$`)
	assetName          = regexp.MustCompile(`^(` + assetNamePattern + `)$`)
)

type Config struct {
	Directory string
	// Authorize must refresh the current administrator role on every call.
	Authorize func(*http.Request) error
	Logger    *slog.Logger
}

type Handler struct{ cfg Config }

// New does not open Directory: an absent optional mount must not break startup.
// An empty Directory disables assets, while requests still pass authorization.
func New(cfg Config) (*Handler, error) {
	if cfg.Authorize == nil {
		return nil, fmt.Errorf("private preview requires authorization")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Handler{cfg: cfg}, nil
}

// privateWriter restores the policy even when ServeContent strips cache headers
// on invalid Range or failed precondition paths.
type privateWriter struct{ http.ResponseWriter }

func (w privateWriter) WriteHeader(status int) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.ResponseWriter.WriteHeader(status)
}

func (w privateWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (h *Handler) ServeHTTP(out http.ResponseWriter, r *http.Request) {
	w := privateWriter{out}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Add("Vary", "Cookie")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	err := h.cfg.Authorize(r.WithContext(ctx))
	if err == nil {
		err = ctx.Err()
	}
	cancel()
	switch {
	case errors.Is(err, ErrUnauthenticated):
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	case errors.Is(err, ErrForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	case err != nil:
		h.unavailable(w, r, "authorize", "", "", err)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	match := assetPath.FindStringSubmatch(r.URL.Path)
	if match == nil || r.URL.RawPath != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || h.cfg.Directory == "" {
		http.NotFound(w, r)
		return
	}
	id, name := match[1], match[2]
	file, info, err := h.open(id, name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, errInvalidAsset) {
			http.NotFound(w, r)
		} else {
			h.unavailable(w, r, "open_asset", id, name, err)
		}
		return
	}
	defer file.Close()
	contentType := "application/octet-stream"
	if name == "scene.json" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	// Bound slow clients as well as reads between mount operations. An individual
	// blocked filesystem syscall still depends on the mounted filesystem's timeout.
	controller := http.NewResponseController(w)
	_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
	defer controller.SetWriteDeadline(time.Time{})
	bodyCtx, bodyCancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer bodyCancel()
	body := &contextContent{ctx: bodyCtx, ReadSeeker: io.NewSectionReader(file, 0, info.Size())}
	http.ServeContent(w, r.WithContext(bodyCtx), name, info.ModTime(), body)
	if body.err != nil && !errors.Is(body.err, context.Canceled) && !errors.Is(body.err, context.DeadlineExceeded) {
		h.cfg.Logger.ErrorContext(r.Context(), "private scene unavailable", "operation", "read_asset", "scene_id", id, "asset_name", name, "error", body.err)
	}
}

func (h *Handler) unavailable(w http.ResponseWriter, r *http.Request, operation, id, name string, err error) {
	if !errors.Is(err, context.Canceled) {
		h.cfg.Logger.ErrorContext(r.Context(), "private scene unavailable", "operation", operation, "scene_id", id, "asset_name", name, "error", err)
	}
	http.Error(w, "private scene unavailable", http.StatusServiceUnavailable)
}

func (h *Handler) open(id, name string) (*os.File, fs.FileInfo, error) {
	rootInfo, err := os.Lstat(h.cfg.Directory)
	if err != nil {
		return nil, nil, err
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errInvalidAsset
	}
	root, err := os.OpenRoot(h.cfg.Directory)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	openedRootInfo, err := root.Stat(".")
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(rootInfo, openedRootInfo) {
		return nil, nil, errInvalidAsset
	}
	dirInfo, err := root.Lstat(id)
	if err != nil {
		return nil, nil, err
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return nil, nil, errInvalidAsset
	}
	// Hold a directory descriptor so later renames cannot redirect file lookup.
	dir, err := root.OpenRoot(id)
	if err != nil {
		return nil, nil, err
	}
	defer dir.Close()
	openedDirInfo, err := dir.Stat(".")
	if err != nil {
		return nil, nil, err
	}
	if !os.SameFile(dirInfo, openedDirInfo) {
		return nil, nil, errInvalidAsset
	}
	dirInfoAfter, err := root.Lstat(id)
	if err != nil {
		return nil, nil, err
	}
	if dirInfoAfter.Mode()&os.ModeSymlink != 0 || !os.SameFile(dirInfo, dirInfoAfter) {
		return nil, nil, errInvalidAsset
	}
	before, err := dir.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	limit := int64(32 << 20)
	if name == "scene.json" {
		limit = 64 << 10
	}
	valid := func(info fs.FileInfo) bool {
		return info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= limit && (name == "scene.json" || info.Size()%32 == 0)
	}
	if !valid(before) {
		return nil, nil, errInvalidAsset
	}
	file, err := openFile(dir, name)
	if err != nil {
		return nil, nil, err
	}
	after, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !valid(after) || !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		file.Close()
		return nil, nil, errInvalidAsset
	}
	current, err := dir.Lstat(name)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !current.Mode().IsRegular() || !os.SameFile(after, current) {
		file.Close()
		return nil, nil, errInvalidAsset
	}
	return file, after, nil
}

type contextContent struct {
	io.ReadSeeker
	ctx context.Context
	err error
}

func (c *contextContent) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		c.err = err
		return 0, err
	}
	n, err := c.ReadSeeker.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		c.err = err
	}
	return n, err
}

func (c *contextContent) Seek(offset int64, whence int) (int64, error) {
	if err := c.ctx.Err(); err != nil {
		c.err = err
		return 0, err
	}
	n, err := c.ReadSeeker.Seek(offset, whence)
	if err != nil {
		c.err = err
	}
	return n, err
}
