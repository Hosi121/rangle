// Package live validates and serves a bounded live-session metadata snapshot.
// Asset production, byte validation, storage and active selection are separate.
package live

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxIndexBytes = 512 << 10
	MaxSessions   = 8
	MaxSegments   = 32
	MaxModelBytes = 32 << 20
)

// Index is one atomically published version-1 snapshot. Input order is kept.
type Index struct {
	Version  int       `json:"version"`
	Sessions []Session `json:"sessions"`
}

type Session struct {
	ID                     string    `json:"id"`
	Title                  string    `json:"title"`
	State                  string    `json:"state"`
	StartedAt              time.Time `json:"started_at"`
	UpdatedAt              time.Time `json:"updated_at"`
	TargetLatencySeconds   float64   `json:"target_latency_seconds"`
	LatestInputTimeSeconds float64   `json:"latest_input_time_seconds"`
	DroppedWindows         int64     `json:"dropped_windows"`
	Source                 Source    `json:"source"`
	Segments               []Segment `json:"segments"`
}

type Source struct {
	Label   string `json:"label"`
	URL     string `json:"url"`
	License string `json:"license"`
	Caveat  string `json:"caveat"`
}

// Segment references one independent RGS1 output. Quality/latency fields are
// measurements supplied by the producer, not verified performance guarantees.
type Segment struct {
	Sequence        int       `json:"sequence"`
	StartSeconds    float64   `json:"start_seconds"`
	EndSeconds      float64   `json:"end_seconds"`
	SceneURL        string    `json:"scene_url"`
	ModelSHA256     string    `json:"model_sha256"`
	ModelBytes      int64     `json:"model_bytes"`
	CapturedAt      time.Time `json:"captured_at"`
	AvailableAt     time.Time `json:"available_at"`
	TrainingSeconds float64   `json:"training_seconds"`
	MeanPackedPSNR  float64   `json:"mean_packed_psnr"`
	MeanPackedSSIM  float64   `json:"mean_packed_ssim"`
}

var utcPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,9})?(Z|\+00:00)$`)

// ParseIndex accepts exactly one strict JSON document. All schema fields are
// required, including zero-valued measurements and empty arrays; null and
// duplicate/unknown keys are rejected. It never fetches a referenced URL.
func ParseIndex(data []byte, assetOrigin string) (*Index, error) {
	if len(data) > MaxIndexBytes {
		return nil, fmt.Errorf("live index exceeds %d bytes", MaxIndexBytes)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("live index is not UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSON(decoder, 0); err != nil {
		return nil, fmt.Errorf("live index JSON: %w", err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("live index must contain one JSON document")
	}
	if err := requiredShape(data); err != nil {
		return nil, err
	}
	var index Index
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&index); err != nil {
		return nil, fmt.Errorf("live index schema: %w", err)
	}
	if err := index.Validate(assetOrigin); err != nil {
		return nil, err
	}
	return &index, nil
}

// Validate checks a constructed snapshot too. It accepts overlapping sliding
// windows, sequence gaps, and completed/failed sessions with no output yet.
// Absolute scene URLs require the configured HTTPS media origin and /live path.
func (index Index) Validate(assetOrigin string) error {
	origin, err := validateOrigin(assetOrigin)
	if err != nil {
		return err
	}
	if index.Version != 1 || index.Sessions == nil || len(index.Sessions) > MaxSessions {
		return fmt.Errorf("invalid live index version/session limit")
	}
	ids := map[string]bool{}
	for _, session := range index.Sessions {
		if !validID(session.ID) || ids[session.ID] {
			return fmt.Errorf("invalid or duplicate live session ID")
		}
		ids[session.ID] = true
		if !text(session.Title, 256) || !utcTime(session.StartedAt) || !utcTime(session.UpdatedAt) || session.UpdatedAt.Before(session.StartedAt) {
			return fmt.Errorf("invalid live session title/timestamps")
		}
		switch session.State {
		case "starting", "live", "ended", "failed":
		default:
			return fmt.Errorf("invalid live session state")
		}
		if !finite(session.TargetLatencySeconds) || session.TargetLatencySeconds <= 0 || !finite(session.LatestInputTimeSeconds) || session.LatestInputTimeSeconds < 0 || session.DroppedWindows < 0 || session.DroppedWindows > 1<<53-1 {
			return fmt.Errorf("invalid live session temporal metadata")
		}
		source := session.Source
		if !text(source.Label, 256) || !text(source.License, 256) || !text(source.Caveat, 4096) || !sourceURL(source.URL) {
			return fmt.Errorf("invalid live source/attribution")
		}
		if session.Segments == nil || len(session.Segments) > MaxSegments {
			return fmt.Errorf("invalid live segment array/limit")
		}
		for i, segment := range session.Segments {
			checksum, err := hex.DecodeString(segment.ModelSHA256)
			if err != nil || len(checksum) != 32 || segment.ModelSHA256 != strings.ToLower(segment.ModelSHA256) || segment.ModelBytes < 1 || segment.ModelBytes > MaxModelBytes || segment.Sequence < 1 || segment.Sequence > 999999 {
				return fmt.Errorf("invalid live segment identity/model proof")
			}
			if !finite(segment.StartSeconds) || !finite(segment.EndSeconds) || segment.StartSeconds < 0 || segment.EndSeconds <= segment.StartSeconds || segment.EndSeconds > session.LatestInputTimeSeconds+1e-6 {
				return fmt.Errorf("invalid live segment timeline")
			}
			if !utcTime(segment.CapturedAt) || !utcTime(segment.AvailableAt) || segment.AvailableAt.Before(segment.CapturedAt) || segment.AvailableAt.Before(session.StartedAt) || segment.AvailableAt.After(session.UpdatedAt) {
				return fmt.Errorf("invalid live segment capture/availability timestamps")
			}
			if !finite(segment.TrainingSeconds) || segment.TrainingSeconds < 0 || !finite(segment.MeanPackedPSNR) || segment.MeanPackedPSNR < 0 || !finite(segment.MeanPackedSSIM) || segment.MeanPackedSSIM < -1 || segment.MeanPackedSSIM > 1 {
				return fmt.Errorf("invalid live segment measurements")
			}
			if i > 0 {
				previous := session.Segments[i-1]
				if segment.Sequence <= previous.Sequence || segment.StartSeconds < previous.StartSeconds || segment.EndSeconds <= previous.EndSeconds || segment.CapturedAt.Before(previous.CapturedAt) || segment.AvailableAt.Before(previous.AvailableAt) {
					return fmt.Errorf("live segments are not chronological")
				}
			}
			revision := fmt.Sprintf("%06d-%s", segment.Sequence, segment.ModelSHA256[:16])
			local := "/live-assets/" + session.ID + "/" + revision + "/scene.json"
			remote := origin + "/live/" + session.ID + "/" + revision + "/scene.json"
			if segment.SceneURL != local && (origin == "" || segment.SceneURL != remote) {
				return fmt.Errorf("live scene URL escapes the configured session/revision")
			}
		}
	}
	return nil
}

func validateOrigin(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || (u.Port() != "" && u.Port() != "443") {
		return "", fmt.Errorf("live asset origin must be one HTTPS origin")
	}
	u.Path = ""
	return u.String(), nil
}

func sourceURL(value string) bool {
	if len(value) > 4096 || !utf8.ValidString(value) {
		return false
	}
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Opaque == "" && u.Fragment == "" && u.RawQuery == "" && !u.ForceQuery
}

// validID implements the schema's [A-Za-z0-9_-]{1,64} identifier grammar.
// Both metadata validation and the hot HTTP route use the same bounded check.
func validID(value string) bool {
	if len(value) == 0 || len(value) > 64 {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func text(value string, limit int) bool {
	return strings.TrimSpace(value) != "" && len(value) <= limit && utf8.ValidString(value)
}
func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func utcTime(t time.Time) bool {
	_, offset := t.Zone()
	return !t.IsZero() && offset == 0 && t.Year() >= 0 && t.Year() <= 9999
}

func uniqueJSON(decoder *json.Decoder, depth int) error {
	if depth > 16 {
		return fmt.Errorf("JSON nesting exceeds 16")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate JSON key")
			}
			seen[name] = true
			if err := uniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case json.Delim('['):
		for decoder.More() {
			if err := uniqueJSON(decoder, depth+1); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case json.Delim('}'), json.Delim(']'):
		return fmt.Errorf("unexpected JSON delimiter")
	}
	return nil
}

func fields(data []byte, names ...string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, err
	}
	if len(object) != len(names) {
		return nil, fmt.Errorf("missing/unknown live field")
	}
	for _, name := range names {
		value, ok := object[name]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, fmt.Errorf("missing/null live field %s", name)
		}
	}
	return object, nil
}

func timestamp(data json.RawMessage) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil || !utcPattern.MatchString(value) {
		return fmt.Errorf("live timestamps require RFC3339 UTC")
	}
	if _, err := time.Parse(time.RFC3339Nano, value); err != nil {
		return fmt.Errorf("invalid live UTC timestamp")
	}
	return nil
}

func requiredShape(data []byte) error {
	index, err := fields(data, "version", "sessions")
	if err != nil {
		return err
	}
	var sessions []json.RawMessage
	if err := json.Unmarshal(index["sessions"], &sessions); err != nil {
		return err
	}
	if len(sessions) > MaxSessions {
		return fmt.Errorf("live index session limit exceeded")
	}
	for _, data := range sessions {
		s, err := fields(data, "id", "title", "state", "started_at", "updated_at", "target_latency_seconds", "latest_input_time_seconds", "dropped_windows", "source", "segments")
		if err != nil {
			return err
		}
		for _, name := range []string{"started_at", "updated_at"} {
			if err := timestamp(s[name]); err != nil {
				return err
			}
		}
		if _, err := fields(s["source"], "label", "url", "license", "caveat"); err != nil {
			return err
		}
		var segments []json.RawMessage
		if err := json.Unmarshal(s["segments"], &segments); err != nil {
			return err
		}
		if len(segments) > MaxSegments {
			return fmt.Errorf("live segment limit exceeded")
		}
		for _, data := range segments {
			segment, err := fields(data, "sequence", "start_seconds", "end_seconds", "scene_url", "model_sha256", "model_bytes", "captured_at", "available_at", "training_seconds", "mean_packed_psnr", "mean_packed_ssim")
			if err != nil {
				return err
			}
			for _, name := range []string{"captured_at", "available_at"} {
				if err := timestamp(segment[name]); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
