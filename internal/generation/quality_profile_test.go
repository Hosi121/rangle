package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"google.golang.org/api/aiplatform/v1"
	"google.golang.org/api/option"
)

func TestQualityProfilesAreReviewedServerSettings(t *testing.T) {
	for _, mode := range []string{"static", "dynamic"} {
		for _, profile := range []string{"", "baseline", "motion-prior", "local-lifetime", "unreviewed"} {
			t.Run(mode+"/"+profile, func(t *testing.T) {
				cfg := testConfig()
				cfg.Presets[0].Mode = mode
				cfg.Presets[0].QualityProfile = profile
				valid := profile == "" || profile == "baseline" || mode == "dynamic" && (profile == "motion-prior" || profile == "local-lifetime")
				if err := cfg.Validate(); (err == nil) != valid {
					t.Fatalf("valid=%v: %v", valid, err)
				}
			})
		}
	}
}

func TestVertexQualityProfileFlagsPreserveOlderWorkers(t *testing.T) {
	for _, mode := range []string{"static", "dynamic"} {
		for _, profile := range []string{"", "baseline", "motion-prior", "local-lifetime"} {
			if mode == "static" && profile != "" && profile != "baseline" {
				continue
			}
			t.Run(mode+"/"+profile, func(t *testing.T) {
				cfg := testConfig()
				preset := cfg.Presets[0]
				preset.Mode = mode
				preset.QualityProfile = profile
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					var job aiplatform.GoogleCloudAiplatformV1CustomJob
					if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					args := job.JobSpec.WorkerPoolSpecs[0].ContainerSpec.Args
					index := slices.Index(args, "--quality-profile")
					if profile == "" || profile == "baseline" {
						if index >= 0 {
							t.Error("default profile changed older worker arguments")
						}
					} else if index < 0 || index+1 >= len(args) || args[index+1] != profile {
						t.Errorf("missing profile: %v", args)
					}
					if mode == "static" && slices.Contains(args, "--mode") {
						t.Error("static worker received --mode")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"name":"projects/113704194486/locations/asia-northeast1/customJobs/123"}`))
				}))
				defer server.Close()
				vertex, err := NewVertex(context.Background(), cfg, option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := vertex.Submit(context.Background(), Job{ID: nonce(1)}, preset); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

func TestQualityProfileIsSnapshottedAndCannotBeChosenByPublicInput(t *testing.T) {
	service, _ := testService()
	service.Config.Presets[0].Mode = "dynamic"
	service.Config.Presets[0].QualityProfile = "motion-prior"
	job, err := service.Create(context.Background(), "owner", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	service.Config.Presets[0].QualityProfile = "local-lifetime"
	replay, err := service.Create(context.Background(), "owner", "vru-frame0", nonce(1))
	if err != nil || replay.ID != job.ID || replay.Snapshot.QualityProfile != "motion-prior" {
		t.Fatalf("snapshot changed: %+v %v", replay, err)
	}
	handler, _, provider := testHandler(t)
	response := call(t, handler, http.MethodPost, "/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(2)+`","quality_profile":"motion-prior"}`, "owner", "")
	if response.StatusCode != 400 || provider.submits.Load() != 0 {
		t.Fatal("public input selected a quality profile")
	}
}
