package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"google.golang.org/api/aiplatform/v1"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestCompressionEvaluationIsDynamicOnlyAndOffByDefault(t *testing.T) {
	for _, mode := range []string{"static", "dynamic"} {
		for _, enabled := range []bool{false, true} {
			cfg := testConfig()
			cfg.Presets[0].Mode = mode
			cfg.Presets[0].EvaluateCompression = enabled
			if err := cfg.Validate(); (err == nil) != (mode == "dynamic" || !enabled) {
				t.Fatalf("mode=%s enabled=%v: %v", mode, enabled, err)
			}
		}
	}
}
func TestVertexAddsCompressionEvaluationOnlyWhenRequested(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := testConfig()
		preset := cfg.Presets[0]
		preset.Mode = "dynamic"
		preset.EvaluateCompression = enabled
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var job aiplatform.GoogleCloudAiplatformV1CustomJob
			if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			args := job.JobSpec.WorkerPoolSpecs[0].ContainerSpec.Args
			if slices.Contains(args, "--evaluate-compression") != enabled {
				t.Errorf("args: %v", args)
			}
			_, _ = w.Write([]byte(`{"name":"projects/113704194486/locations/asia-northeast1/customJobs/123"}`))
		}))
		vertex, err := NewVertex(context.Background(), cfg, option.WithEndpoint(server.URL+"/"), option.WithoutAuthentication())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := vertex.Submit(context.Background(), Job{ID: nonce(1)}, preset); err != nil {
			t.Fatal(err)
		}
		server.Close()
	}
	handler, _, provider := testHandler(t)
	response := call(t, handler, http.MethodPost, "/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(1)+`","evaluate_compression":true}`, "owner", "")
	if response.StatusCode != 400 || provider.submits.Load() != 0 {
		t.Fatal("public request changed evaluation settings")
	}
}
func TestFirestoreConstructorForwardsExplicitClientOptions(t *testing.T) {
	t.Setenv("FIRESTORE_EMULATOR_HOST", "")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	store, err := NewFirestoreStore(ctx, "demo-option-forwarding", "rangle-stg",
		option.WithEndpoint("127.0.0.1:1"), option.WithoutAuthentication(),
		option.WithGRPCDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
	if err != nil {
		t.Fatalf("explicit options were not accepted: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}
