// Package catalog loads published clip metadata independently of HTTP and storage.
package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Hosi121/rangle/internal/sports"
	"github.com/Hosi121/rangle/internal/stream"
)

const maxCatalogBytes = 16 << 20

type clip struct {
	Context     *sports.Context `json:"context,omitempty"`
	Title       string          `json:"title"`
	Sport       string          `json:"sport"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags,omitempty"`
	PreviewURL  string          `json:"preview_url,omitempty"`
	SceneURL    string          `json:"scene_url,omitempty"`
	Manifest    stream.Manifest `json:"manifest"`
}

// Catalog is a validated startup snapshot. Input order is the public feed order.
type Catalog struct {
	clips        []clip
	byID         map[string]int
	snapshot     string
	sourceSHA256 string
}

// Load accepts one version-1 JSON document, bounded before decoding. URLs are
// validated as metadata; loading a catalog never fetches a remote resource.
func Load(source io.Reader) (*Catalog, error) {
	data, err := io.ReadAll(io.LimitReader(source, maxCatalogBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read catalog: %w", err)
	}
	if len(data) > maxCatalogBytes {
		return nil, fmt.Errorf("catalog exceeds %d bytes", maxCatalogBytes)
	}
	if err := catalogJSON(data); err != nil {
		return nil, err
	}
	var document struct {
		Version int    `json:"version"`
		Clips   []clip `json:"clips"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("catalog must contain one JSON document")
	}
	if document.Version != 1 || document.Clips == nil {
		return nil, fmt.Errorf("catalog requires version 1 and a clips array")
	}
	seen := make(map[string]bool, len(document.Clips))
	byID := make(map[string]int, len(document.Clips))
	for i, entry := range document.Clips {
		if strings.TrimSpace(entry.Title) == "" || len(entry.Title) > 256 ||
			strings.TrimSpace(entry.Sport) == "" || len(entry.Sport) > 128 ||
			len(entry.Description) > 4096 || len(entry.Manifest.Codec) > 128 {
			return nil, fmt.Errorf("clip %d has missing or oversized metadata", i)
		}
		if len(entry.Tags) > 10 {
			return nil, fmt.Errorf("clip %d has too many tags", i)
		}
		tags := make(map[string]bool, len(entry.Tags))
		for _, tag := range entry.Tags {
			key := strings.ToLower(tag)
			if !validTag(tag) || tags[key] {
				return nil, fmt.Errorf("clip %d has an invalid or duplicate tag", i)
			}
			tags[key] = true
		}
		if err := entry.Manifest.Validate(); err != nil {
			return nil, fmt.Errorf("clip %d manifest: %w", i, err)
		}
		id := entry.Manifest.ClipID
		if seen[id] {
			return nil, fmt.Errorf("duplicate clip ID %q", id)
		}
		seen[id] = true
		byID[id] = i
		for _, value := range []string{entry.PreviewURL, entry.SceneURL} {
			if value != "" && !validMediaURL(value) {
				return nil, fmt.Errorf("clip %q has an invalid media URL", id)
			}
		}
	}
	contexts := make([]*sports.Context, len(document.Clips))
	for i, c := range document.Clips {
		contexts[i] = c.Context
	}
	if err := sports.ValidateCollection(contexts); err != nil {
		return nil, fmt.Errorf("catalog context: %w", err)
	}
	// Ignore whitespace differences in the publisher's JSON when making cursors.
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("encode catalog snapshot: %w", err)
	}
	sum := sha256.Sum256(canonical)
	sourceSum := sha256.Sum256(data)
	return &Catalog{clips: document.Clips, byID: byID, snapshot: hex.EncodeToString(sum[:]), sourceSHA256: hex.EncodeToString(sourceSum[:])}, nil
}

// SourceSHA256 identifies the original JSON bytes, including whitespace. It is
// independent of the canonical catalog/origin/filter hashes used by cursors.
func (c *Catalog) SourceSHA256() string { return c.sourceSHA256 }

func validTag(value string) bool {
	if value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 32 {
		return false
	}
	for _, c := range value {
		if !unicode.IsLetter(c) && !unicode.IsNumber(c) && !unicode.IsMark(c) && c != '_' {
			return false
		}
	}
	return true
}

// Manifests returns independent copies for constructing delivery handlers.
func (c *Catalog) Manifests() []stream.Manifest {
	result := make([]stream.Manifest, len(c.clips))
	for i, entry := range c.clips {
		m := entry.Manifest
		m.Representations = append([]stream.Representation(nil), m.Representations...)
		for j := range m.Representations {
			m.Representations[j].Segments = append([]stream.Segment(nil), m.Representations[j].Segments...)
		}
		result[i] = m
	}
	return result
}

// LocalPreviewPaths returns an independent list of catalog-declared root paths.
// Absolute external URLs are metadata only and never become local file aliases.
func (c *Catalog) LocalPreviewPaths() []string {
	var paths []string
	for _, entry := range c.clips {
		if strings.HasPrefix(entry.PreviewURL, "/") {
			paths = append(paths, entry.PreviewURL)
		}
	}
	return paths
}

func validMediaURL(value string) bool {
	if len(value) > 4096 {
		return false
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return false
	}
	if u.IsAbs() {
		return (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
	}
	return u.Host == "" && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") &&
		u.RawQuery == "" && !u.ForceQuery && u.EscapedPath() == u.Path && path.Clean(u.Path) == u.Path &&
		!strings.Contains(u.Path, "\\")
}

// Metadata returns catalog-owned labels without fetching assets or resolving URLs.
func (c *Catalog) Metadata(id string) (PublicClip, bool) {
	i, ok := c.byID[id]
	if !ok {
		return PublicClip{}, false
	}
	entry := c.clips[i]
	return PublicClip{ID: id, Title: entry.Title, Sport: entry.Sport}, true
}
