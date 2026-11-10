package generation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/publisher"
	"github.com/Hosi121/rangle/internal/sports"
)

func TestReadConfigRejectsUnboundedOrAmbiguousInput(t *testing.T) {
	valid, err := json.Marshal(testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"unknown field": strings.Replace(string(valid), `"project":`, `"unexpected":true,"project":`, 1),
		"second value":  string(valid) + ` {}`,
		"too large":     string(valid) + strings.Repeat(" ", 64<<10),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadConfig(strings.NewReader(value)); err == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
	if _, err := ReadConfig(strings.NewReader(string(valid))); err != nil {
		t.Fatal(err)
	}
}

func TestConfigPinsComputeBudgetAndIdentity(t *testing.T) {
	cases := map[string]func(*Config){
		"unbounded attempts":     func(c *Config) { c.Attempts = 11 },
		"foreign trainer":        func(c *Config) { c.ServiceAccount = "other@comin-up.iam.gserviceaccount.com" },
		"mutable image":          func(c *Config) { c.Image = "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/trainer:latest" },
		"foreign region":         func(c *Config) { c.Image = strings.Replace(c.Image, Location, "us-central1", 1) },
		"missing project number": func(c *Config) { c.ProjectNumber = "" },
		"unreviewed license":     func(c *Config) { c.Presets[0].License = "unknown" },
		"unverified input":       func(c *Config) { c.Presets[0].InputSHA256 = "" },
		"unsafe input path":      func(c *Config) { c.Presets[0].InputURI = "gs://bucket/datasets/../secret.npz" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := testConfig()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("accepted invalid configuration")
			}
		})
	}
}

func TestVertexResourceNamesStayInTheConfiguredProjectAndRegion(t *testing.T) {
	v := &Vertex{config: testConfig()}
	for _, project := range []string{"comin-up", "113704194486"} {
		if !v.validName("projects/" + project + "/locations/asia-northeast1/customJobs/123") {
			t.Fatal("rejected canonical name")
		}
	}
	for _, name := range []string{
		"projects/foreign/locations/asia-northeast1/customJobs/123",
		"projects/comin-up/locations/us-central1/customJobs/123",
		"projects/comin-up/locations/asia-northeast1/customJobs/123/extra",
	} {
		if v.validName(name) {
			t.Fatalf("accepted %s", name)
		}
	}
}

func TestPublicationKeepsReviewedSportsContext(t *testing.T) {
	cfg := testConfig()
	cfg.Presets[0].Context = &sports.Context{Publisher: &publisher.Account{ID: "example", Name: "Example Sports", Kind: "company"}, Event: &sports.Event{ID: "sample", Name: "Training", Kind: "training"}}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	p, ok := cfg.preset(cfg.Presets[0].ID)
	if !ok {
		t.Fatal("missing preset")
	}
	job := Job{ID: strings.Repeat("a", 32), Snapshot: p}
	clip := publicClip(job, cfg.MediaOrigin)
	if clip.Context.Publisher.ID != "example" || clip.Context.Event.Kind != "training" {
		t.Fatal("publication dropped sporting context")
	}
	cfg.Presets[0].Context.Publisher.Name = "Changed later"
	if clip.Context.Publisher.Name != "Example Sports" || p.Context.Publisher.Name != "Example Sports" {
		t.Fatal("config mutation changed immutable publication context")
	}
	p.Context.Publisher.Kind = "person"
	if p.Context.Validate() == nil {
		t.Fatal("personal publisher accepted")
	}
}
