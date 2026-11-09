// Package publication verifies a finite, approved asset snapshot before an
// operator promotes its catalog. It never uploads or changes a public pointer.
package publication

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Hosi121/rangle/internal/stream"
)

const (
	maxDocument   = 16 << 20
	maxModel      = 32 << 20
	maxFiles      = 4096
	maxTotalBytes = 512 << 20
)

type Proof struct {
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Approval is an operator assertion, not a legal opinion or a user signature.
// Every file is a root-relative URL path, including the standalone manifest.
type Approval struct {
	ClipID        string           `json:"clip_id"`
	Revision      string           `json:"revision"`
	Status        string           `json:"status"`
	Actor         string           `json:"actor"`
	Basis         string           `json:"basis"`
	SourceURL     string           `json:"source_url"`
	License       string           `json:"license"`
	LicenseURL    string           `json:"license_url"`
	Modifications string           `json:"modifications"`
	Attribution   string           `json:"attribution"`
	Caveat        string           `json:"caveat"`
	Files         map[string]Proof `json:"files"`
}

type Retained struct {
	ClipID   string `json:"clip_id"`
	Revision string `json:"revision"`
	Actor    string `json:"actor"`
	Basis    string `json:"basis"`
}

type Ledger struct {
	Version  int        `json:"version"`
	Clips    []Approval `json:"clips"`
	Retained []Retained `json:"retained"`
}

type entry struct {
	Title       string          `json:"title"`
	Sport       string          `json:"sport"`
	Description string          `json:"description"`
	Tags        []string        `json:"tags,omitempty"`
	PreviewURL  string          `json:"preview_url,omitempty"`
	SceneURL    string          `json:"scene_url,omitempty"`
	Manifest    stream.Manifest `json:"manifest"`
}

type document struct {
	Version int     `json:"version"`
	Clips   []entry `json:"clips"`
}

type CheckedClip struct {
	ClipID      string   `json:"clip_id"`
	Revision    string   `json:"revision"`
	EntrySHA256 string   `json:"entry_sha256"`
	Approval    Approval `json:"approval"`
}

type RetainedClip struct {
	Retained
	EntrySHA256 string `json:"entry_sha256"`
	Validation  string `json:"validation"`
}

type RemovedClip struct {
	ClipID      string `json:"clip_id"`
	Revision    string `json:"revision"`
	EntrySHA256 string `json:"entry_sha256"`
}

// Receipt is deterministic: repeating verification with identical inputs does
// not create another publication or overwrite an existing, different receipt.
// Files in Checked have all been measured against their recorded proofs.
type Receipt struct {
	Version               int            `json:"version"`
	Verifier              string         `json:"verifier"`
	Scope                 string         `json:"scope"`
	Origin                string         `json:"origin"`
	CatalogSHA256         string         `json:"catalog_sha256"`
	ApprovalsSHA256       string         `json:"approvals_sha256"`
	PreviousCatalogSHA256 string         `json:"previous_catalog_sha256,omitempty"`
	Checked               []CheckedClip  `json:"checked"`
	Retained              []RetainedClip `json:"retained"`
	Removed               []RemovedClip  `json:"removed"`
	CheckedFiles          int            `json:"checked_files"`
	CheckedBytes          int64          `json:"checked_bytes"`
}

type Options struct {
	CatalogPath         string
	ApprovalsPath       string
	Root                string
	Origin              string
	PreviousCatalogPath string
	PreviousSHA256      string
	Remote              bool
}

func hash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func contentHash(v any) string {
	data, _ := json.Marshal(v)
	return hash(data)
}

func validHash(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == 32 && value == strings.ToLower(value)
}

func readDocument(filename string) ([]byte, error) {
	info, err := os.Lstat(filename)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxDocument {
		return nil, fmt.Errorf("document must be a bounded regular file")
	}
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDocument+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDocument {
		return nil, fmt.Errorf("document exceeds %d bytes", maxDocument)
	}
	return data, nil
}

func decode(data []byte, v any) error {
	// encoding/json otherwise accepts duplicate keys and silently takes the last
	// value. Approval, catalog and manifest interpretations must be unambiguous.
	d := json.NewDecoder(bytes.NewReader(data))
	if err := uniqueJSON(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	return d.Decode(v)
}

func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return fmt.Errorf("JSON nesting exceeds 64")
	}
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate or invalid JSON key %q", key)
			}
			seen[s] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	case json.Delim('['):
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
		_, err = d.Token()
		return err
	case json.Delim('}'), json.Delim(']'):
		return fmt.Errorf("unexpected JSON delimiter")
	}
	return nil
}
