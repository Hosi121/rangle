package generation

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
)

// This public fixture is the earlier static-worker metrics document at
// https://media.rangleplay.stream/generated/e7ef956d504cd595c4d8e081b91fff70/metrics.json.
// Its full evaluation fields are retained; only per-job/input/raw measurements
// are substituted below to exercise the same schema with a small local model.
func legacyStaticFixture(t *testing.T) (*Service, Job, *outputFixture) {
	t.Helper()
	service, job, fixture := fixturePublication(t, "static")
	data, err := os.ReadFile("testdata/legacy-static-metrics.json")
	if err != nil {
		t.Fatal(err)
	}
	var metrics map[string]any
	if err := json.Unmarshal(data, &metrics); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mode", "model_bytes", "model_sha256"} {
		if _, ok := metrics[key]; ok {
			t.Fatal("fixture no longer has the real legacy schema", key)
		}
	}
	metrics["job_id"] = job.ID
	metrics["input_sha256"] = job.Snapshot.InputSHA256
	metrics["splat_bytes"] = fixture.metrics["splat_bytes"]
	metrics["splat_sha256"] = fixture.metrics["splat_sha256"]
	fixture.metrics = metrics
	cdnCacheFixture(fixture, "static")
	return service, job, fixture
}

func TestLegacyStaticMetricsPublishWithMandatoryRawProofAndObservedCompressedProof(t *testing.T) {
	for _, snapshotMode := range []string{"", "static"} {
		t.Run("snapshot="+snapshotMode, func(t *testing.T) {
			service, job, fixture := legacyStaticFixture(t)
			store := service.Store.(*publicationMemory)
			stored := store.jobs[job.ID]
			stored.Snapshot.Mode = snapshotMode
			store.jobs[job.ID] = stored
			handler, err := NewHandler(service, func(*http.Request) (string, error) { return job.Owner, nil })
			if err != nil {
				t.Fatal(err)
			}
			response := call(t, handler, "PUT", "/api/v1/generation/jobs/"+job.ID+"/publication", `{"published":true}`, job.Owner, "")
			if response.StatusCode != 200 {
				t.Fatalf("legacy static publication: %d %s", response.StatusCode, decode(t, response)["error"])
			}
			rows, err := store.Publications(context.Background())
			if err != nil || len(rows) != 1 {
				t.Fatal("publication not persisted", err)
			}
			if rows[0].Model != proof(fixture.model) || rows[0].Model.SHA256 == fixture.metrics["splat_sha256"] {
				t.Fatal("compressed ArtifactProof did not record observed bytes")
			}
			if fixture.requests.Load() != 3 {
				t.Fatal("publication did not read the complete scene/metrics/model set")
			}
		})
	}
	// The compatibility contract is not an exact historical dictionary: explicit
	// static mode, baseline quality and current model measurements also work.
	service, job, fixture := legacyStaticFixture(t)
	fixture.metrics["mode"] = "static"
	fixture.metrics["quality_profile"] = "baseline"
	fixture.metrics["model_bytes"] = len(fixture.model)
	fixture.metrics["model_sha256"] = proof(fixture.model).SHA256
	if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); err != nil {
		t.Fatal("common worker schema was rejected", err)
	}
}

func TestStaticMetricsNeverSkipRawValidationOrIgnorePartialCompressedMeasurements(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*outputFixture)
	}{
		{"wrong job", func(f *outputFixture) { f.metrics["job_id"] = nonce(999) }},
		{"wrong input", func(f *outputFixture) { f.metrics["input_sha256"] = strings.Repeat("a", 64) }},
		{"wrong source", func(f *outputFixture) { f.metrics["source"] = "https://unreviewed.invalid" }},
		{"wrong license", func(f *outputFixture) { f.metrics["license"] = "unreviewed" }},
		{"missing raw size", func(f *outputFixture) { delete(f.metrics, "splat_bytes") }},
		{"missing raw hash", func(f *outputFixture) { delete(f.metrics, "splat_sha256") }},
		{"zero raw size", func(f *outputFixture) { f.metrics["splat_bytes"] = 0 }},
		{"wrong raw size", func(f *outputFixture) { f.metrics["splat_bytes"] = 32 }},
		{"wrong raw hash", func(f *outputFixture) { f.metrics["splat_sha256"] = strings.Repeat("a", 64) }},
		{"null raw size", func(f *outputFixture) { f.metrics["splat_bytes"] = nil }},
		{"tampered valid gzip", func(f *outputFixture) {
			var packed bytes.Buffer
			writer := gzip.NewWriter(&packed)
			_, _ = writer.Write(bytes.Repeat([]byte{1}, 64))
			_ = writer.Close()
			f.model = packed.Bytes()
		}},
		{"compressed size only", func(f *outputFixture) { f.metrics["model_bytes"] = len(f.model) }},
		{"compressed hash only", func(f *outputFixture) { f.metrics["model_sha256"] = proof(f.model).SHA256 }},
		{"null compressed pair", func(f *outputFixture) { f.metrics["model_bytes"] = nil; f.metrics["model_sha256"] = nil }},
		{"zero compressed size", func(f *outputFixture) {
			f.metrics["model_bytes"] = 0
			f.metrics["model_sha256"] = proof(f.model).SHA256
		}},
		{"invalid compressed hash", func(f *outputFixture) {
			f.metrics["model_bytes"] = len(f.model)
			f.metrics["model_sha256"] = "not-sha256"
		}},
		{"wrong compressed hash", func(f *outputFixture) {
			f.metrics["model_bytes"] = len(f.model)
			f.metrics["model_sha256"] = strings.Repeat("a", 64)
		}},
		{"wrong compressed size", func(f *outputFixture) {
			f.metrics["model_bytes"] = len(f.model) + 1
			f.metrics["model_sha256"] = proof(f.model).SHA256
		}},
		{"null mode", func(f *outputFixture) { f.metrics["mode"] = nil }},
		{"empty mode", func(f *outputFixture) { f.metrics["mode"] = "" }},
		{"wrong explicit mode", func(f *outputFixture) { f.metrics["mode"] = "dynamic" }},
		{"new schema without raw proof", func(f *outputFixture) {
			f.metrics["mode"] = "static"
			f.metrics["model_bytes"] = len(f.model)
			f.metrics["model_sha256"] = proof(f.model).SHA256
			delete(f.metrics, "splat_sha256")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, job, fixture := legacyStaticFixture(t)
			tc.change(fixture)
			if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); !errors.Is(err, ErrArtifacts) {
				t.Fatalf("invalid static metrics published: %v", err)
			}
			stored, err := service.Store.Get(context.Background(), job.ID)
			if err != nil || stored.Published {
				t.Fatal("failed validation changed publication", err)
			}
			public, err := service.PublicClips(context.Background())
			if err != nil || len(public) != 0 {
				t.Fatal("failed validation changed feed", err)
			}
		})
	}
}

func TestDynamicMetricsCannotUseStaticCompatibilityOrOmitStoredModelProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*outputFixture)
	}{
		{"missing mode", func(f *outputFixture) { delete(f.metrics, "mode") }},
		{"null mode", func(f *outputFixture) { f.metrics["mode"] = nil }},
		{"static mode", func(f *outputFixture) { f.metrics["mode"] = "static" }},
		{"missing model size", func(f *outputFixture) { delete(f.metrics, "model_bytes") }},
		{"missing model hash", func(f *outputFixture) { delete(f.metrics, "model_sha256") }},
		{"static shape", func(f *outputFixture) {
			delete(f.metrics, "mode")
			delete(f.metrics, "model_bytes")
			delete(f.metrics, "model_sha256")
			f.metrics["splat_bytes"] = len(f.model)
			f.metrics["splat_sha256"] = proof(f.model).SHA256
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, job, fixture := fixturePublication(t, "dynamic")
			tc.change(fixture)
			if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); !errors.Is(err, ErrArtifacts) {
				t.Fatalf("invalid dynamic metrics published: %v", err)
			}
		})
	}
}
