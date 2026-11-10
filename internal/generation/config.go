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

	"github.com/Hosi121/rangle/internal/publisher"
	"github.com/Hosi121/rangle/internal/sports"
)

const (
	Location   = "asia-northeast1"
	MaxRuntime = 30 * time.Minute
	// Reserve a conservative $2 per attempt, at most $20 for this release. The
	// compute is Spot T4 + n1-standard-4 (offline) or n1-standard-16 (live),
	// without automatic retry. Both share the same ledger and reservation.
	MaxAttempts = 10
)

type Preset struct {
	PublisherAccounts   []publisher.ID  `json:"publisher_accounts,omitempty"`
	Context             *sports.Context `json:"context,omitempty" firestore:"context,omitempty"`
	ID                  string          `json:"id"`
	QualityProfile      string          `json:"quality_profile,omitempty"`
	EvaluateCompression bool            `json:"evaluate_compression,omitempty"`
	Mode                string          `json:"mode,omitempty"`
	Title               string          `json:"title"`
	Sport               string          `json:"sport"`
	Description         string          `json:"description"`
	InputURI            string          `json:"input_uri"`
	InputSHA256         string          `json:"input_sha256"`
	PreviewURL          string          `json:"preview_url"`
	Source              string          `json:"source"`
	License             string          `json:"license"`
}

// ModeName keeps immutable presets written before temporal training compatible.
func (p Preset) ModeName() string {
	if p.Mode == "" {
		return "static"
	}
	return p.Mode
}

type Config struct {
	Project        string      `json:"project"`
	ProjectNumber  string      `json:"project_number"`
	Database       string      `json:"database"`
	Image          string      `json:"image"`
	ServiceAccount string      `json:"service_account"`
	OutputBucket   string      `json:"output_bucket"`
	MediaOrigin    string      `json:"media_origin"`
	Deadline       time.Time   `json:"deadline"`
	Attempts       int         `json:"attempts"`
	Presets        []Preset    `json:"presets"`
	Live           *LiveConfig `json:"live,omitempty"`
}

// LiveConfig isolates the streaming publisher's identity and image from offline
// trainers. Admission, deadlines and attempt reservations are shared by all modes.
type LiveConfig struct {
	Image          string `json:"image"`
	ServiceAccount string `json:"service_account"`
	CatalogBucket  string `json:"catalog_bucket"`
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
	if !pinnedImage(c.Image, c.Project) {
		return fmt.Errorf("generation image must be pinned to a digest in the Tokyo project repository")
	}
	if c.ServiceAccount != "rangle-stg-trainer@"+c.Project+".iam.gserviceaccount.com" || c.Attempts < 1 || c.Attempts > MaxAttempts || c.Deadline.IsZero() {
		return fmt.Errorf("generation requires a dedicated trainer identity, deadline and at most ten attempts")
	}
	if c.Live != nil && (!pinnedImage(c.Live.Image, c.Project) ||
		c.Live.ServiceAccount != "rangle-stg-live-trainer@"+c.Project+".iam.gserviceaccount.com" ||
		c.Live.CatalogBucket != "rangle-stg-"+c.ProjectNumber+"-catalog") {
		return fmt.Errorf("live generation requires a pinned Tokyo image, dedicated identity and project catalog")
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
	contexts := make([]*sports.Context, len(c.Presets))
	for i, p := range c.Presets {
		contexts[i] = p.Context
	}
	if err := sports.ValidateCollection(contexts); err != nil {
		return fmt.Errorf("generation context: %w", err)
	}
	seen := map[string]bool{}
	for _, p := range c.Presets {
		if !p.validProfile() || p.EvaluateCompression && p.ModeName() != "dynamic" {
			return fmt.Errorf("generation preset %q has an unsupported quality profile", p.ID)
		}
		if p.ModeName() != "static" && p.ModeName() != "dynamic" && (p.ModeName() != "live" || c.Live == nil) {
			return fmt.Errorf("generation preset %q has an unsupported mode", p.ID)
		}
		u, err := url.Parse(p.InputURI)
		validInput := err == nil && u.Scheme == "gs" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.Contains(u.Path, "..")
		if validInput {
			if p.ModeName() == "live" {
				validInput = strings.HasPrefix(u.Path, "/datasets/live/") && strings.HasSuffix(u.Path, ".zip")
			} else {
				validInput = strings.HasPrefix(u.Path, "/datasets/") && strings.HasSuffix(u.Path, ".npz")
			}
		}
		if !identifier.MatchString(p.ID) || seen[p.ID] || p.Title == "" || p.Sport == "" || p.Description == "" || p.License != "CC BY 4.0" || !httpsURL(p.Source) || !httpsURL(p.PreviewURL) || !checksum.MatchString(p.InputSHA256) || !validInput {
			return fmt.Errorf("generation preset %q must have reviewed CC BY 4.0 provenance and a checksummed dataset bundle", p.ID)
		}
		if len(p.PublisherAccounts) > 100 {
			return fmt.Errorf("too many preset publisher grants")
		}
		grants := map[publisher.ID]bool{}
		for _, id := range p.PublisherAccounts {
			if !id.Valid() || grants[id] {
				return fmt.Errorf("invalid preset publisher grant")
			}
			grants[id] = true
		}
		seen[p.ID] = true
	}
	return nil
}

func pinnedImage(value, project string) bool {
	prefix := Location + "-docker.pkg.dev/" + project + "/rangle-stg/"
	image := strings.Split(value, "@sha256:")
	return len(image) == 2 && strings.HasPrefix(image[0], prefix) && len(image[0]) > len(prefix) && checksum.MatchString(image[1])
}

func httpsURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func (c Config) preset(id string) (Preset, bool) {
	for _, p := range c.Presets {
		if p.ID == id {
			p.Context = p.Context.Clone()
			p.PublisherAccounts = append([]publisher.ID(nil), p.PublisherAccounts...)
			return p, true
		}
	}
	return Preset{}, false
}

func (p Preset) validProfile() bool {
	switch p.QualityProfile {
	case "", "baseline":
		return true
	case "motion-prior", "local-lifetime":
		return p.ModeName() == "dynamic"
	default:
		return false
	}
}
