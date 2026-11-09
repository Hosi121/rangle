package generation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/aiplatform/v1"
	"google.golang.org/api/option"
)

func liveConfig() Config {
	c := testConfig()
	c.Live = &LiveConfig{
		Image:          "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/live-trainer@sha256:" + strings.Repeat("c", 64),
		ServiceAccount: "rangle-stg-live-trainer@comin-up.iam.gserviceaccount.com",
		CatalogBucket:  "rangle-stg-113704194486-catalog",
	}
	p := c.Presets[0]
	p.ID, p.Mode = "vru-live", "live"
	p.InputURI = "gs://rangle-stg-example-inputs/datasets/live/vru.zip"
	c.Presets = append(c.Presets, p)
	return c
}

func TestLiveConfigRequiresSeparatePublisherAndReviewedSourceBundle(t *testing.T) {
	if err := liveConfig().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Config){
		"disabled":               func(c *Config) { c.Live = nil },
		"mutable image":          func(c *Config) { c.Live.Image = "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/live:latest" },
		"foreign region":         func(c *Config) { c.Live.Image = strings.Replace(c.Live.Image, Location, "us-central1", 1) },
		"offline identity":       func(c *Config) { c.Live.ServiceAccount = c.ServiceAccount },
		"foreign catalog":        func(c *Config) { c.Live.CatalogBucket = "other-catalog" },
		"offline input":          func(c *Config) { c.Presets[1].InputURI = c.Presets[0].InputURI },
		"unscoped zip":           func(c *Config) { c.Presets[1].InputURI = "gs://inputs/datasets/vru.zip" },
		"traversal":              func(c *Config) { c.Presets[1].InputURI = "gs://inputs/datasets/live/%2e%2e/vru.zip" },
		"unverified":             func(c *Config) { c.Presets[1].InputSHA256 = "" },
		"lossy profile":          func(c *Config) { c.Presets[1].QualityProfile = "local-lifetime" },
		"compression experiment": func(c *Config) { c.Presets[1].EvaluateCompression = true },
		"offline zip":            func(c *Config) { c.Presets[0].InputURI = c.Presets[1].InputURI },
	} {
		t.Run(name, func(t *testing.T) {
			c := liveConfig()
			mutate(&c)
			if c.Validate() == nil {
				t.Fatal("accepted unsafe live configuration")
			}
		})
	}
}

func TestVertexLiveUsesPublisherIdentityWithoutChangingBudget(t *testing.T) {
	c := liveConfig()
	id := nonce(1)
	created := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var job aiplatform.GoogleCloudAiplatformV1CustomJob
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			t.Fatal(err)
		}
		s := job.JobSpec
		if s.ServiceAccount != c.Live.ServiceAccount || s.Scheduling.Strategy != "SPOT" || s.Scheduling.Timeout != "1800s" || !s.Scheduling.DisableRetries || len(s.WorkerPoolSpecs) != 1 {
			t.Fatal("live job changed shared compute limits or publisher identity")
		}
		p := s.WorkerPoolSpecs[0]
		if p.ContainerSpec.ImageUri != c.Live.Image || p.ReplicaCount != 1 || p.MachineSpec.AcceleratorCount != 1 || p.MachineSpec.AcceleratorType != "NVIDIA_TESLA_T4" || p.MachineSpec.MachineType != "n1-standard-16" || job.Labels["rangle_job"] != id {
			t.Fatal("live job changed pinned shape, image or shutdown ownership")
		}
		args := p.ContainerSpec.Args
		got := map[string]string{}
		for i := 0; i < len(args); i += 2 {
			got[args[i]] = args[i+1]
		}
		want := map[string]string{
			"--input-uri": c.Presets[1].InputURI, "--input-sha256": c.Presets[1].InputSHA256,
			"--output-prefix": "gs://" + c.OutputBucket + "/live/" + id + "/", "--catalog-bucket": c.Live.CatalogBucket,
			"--session-id": id, "--deadline": created.Add(MaxRuntime).Format(time.RFC3339Nano),
			"--media-origin": c.MediaOrigin, "--source-seconds": "10", "--seed-available-after": "0.8",
			"--bootstrap-steps": "300", "--steps-per-update": "300", "--decode-workers": "16",
			"--title": c.Presets[1].Title, "--source": c.Presets[1].Source, "--license": "CC BY 4.0",
		}
		if len(got) != len(want) {
			t.Fatal("unexpected training override")
		}
		for key, value := range want {
			if got[key] != value {
				t.Errorf("%s=%q want %q", key, got[key], value)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "projects/113704194486/locations/asia-northeast1/customJobs/123"})
	}))
	defer server.Close()
	v, err := NewVertex(context.Background(), c, option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.Submit(context.Background(), Job{ID: id, CreatedAt: created}, c.Presets[1]); err != nil {
		t.Fatal(err)
	}
}

func TestLiveAndOfflineShareAdmissionAndNeverPublishLiveAsVOD(t *testing.T) {
	s, p := testService()
	s.Config = liveConfig()
	ctx := context.Background()
	job, err := s.Create(ctx, "viewer", "vru-live", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, "other", "vru-frame0", nonce(2)); !errors.Is(err, ErrBusy) {
		t.Fatalf("offline bypassed active live job: %v", err)
	}
	h, err := NewHandler(s, func(r *http.Request) (string, error) { return r.Header.Get("X-Test-User"), nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []State{Running, Succeeded} {
		p.state = state
		result := call(t, h, "GET", "/api/v1/generation/jobs/"+job.ID, "", "viewer", "")
		body := decode(t, result)
		if string(body["viewer_url"]) != `"/demo/?live=`+job.ID+`"` || body["scene_url"] != nil {
			t.Fatalf("live response exposed VOD viewer: %s", body)
		}
	}
	if _, err = s.SetPublication(ctx, "viewer", job.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("live eligible for VOD publication: %v", err)
	}
	if exists, err := s.CompletedClipExists(ctx, "generated-"+job.ID); err != nil || exists {
		t.Fatalf("live detail eligible: %v %v", exists, err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/clips/{clip}", h.ClipHandler(http.NotFoundHandler()))
	if result := call(t, mux, "GET", "/api/v1/clips/generated-"+job.ID, "", "", ""); result.StatusCode != 404 {
		t.Fatalf("live VOD detail: %d", result.StatusCode)
	}
	p.state = Failed
	if _, err = s.Create(ctx, "viewer", "vru-frame0", nonce(3)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Create(ctx, "viewer", "vru-live", nonce(4)); !errors.Is(err, ErrBudget) {
		t.Fatalf("live bypassed shared attempt budget: %v", err)
	}
	if p.submits.Load() != 2 {
		t.Fatal("unexpected additional GPU allocation")
	}
}
