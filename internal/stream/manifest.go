// Package stream defines the transport metadata shared by the delivery origin
// and the adaptive bitrate controller. It does not define a Gaussian codec.
package stream

import (
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

const Version = 1

// Manifest describes a versioned VOD clip whose representations share a timeline.
type Manifest struct {
	Version         int              `json:"version"`
	ClipID          string           `json:"clip_id"`
	Revision        string           `json:"revision"`
	Codec           string           `json:"codec"`
	Representations []Representation `json:"representations"`
}

// Representation is an independently decodable quality rendition. BitrateBPS
// advertises a conservative payload bitrate; the controller also uses actual
// segment sizes. PeakDecodedBytes is a working-set estimate, not download size.
type Representation struct {
	ID               string    `json:"id"`
	BitrateBPS       int64     `json:"bitrate_bps"`
	InitURL          string    `json:"init_url,omitempty"`
	InitSizeBytes    int64     `json:"init_size_bytes,omitempty"`
	PeakDecodedBytes int64     `json:"peak_decoded_bytes"`
	DecodeTimeMS     float64   `json:"decode_time_ms"`
	Segments         []Segment `json:"segments"`
}

// Segment is one independently decodable temporal interval. Its SHA256 covers
// the exact bytes served by the origin, before any application-level decoding.
type Segment struct {
	Index           int     `json:"index"`
	StartSeconds    float64 `json:"start_seconds"`
	DurationSeconds float64 `json:"duration_seconds"`
	URL             string  `json:"url"`
	SizeBytes       int64   `json:"size_bytes"`
	Independent     bool    `json:"independent"`
	SHA256          string  `json:"sha256"`
}

// URL returns the immutable manifest URL for this clip revision.
func (m Manifest) URL() string {
	return "/manifests/" + m.ClipID + "/" + m.Revision + ".json"
}

// Validate rejects ambiguous timelines and unadvertised dependency assumptions.
// Version 1 supports local absolute asset paths and independent segments only.
func (m Manifest) Validate() error {
	if m.Version != Version || !validID(m.ClipID) || !validID(m.Revision) || strings.TrimSpace(m.Codec) == "" {
		return fmt.Errorf("invalid manifest version, identity, or codec")
	}
	if len(m.Representations) == 0 {
		return fmt.Errorf("manifest has no representations")
	}
	ids := make(map[string]bool)
	for _, r := range m.Representations {
		if !validID(r.ID) || ids[r.ID] {
			return fmt.Errorf("invalid or duplicate representation %q", r.ID)
		}
		ids[r.ID] = true
		if r.BitrateBPS <= 0 || r.PeakDecodedBytes <= 0 || !finite(r.DecodeTimeMS) || r.DecodeTimeMS < 0 {
			return fmt.Errorf("invalid resource metadata for %q", r.ID)
		}
		prefix := "/assets/" + m.ClipID + "/" + m.Revision + "/" + r.ID + "/"
		if r.InitURL == "" && r.InitSizeBytes != 0 || r.InitURL != "" && (r.InitSizeBytes <= 0 || !validAssetURL(r.InitURL, prefix)) {
			return fmt.Errorf("invalid initialization asset for %q", r.ID)
		}
		if len(r.Segments) == 0 || len(r.Segments) != len(m.Representations[0].Segments) {
			return fmt.Errorf("unaligned segment count for %q", r.ID)
		}
		end := 0.0
		urls := make(map[string]bool)
		if r.InitURL != "" {
			urls[r.InitURL] = true
		}
		for i, s := range r.Segments {
			if s.Index != i || !finite(s.StartSeconds) || s.StartSeconds < 0 || !finite(s.DurationSeconds) || s.DurationSeconds <= 0 || math.Abs(s.StartSeconds-end) > 1e-6 {
				return fmt.Errorf("invalid segment timeline for %q at %d", r.ID, i)
			}
			if !s.Independent || s.SizeBytes <= 0 || !validAssetURL(s.URL, prefix) || urls[s.URL] {
				return fmt.Errorf("invalid segment asset for %q at %d", r.ID, i)
			}
			checksum, err := hex.DecodeString(s.SHA256)
			if err != nil || len(checksum) != 32 {
				return fmt.Errorf("invalid segment checksum for %q at %d", r.ID, i)
			}
			baseline := m.Representations[0].Segments[i]
			if math.Abs(s.StartSeconds-baseline.StartSeconds) > 1e-6 || math.Abs(s.DurationSeconds-baseline.DurationSeconds) > 1e-6 {
				return fmt.Errorf("unaligned rendition timeline for %q at %d", r.ID, i)
			}
			urls[s.URL] = true
			end = s.StartSeconds + s.DurationSeconds
			if !finite(end) {
				return fmt.Errorf("segment timeline overflows for %q", r.ID)
			}
		}
	}
	return nil
}

func validID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for _, c := range value {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' {
			continue
		}
		return false
	}
	return true
}

func validAssetURL(value, prefix string) bool {
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	name := strings.TrimPrefix(value, prefix)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\?#%") {
		return false
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
