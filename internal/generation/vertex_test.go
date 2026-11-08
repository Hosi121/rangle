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

func TestVertexRequestPinsShapeIdentityImageAndDeadline(t *testing.T) {
	cfg := testConfig()
	created := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	id := nonce(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/projects/comin-up/locations/asia-northeast1/customJobs" || r.Method != "POST" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		var job aiplatform.GoogleCloudAiplatformV1CustomJob
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			t.Error(err)
		}
		if job.Labels["rangle_job"] != id || job.Labels["application"] != "rangle" || job.Labels["environment"] != "presentation-stg" {
			t.Error("missing ownership labels")
		}
		spec := job.JobSpec
		if spec == nil {
			t.Fatal("no job spec")
		}
		if spec.ServiceAccount != cfg.ServiceAccount || spec.Scheduling.Timeout != "1800s" || spec.Scheduling.Strategy != "SPOT" || !spec.Scheduling.DisableRetries {
			t.Error("training budget/identity changed")
		}
		if len(spec.WorkerPoolSpecs) != 1 {
			t.Fatal("multiple worker pools")
		}
		pool := spec.WorkerPoolSpecs[0]
		if pool.ReplicaCount != 1 || pool.MachineSpec.AcceleratorCount != 1 || pool.MachineSpec.AcceleratorType != "NVIDIA_TESLA_T4" || pool.MachineSpec.MachineType != "n1-standard-4" || pool.ContainerSpec.ImageUri != cfg.Image {
			t.Error("training shape or pinned image changed")
		}
		args := strings.Join(pool.ContainerSpec.Args, " ")
		if !strings.Contains(args, created.Add(MaxRuntime).Format(time.RFC3339Nano)) || !strings.Contains(args, cfg.Presets[0].InputSHA256) {
			t.Error("missing immutable input or absolute deadline")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"name": "projects/113704194486/locations/asia-northeast1/customJobs/123"})
	}))
	defer server.Close()
	v, err := NewVertex(context.Background(), cfg, option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.Submit(context.Background(), Job{ID: id, CreatedAt: created}, cfg.Presets[0]); err != nil {
		t.Fatal(err)
	}
}

func TestVertexDoesNotRetryAmbiguousCreate(t *testing.T) {
	for _, code := range []int{400, 429, 500, 503} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"error":{"message":"test failure"}}`))
			}))
			defer server.Close()
			v, err := NewVertex(context.Background(), testConfig(), option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
			if err != nil {
				t.Fatal(err)
			}
			_, err = v.Submit(context.Background(), Job{ID: nonce(1), CreatedAt: time.Now()}, testConfig().Presets[0])
			var rejected *Rejected
			if (code < 500) != errors.As(err, &rejected) {
				t.Fatalf("classification %d: %v", code, err)
			}
			if calls != 1 {
				t.Fatalf("create was attempted %d times", calls)
			}
		})
	}
}
