package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/api/aiplatform/v1"
	"google.golang.org/api/option"
)

func TestReviewedPresetModeValidation(t *testing.T) {
	for _, mode := range []string{"", "static", "dynamic", "unknown", "4dgs"} {
		t.Run("mode="+mode, func(t *testing.T) {
			cfg := testConfig()
			cfg.Presets[0].Mode = mode
			err := cfg.Validate()
			valid := mode == "" || mode == "static" || mode == "dynamic"
			if (err == nil) != valid {
				t.Fatalf("mode %q: %v", mode, err)
			}
		})
	}
}

func TestVertexSelectsOnlyReviewedTemporalModeAndKeepsStaticWorkerCompatible(t *testing.T) {
	for _, mode := range []string{"", "static", "dynamic"} {
		t.Run("mode="+mode, func(t *testing.T) {
			cfg := testConfig()
			preset := cfg.Presets[0]
			preset.Mode = mode
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var job aiplatform.GoogleCloudAiplatformV1CustomJob
				if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
					t.Errorf("decode submitted job: %v", err)
					http.Error(w, "invalid job", http.StatusBadRequest)
					return
				}
				spec := job.JobSpec
				pool := spec.WorkerPoolSpecs[0]
				args := pool.ContainerSpec.Args
				i, j := slices.Index(args, "--mode"), slices.Index(args, "--steps")
				if mode == "dynamic" {
					if i < 0 || j < 0 || args[i+1] != "dynamic" || args[j+1] != "12000" {
						t.Errorf("temporal arguments: %q", args)
					}
				} else if i >= 0 || j >= 0 {
					t.Errorf("new flags sent to old static worker: %q", args)
				}
				if pool.MachineSpec.AcceleratorCount != 1 || pool.MachineSpec.AcceleratorType != "NVIDIA_TESLA_T4" || pool.ReplicaCount != 1 || spec.Scheduling.Timeout != "1800s" || spec.Scheduling.Strategy != "SPOT" {
					t.Error("temporal training changed compute budget")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"name":"projects/113704194486/locations/asia-northeast1/customJobs/123"}`))
			}))
			defer server.Close()
			v, err := NewVertex(context.Background(), cfg, option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
			if err != nil {
				t.Fatal(err)
			}
			if _, err = v.Submit(context.Background(), Job{ID: nonce(1), CreatedAt: time.Now()}, preset); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDynamicJobModeAndResultsUseImmutablePresetSnapshot(t *testing.T) {
	s, p := testService()
	s.Config.Presets[0].Mode = "dynamic"
	h, err := NewHandler(s, func(r *http.Request) (string, error) { return "viewer", nil })
	if err != nil {
		t.Fatal(err)
	}
	result := call(t, h, "GET", "/api/v1/generation/presets", "", "viewer", "")
	raw := decode(t, result)
	if !strings.Contains(string(raw["presets"]), `"mode":"dynamic"`) {
		t.Fatal("missing reviewed mode")
	}
	job, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	s.Config.Presets[0].Mode = "static"
	p.state = Succeeded
	result = call(t, h, "GET", "/api/v1/generation/jobs/"+job.ID, "", "viewer", "")
	raw = decode(t, result)
	if !strings.Contains(string(raw["job"]), `"mode":"dynamic"`) {
		t.Fatal("job mode changed with live configuration")
	}
	for _, private := range []string{"gs://", "owner", "preset_snapshot", "provider_name"} {
		if strings.Contains(string(raw["job"]), private) {
			t.Fatalf("leaked %s", private)
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/api/v1/clips/{clip}", h.ClipHandler(http.NotFoundHandler()))
	result = call(t, mux, "GET", "/api/v1/clips/generated-"+job.ID, "", "", "")
	clip := decode(t, result)
	if !strings.Contains(string(clip["title"]), "4DGS") || !strings.Contains(string(clip["tags"]), "4DGS") {
		t.Fatal("dynamic result advertised as static")
	}
	result = call(t, h, "GET", "/api/v1/generation/jobs", "", "viewer", "")
	raw = decode(t, result)
	if !strings.Contains(string(raw["jobs"]), `"mode":"dynamic"`) {
		t.Fatal("history lost training mode")
	}
}
