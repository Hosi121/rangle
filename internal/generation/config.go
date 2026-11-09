package generation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	Location   = "asia-northeast1"
	MaxRuntime = 30 * time.Minute
	// Reserve a conservative $2 per attempt, at most $20 for this release. The
	// compute shape is fixed to Spot T4 + n1-standard-4, without automatic retry.
	MaxAttempts = 10
)

type Preset struct {
	ID          string `json:"id"`
	Mode        string `json:"mode,omitempty"`
	Title       string `json:"title"`
	Sport       string `json:"sport"`
	Description string `json:"description"`
	InputURI    string `json:"input_uri"`
	InputSHA256 string `json:"input_sha256"`
	PreviewURL  string `json:"preview_url"`
	Source      string `json:"source"`
	License     string `json:"license"`
}

// ModeName keeps immutable presets written before temporal training compatible.
func (p Preset) ModeName() string {
	if p.Mode == "" {
		return "static"
	}
	return p.Mode
}

type Config struct {
	Project        string    `json:"project"`
	ProjectNumber  string    `json:"project_number"`
	Database       string    `json:"database"`
	Image          string    `json:"image"`
	ServiceAccount string    `json:"service_account"`
	OutputBucket   string    `json:"output_bucket"`
	MediaOrigin    string    `json:"media_origin"`
	Deadline       time.Time `json:"deadline"`
	Attempts       int       `json:"attempts"`
	Presets        []Preset  `json:"presets"`
}

var identifier = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var checksum = regexp.MustCompile(`^[a-f0-9]{64}$`)
var requestID = regexp.MustCompile(`^[a-f0-9]{32}$`)
var projectNumber = regexp.MustCompile(`^[0-9]{6,20}$`)

func ReadConfig(r io.Reader) (Config, error) {
	var cfg Config
	payload, err := io.ReadAll(io.LimitReader(r, (64<<10)+1))
	if err != nil {
		return cfg, fmt.Errorf("read generation config: %w", err)
	}
	if len(payload) > 64<<10 {
		return cfg, fmt.Errorf("generation config exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(payload))
	d.DisallowUnknownFields()
	if err := d.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("decode generation config: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return cfg, fmt.Errorf("generation config must contain one JSON value")
	}
	return cfg, cfg.Validate()
}

func (c Config) Validate() error {
	if !identifier.MatchString(c.Project) || !projectNumber.MatchString(c.ProjectNumber) || !identifier.MatchString(c.Database) || !identifier.MatchString(c.OutputBucket) {
		return fmt.Errorf("generation project, database and output bucket are required")
	}
	prefix := Location + "-docker.pkg.dev/" + c.Project + "/rangle-stg/"
	image := strings.Split(c.Image, "@sha256:")
	if len(image) != 2 || !strings.HasPrefix(image[0], prefix) || !checksum.MatchString(image[1]) {
		return fmt.Errorf("generation image must be pinned to a digest in the Tokyo project repository")
	}
	if c.ServiceAccount != "rangle-stg-trainer@"+c.Project+".iam.gserviceaccount.com" || c.Attempts < 1 || c.Attempts > MaxAttempts || c.Deadline.IsZero() {
		return fmt.Errorf("generation requires a dedicated trainer identity, deadline and at most ten attempts")
	}
	if !httpsURL(c.MediaOrigin) {
		return fmt.Errorf("generation media origin must use HTTPS")
	}
	origin, _ := url.Parse(c.MediaOrigin)
	if origin.RawQuery != "" || origin.Fragment != "" || origin.Path != "" {
		return fmt.Errorf("generation media origin must not contain a path, query or fragment")
	}
	if len(c.Presets) < 1 || len(c.Presets) > 10 {
		return fmt.Errorf("generation requires one to ten reviewed presets")
	}
	seen := map[string]bool{}
	for _, p := range c.Presets {
		if p.ModeName() != "static" && p.ModeName() != "dynamic" {
			return fmt.Errorf("generation preset %q has an unsupported mode", p.ID)
		}
		u, err := url.Parse(p.InputURI)
		if !identifier.MatchString(p.ID) || seen[p.ID] || p.Title == "" || p.Sport == "" || p.Description == "" || p.License != "CC BY 4.0" || !httpsURL(p.Source) || !httpsURL(p.PreviewURL) || !checksum.MatchString(p.InputSHA256) || err != nil || u.Scheme != "gs" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/datasets/") || !strings.HasSuffix(u.Path, ".npz") || strings.Contains(u.Path, "..") {
			return fmt.Errorf("generation preset %q must have reviewed CC BY 4.0 provenance and a checksummed dataset bundle", p.ID)
		}
		seen[p.ID] = true
	}
	return nil
}

func httpsURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func (c Config) preset(id string) (Preset, bool) {
	for _, p := range c.Presets {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
