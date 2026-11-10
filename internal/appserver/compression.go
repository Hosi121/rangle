package appserver

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Build output only; models, videos and already compressed fonts/images are
// deliberately excluded. Keep this list aligned with precompress-web.mjs.
const maxPrecompressedBytes = 32 << 20

func compressibleUI(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".html", ".js", ".mjs", ".css", ".json", ".wasm", ".svg", ".txt":
		return true
	}
	return false
}

func uiMedia(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".rgs", ".rgsz", ".splat", ".webm", ".mp4", ".m4v", ".ogv":
		return true
	}
	return false
}

type gzipAsset struct {
	file             *os.File
	originalInfo     os.FileInfo
	compressedInfo   os.FileInfo
	originalSHA256   []byte
	compressedSHA256 []byte
}

// Open only sidecars for an already allowed original. Bad/missing sidecars are
// unavailable representations, so identity continues to work. Validation streams
// through fixed buffers once at startup, without retaining UI bodies in memory.
func (h *Handler) attachGzip(root *os.Root, relative, url string) error {
	if !compressibleUI(relative) {
		return nil
	}
	item := h.assets[url]
	originalInfo, err := item.file.Stat()
	if err != nil || originalInfo.Size() <= 0 || originalInfo.Size() > maxPrecompressedBytes {
		return nil
	}
	file, err := openRelative(root, relative+".gz", false)
	if err != nil {
		return nil
	}
	keep := false
	defer func() {
		if !keep {
			file.Close()
		}
	}()
	compressedInfo, err := file.Stat()
	if err != nil || compressedInfo.Size() <= 0 || compressedInfo.Size() >= originalInfo.Size() {
		return nil
	}
	compressedHash := sha256.New()
	originalHash := sha256.New()
	encoded := bufio.NewReader(io.TeeReader(io.NewSectionReader(file, 0, compressedInfo.Size()), compressedHash))
	reader, err := gzip.NewReader(encoded)
	if err != nil {
		return nil
	}
	reader.Multistream(false)
	original := io.NewSectionReader(item.file, 0, originalInfo.Size())
	left, right := make([]byte, 32<<10), make([]byte, 32<<10)
	matched := int64(0)
	for {
		n, readErr := reader.Read(left)
		if n > 0 {
			m, originalErr := io.ReadFull(original, right[:n])
			if originalErr != nil || m != n || !bytes.Equal(left[:n], right[:n]) {
				reader.Close()
				return nil
			}
			originalHash.Write(right[:n])
			matched += int64(n)
		}
		if readErr != nil {
			reader.Close()
			if readErr != io.EOF || matched != originalInfo.Size() {
				return nil
			}
			break
		}
	}
	if _, err := encoded.ReadByte(); err != io.EOF {
		return nil // Reject trailing bytes or additional gzip members.
	}
	afterOriginal, err := item.file.Stat()
	if err != nil || !sameFileVersion(originalInfo, afterOriginal) {
		return nil
	}
	afterCompressed, err := file.Stat()
	if err != nil || !sameFileVersion(compressedInfo, afterCompressed) {
		return nil
	}
	if len(h.files) >= maxOpenFiles {
		return fmt.Errorf("too many published files (limit %d)", maxOpenFiles)
	}
	h.files = append(h.files, file)
	keep = true
	item.gzip = &gzipAsset{file: file, originalInfo: afterOriginal, compressedInfo: afterCompressed,
		originalSHA256: originalHash.Sum(nil), compressedSHA256: compressedHash.Sum(nil)}
	h.assets[url] = item
	return nil
}

func (item asset) currentGzip(originalInfo os.FileInfo) (*os.File, os.FileInfo) {
	if item.gzip == nil || !sameFileVersion(item.gzip.originalInfo, originalInfo) {
		return nil, nil
	}
	info, err := item.gzip.file.Stat()
	if err != nil || !sameFileVersion(item.gzip.compressedInfo, info) {
		return nil, nil
	}
	// File timestamps can have a coarse tick, even on Linux: an in-place
	// same-size edit with restored mtime can share the old ctime. Compare the
	// pinned bytes with bounded streaming reads before serving a sidecar. This
	// adds disk/hash work, but does not compress per request or cache bodies.
	if !fileDigestMatches(item.file, originalInfo.Size(), item.gzip.originalSHA256) ||
		!fileDigestMatches(item.gzip.file, info.Size(), item.gzip.compressedSHA256) {
		return nil, nil
	}
	afterOriginal, err := item.file.Stat()
	if err != nil || !sameFileVersion(originalInfo, afterOriginal) {
		return nil, nil
	}
	afterCompressed, err := item.gzip.file.Stat()
	if err != nil || !sameFileVersion(info, afterCompressed) {
		return nil, nil
	}
	return item.gzip.file, info
}

func fileDigestMatches(file *os.File, size int64, expected []byte) bool {
	if size <= 0 || size > maxPrecompressedBytes {
		return false
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.NewSectionReader(file, 0, size))
	return err == nil && n == size && bytes.Equal(hash.Sum(nil), expected)
}

var qualityValue = regexp.MustCompile(`^(?:0(?:\.[0-9]{0,3})?|1(?:\.0{0,3})?)$`)

// Missing Accept-Encoding keeps existing identity behavior. Identity remains
// acceptable by default, except explicit identity;q=0 or wildcard;q=0.
func encodingQualities(header []string) (gzipQ, identityQ float64) {
	values := map[string]float64{}
	for _, line := range header {
		for _, part := range strings.Split(line, ",") {
			fields := strings.Split(part, ";")
			coding := strings.ToLower(strings.TrimSpace(fields[0]))
			if coding == "" {
				continue
			}
			q := 1.0
			if len(fields) > 1 {
				parameter := strings.SplitN(strings.TrimSpace(fields[1]), "=", 2)
				if len(fields) != 2 || len(parameter) != 2 || !strings.EqualFold(parameter[0], "q") || !qualityValue.MatchString(parameter[1]) {
					q = 0
				} else {
					q, _ = strconv.ParseFloat(parameter[1], 64)
				}
			}
			// Conflicting duplicates must not turn an explicit refusal into consent.
			if old, exists := values[coding]; !exists || q < old {
				values[coding] = q
			}
		}
	}
	gzipQ, exists := values["gzip"]
	if !exists {
		gzipQ = values["*"]
	}
	identityQ, exists = values["identity"]
	if !exists {
		identityQ = 1
		if wildcard, exists := values["*"]; exists && wildcard == 0 {
			identityQ = 0
		}
	}
	return gzipQ, identityQ
}

func serveUIFile(w http.ResponseWriter, r *http.Request, item asset, info os.FileInfo) {
	w.Header().Add("Vary", "Accept-Encoding")
	gzipQ, identityQ := encodingQualities(r.Header.Values("Accept-Encoding"))
	useGzip := gzipQ > 0 && (identityQ == 0 || gzipQ >= identityQ)
	// Preserve ranges over the original UI bytes whenever identity is allowed.
	if r.Header.Get("Range") != "" && identityQ > 0 {
		useGzip = false
	}
	var file *os.File
	var compressedInfo os.FileInfo
	gzipUnavailable := false
	if useGzip {
		file, compressedInfo = item.currentGzip(info)
		useGzip = file != nil
		gzipUnavailable = !useGzip
	}
	if !useGzip {
		if identityQ == 0 {
			const message = "no acceptable content encoding\n"
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(message)))
			w.WriteHeader(http.StatusNotAcceptable)
			if r.Method != http.MethodHead {
				io.WriteString(w, message)
			}
			return
		}
		modified := info.ModTime()
		if gzipUnavailable && item.gzip != nil && info.ModTime().Equal(item.gzip.originalInfo.ModTime()) {
			// A restored mtime must not let a conditional request reuse the
			// previous gzip after byte validation failed. Unknown modification
			// time makes ServeContent send fresh identity bytes instead of 304.
			modified = time.Time{}
		}
		http.ServeContent(w, r, item.name, modified, io.NewSectionReader(item.file, 0, info.Size()))
		return
	}
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Set("Content-Length", strconv.FormatInt(compressedInfo.Size(), 10))
	if r.Header.Get("Range") != "" {
		// A client refusing identity receives a complete gzip representation.
		// RFC Range is optional; partial gzip or multipart gzip is not usable by
		// ordinary decoders. Keep conditional GET handling in ServeContent.
		r = r.Clone(r.Context())
		r.Header.Del("Range")
		r.Header.Del("If-Range")
	}
	http.ServeContent(w, r, item.name, info.ModTime(), io.NewSectionReader(file, 0, compressedInfo.Size()))
}
