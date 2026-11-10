package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func cdnCacheFixture(fixture *outputFixture, mode string) {
	model := "model.splat"
	if mode == "dynamic" {
		model = "model.rgs"
	}
	fixture.cacheControl = map[string][]string{}
	for _, name := range []string{"scene.json", "metrics.json", model} {
		fixture.cacheControl[name] = []string{"public,max-age=86400"}
	}
}

func TestPublicationAcceptsCDNRewrittenTTLWithoutImmutableHint(t *testing.T) {
	for _, mode := range []string{"static", "dynamic"} {
		t.Run(mode, func(t *testing.T) {
			service, job, fixture := fixturePublication(t, mode)
			cdnCacheFixture(fixture, mode)
			handler, err := NewHandler(service, func(*http.Request) (string, error) { return job.Owner, nil })
			if err != nil {
				t.Fatal(err)
			}
			response := call(t, handler, "PUT", "/api/v1/generation/jobs/"+job.ID+"/publication", `{"published":true}`, job.Owner, "")
			if response.StatusCode != 200 {
				t.Fatalf("CDN-compatible publication failed: %d %s", response.StatusCode, decode(t, response)["error"])
			}
			rows, err := service.PublicClips(context.Background())
			if err != nil || len(rows) != 1 || rows[0].ID != "generated-"+job.ID {
				t.Fatal("CDN artifact was not published", err)
			}
			publications, err := service.Store.(PublicationStore).Publications(context.Background())
			if err != nil || len(publications) != 1 || publications[0].Model.SHA256 != proof(fixture.model).SHA256 || publications[0].Model.Bytes != int64(len(fixture.model)) || fixture.requests.Load() != 3 {
				t.Fatal("CDN TTL acceptance bypassed artifact proofs", err)
			}
		})
	}
}

func TestCDNCacheAcceptanceStillRejectsMissingTamperedAndUncacheableArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*outputFixture)
	}{
		{"missing scene", func(f *outputFixture) { f.fail = "scene.json" }},
		{"missing metrics", func(f *outputFixture) { f.fail = "metrics.json" }},
		{"missing model", func(f *outputFixture) { f.fail = "model.rgs" }},
		{"tampered model", func(f *outputFixture) { f.model[len(f.model)-1] ^= 1 }},
		{"wrong provenance", func(f *outputFixture) { f.metrics["source"] = "https://unreviewed.invalid" }},
		{"wrong size", func(f *outputFixture) { f.metrics["model_bytes"] = len(f.model) + 1 }},
		{"missing cache policy", func(f *outputFixture) { f.cacheControl["scene.json"] = nil }},
		{"nonpublic scene", func(f *outputFixture) { f.cacheControl["scene.json"] = []string{"not-public,max-age=86400"} }},
		{"private metrics", func(f *outputFixture) { f.cacheControl["metrics.json"] = []string{"public,max-age=86400,private"} }},
		{"no-store model", func(f *outputFixture) { f.cacheControl["model.rgs"] = []string{"public,max-age=86400", "no-store"} }},
		{"no-cache model", func(f *outputFixture) { f.cacheControl["model.rgs"] = []string{"public,max-age=86400,no-cache"} }},
		{"zero shared TTL", func(f *outputFixture) { f.cacheControl["model.rgs"] = []string{"public,max-age=86400,s-maxage=0"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, job, fixture := fixturePublication(t, "dynamic")
			cdnCacheFixture(fixture, "dynamic")
			tc.change(fixture)
			if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); !errors.Is(err, ErrArtifacts) {
				t.Fatalf("invalid artifacts published: %v", err)
			}
			stored, err := service.Store.Get(context.Background(), job.ID)
			if err != nil || stored.Published {
				t.Fatal("failed validation changed job state", err)
			}
			rows, err := service.PublicClips(context.Background())
			if err != nil || len(rows) != 0 {
				t.Fatal("failed validation changed feed", err)
			}
		})
	}
}

func TestPublicationArtifactFailureWarnsOnceWithoutSecretsOrBody(t *testing.T) {
	service, job, fixture := fixturePublication(t, "dynamic")
	cdnCacheFixture(fixture, "dynamic")
	fixture.fail = "metrics.json"
	var logs bytes.Buffer
	service.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	handler, err := NewHandler(service, func(*http.Request) (string, error) { return job.Owner, nil })
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/generation/jobs/" + job.ID + "/publication"
	request := httptest.NewRequest("PUT", "https://app.rangle.test"+path, strings.NewReader(`{"published":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer secret-validation-token")
	request.Header.Set("Cookie", "rangle_session=secret-session")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 409 || !strings.Contains(response.Body.String(), "publication_unavailable") {
		t.Fatal("publication error contract changed")
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected one warning, got %d", len(lines))
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &record); err != nil {
		t.Fatal(err)
	}
	if record["level"] != "WARN" || record["job_id"] != job.ID || !strings.Contains(record["error"].(string), "metrics.json returned HTTP 404") {
		t.Fatalf("missing publication diagnosis: %s", lines[0])
	}
	for _, private := range []string{"secret-validation-token", "secret-session", `"published":true`, "preset_snapshot", "input_uri", "Authorization", "Cookie"} {
		if strings.Contains(logs.String(), private) {
			t.Fatalf("warning exposed %s", private)
		}
	}
}
