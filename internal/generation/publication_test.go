package generation

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
	"golang.org/x/time/rate"
)

type publicationMemory struct {
	*memoryStore
	entries map[string]Publication
}

func newPublicationMemory() *publicationMemory {
	return &publicationMemory{newMemory(), map[string]Publication{}}
}
func (m *publicationMemory) Publications(context.Context) ([]Publication, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := []Publication{}
	for _, entry := range m.entries {
		rows = append(rows, entry)
	}
	return rows, nil
}
func (m *publicationMemory) SetPublication(_ context.Context, owner, id string, publication *Publication, now time.Time) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok || job.Owner != owner {
		return Job{}, ErrNotFound
	}
	if job.State != Succeeded {
		return Job{}, ErrConflict
	}
	if publication == nil {
		delete(m.entries, id)
	} else {
		m.entries[id] = *publication
	}
	if job.Published != (publication != nil) {
		job.UpdatedAt = now
	}
	job.Published = publication != nil
	m.jobs[id] = job
	return job, nil
}

type outputFixture struct {
	scene, metrics map[string]any
	model          []byte
	requests       atomic.Int32
	fail           string
	redirect       string
	cacheControl   map[string][]string
}

func fixturePublication(t *testing.T, mode string) (*Service, Job, *outputFixture) {
	t.Helper()
	return fixturePublicationFor(t, mode, OperatorPrincipal("owner"))
}

func fixturePublicationFor(t *testing.T, mode string, principal Principal) (*Service, Job, *outputFixture) {
	t.Helper()
	service, provider := testService()
	service.Store = newPublicationMemory()
	service.Config.Presets[0].Mode = mode
	if principal.Kind == "staff" {
		service.Config.Presets[0].PublisherAccounts = []publisher.ID{principal.Account.ID}
	}
	job, err := service.CreateFor(context.Background(), principal, "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	provider.state = Succeeded
	job, err = service.GetFor(context.Background(), principal, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &outputFixture{scene: map[string]any{"version": 1, "frameRate": 1, "camera": map[string]any{}, "attribution": map[string]any{"label": job.Snapshot.Title, "source": job.Snapshot.Source, "license": job.Snapshot.License}}, metrics: map[string]any{"job_id": job.ID, "mode": mode, "input_sha256": job.Snapshot.InputSHA256, "source": job.Snapshot.Source, "license": job.Snapshot.License}}
	if mode == "static" {
		fixture.scene["frames"] = []string{"model.splat"}
		raw := make([]byte, 64)
		var packed bytes.Buffer
		writer := gzip.NewWriter(&packed)
		_, _ = writer.Write(raw)
		_ = writer.Close()
		fixture.model = packed.Bytes()
		fixture.metrics["splat_bytes"] = len(raw)
		fixture.metrics["splat_sha256"] = proof(raw).SHA256
	} else {
		fixture.scene["frameCount"] = 3
		fixture.scene["temporal"] = map[string]any{"url": "model.rgs"}
		fixture.model = make([]byte, 32+56)
		copy(fixture.model, "RANGL4D1")
		for index, value := range []uint32{1, 56, 3, math.Float32bits(1), math.Float32bits(2), 0} {
			binary.LittleEndian.PutUint32(fixture.model[8+index*4:], value)
		}
		for _, offset := range []int{12, 16, 20, 48} {
			binary.LittleEndian.PutUint32(fixture.model[32+offset:], math.Float32bits(1))
		}
	}
	fixture.metrics["model_bytes"] = len(fixture.model)
	fixture.metrics["model_sha256"] = proof(fixture.model).SHA256
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.requests.Add(1)
		prefix := "/generated/" + job.ID + "/"
		name := strings.TrimPrefix(r.URL.Path, prefix)
		if !strings.HasPrefix(r.URL.Path, prefix) || r.URL.RawQuery != "" {
			t.Errorf("unexpected artifact URL: %s", r.URL)
			w.WriteHeader(400)
			return
		}
		if fixture.fail == name {
			w.WriteHeader(404)
			return
		}
		if fixture.redirect != "" {
			http.Redirect(w, r, fixture.redirect, 302)
			return
		}
		if values, ok := fixture.cacheControl[name]; ok {
			for _, value := range values {
				w.Header().Add("Cache-Control", value)
			}
		} else {
			w.Header().Set("Cache-Control", "public,max-age=31536000,immutable")
		}
		switch name {
		case "scene.json":
			_ = json.NewEncoder(w).Encode(fixture.scene)
		case "metrics.json":
			_ = json.NewEncoder(w).Encode(fixture.metrics)
		case "model.splat":
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write(fixture.model)
		case "model.rgs":
			_, _ = w.Write(fixture.model)
		default:
			t.Errorf("unexpected artifact name %q", name)
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(server.Close)
	service.Config.MediaOrigin = server.URL
	service.MediaClient = server.Client()
	return service, job, fixture
}

func TestPublicationValidatesBothOutputsAndKeepsUnlistedDeepLinks(t *testing.T) {
	for _, mode := range []string{"static", "dynamic"} {
		t.Run(mode, func(t *testing.T) {
			service, job, fixture := fixturePublication(t, mode)
			for range 2 {
				result, err := service.SetPublication(context.Background(), job.Owner, job.ID, true)
				if err != nil || !result.Published {
					t.Fatalf("publish: %+v %v", result, err)
				}
			}
			if fixture.requests.Load() != 3 {
				t.Fatal("idempotent publication downloaded artifacts again")
			}
			rows, err := service.PublicClips(context.Background())
			if err != nil || len(rows) != 1 {
				t.Fatalf("public clips: %+v %v", rows, err)
			}
			clip := rows[0]
			if clip.Codec != "" || clip.ManifestURL != "" || !strings.Contains(clip.Description, job.Snapshot.Source) || !strings.Contains(clip.Description, "CC BY 4.0") {
				t.Fatal("generated output lost provenance or advertised a fabricated transport")
			}
			for range 2 {
				result, err := service.SetPublication(context.Background(), job.Owner, job.ID, false)
				if err != nil || result.Published {
					t.Fatal("unpublish failed", err)
				}
			}
			if fixture.requests.Load() != 3 {
				t.Fatal("unpublishing must not depend on media availability")
			}
			rows, err = service.PublicClips(context.Background())
			if err != nil || len(rows) != 0 {
				t.Fatal("unlisted clip remained in feed")
			}
			handler, err := NewHandler(service, func(*http.Request) (string, error) { return "owner", nil })
			if err != nil {
				t.Fatal(err)
			}
			mux := http.NewServeMux()
			mux.Handle("/api/v1/clips/{clip}", handler.ClipHandler(http.NotFoundHandler()))
			response := call(t, mux, "GET", "/api/v1/clips/generated-"+job.ID, "", "", "")
			if response.StatusCode != 200 {
				t.Fatal("unpublication removed an existing result deep link")
			}
		})
	}
}

func TestPublicationFailureNeverChangesExistingFeed(t *testing.T) {
	cases := map[string]func(*Service, *outputFixture){
		"missing scene":   func(_ *Service, f *outputFixture) { f.fail = "scene.json" },
		"missing metrics": func(_ *Service, f *outputFixture) { f.fail = "metrics.json" },
		"missing model":   func(_ *Service, f *outputFixture) { f.fail = "model.rgs" },
		"foreign model": func(_ *Service, f *outputFixture) {
			f.scene["temporal"] = map[string]any{"url": "https://evil.invalid/model.rgs"}
		},
		"parent model":  func(_ *Service, f *outputFixture) { f.scene["temporal"] = map[string]any{"url": "../other/model.rgs"} },
		"wrong job":     func(_ *Service, f *outputFixture) { f.metrics["job_id"] = nonce(999) },
		"wrong input":   func(_ *Service, f *outputFixture) { f.metrics["input_sha256"] = strings.Repeat("c", 64) },
		"wrong hash":    func(_ *Service, f *outputFixture) { f.metrics["model_sha256"] = strings.Repeat("c", 64) },
		"wrong size":    func(_ *Service, f *outputFixture) { f.metrics["model_bytes"] = len(f.model) - 1 },
		"huge model":    func(_ *Service, f *outputFixture) { f.metrics["model_bytes"] = 33 << 20 },
		"wrong source":  func(_ *Service, f *outputFixture) { f.metrics["source"] = "https://evil.invalid" },
		"wrong profile": func(_ *Service, f *outputFixture) { f.metrics["quality_profile"] = "motion-prior" },
		"corrupt model": func(_ *Service, f *outputFixture) {
			f.model[0] = 'x'
			f.metrics["model_sha256"] = proof(f.model).SHA256
		},
		"redirect": func(_ *Service, f *outputFixture) { f.redirect = "https://evil.invalid/generated/scene.json" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			service, job, fixture := fixturePublication(t, "dynamic")
			mutate(service, fixture)
			if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); !errors.Is(err, ErrArtifacts) {
				t.Fatalf("validation passed: %v", err)
			}
			rows, err := service.PublicClips(context.Background())
			if err != nil || len(rows) != 0 {
				t.Fatal("failed validation changed the feed")
			}
			stored, err := service.Store.Get(context.Background(), job.ID)
			if err != nil || stored.Published {
				t.Fatal("failed validation marked job published")
			}
		})
	}
}

func TestPublicationHTTPAuthorizationStrictBodyAndJobViews(t *testing.T) {
	service, job, _ := fixturePublication(t, "dynamic")
	handler, err := NewHandler(service, func(r *http.Request) (string, error) { return r.Header.Get("X-Test-User"), nil })
	if err != nil {
		t.Fatal(err)
	}
	handler.limiter = rate.NewLimiter(rate.Inf, 100)
	path := "/api/v1/generation/jobs/" + job.ID + "/publication"
	for _, tc := range []struct {
		owner, origin, body string
		status              int
	}{
		{"", "", `{"published":true}`, 401}, {"other", "", `{"published":true}`, 404},
		{"owner", "https://evil.invalid", `{"published":true}`, 403},
		{"owner", "", `{}`, 400}, {"owner", "", `{"published":null}`, 400}, {"owner", "", `{"published":"true"}`, 400},
		{"owner", "", `{"published":true,"scene_url":"https://evil.invalid"}`, 400}, {"owner", "", `{"published":true}{}`, 400},
	} {
		response := call(t, handler, "PUT", path, tc.body, tc.owner, tc.origin)
		if response.StatusCode != tc.status {
			t.Fatalf("%+v: %d", tc, response.StatusCode)
		}
	}
	response := call(t, handler, "PUT", path, `{"published":true}`, "owner", "")
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	if !bytes.Contains(decode(t, response)["job"], []byte(`"published":true`)) {
		t.Fatal("publication response omitted state")
	}
	for _, target := range []string{"/api/v1/generation/jobs", "/api/v1/generation/jobs/" + job.ID} {
		response = call(t, handler, "GET", target, "", "owner", "")
		data := decode(t, response)
		encoded, _ := json.Marshal(data)
		if !bytes.Contains(encoded, []byte(`"published":true`)) {
			t.Fatalf("%s lost publication: %s", target, encoded)
		}
	}
	response = call(t, handler, "POST", "/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(1)+`"}`, "owner", "")
	if response.StatusCode != 202 || !bytes.Contains(decode(t, response)["job"], []byte(`"published":true`)) {
		t.Fatal("create replay lost publication state")
	}
	memory := service.Store.(*publicationMemory)
	memory.mu.Lock()
	changed := memory.jobs[job.ID]
	changed.State = Running
	changed.Published = false
	memory.jobs[job.ID] = changed
	memory.mu.Unlock()
	response = call(t, handler, "PUT", path, `{"published":true}`, "owner", "")
	if response.StatusCode != 409 {
		t.Fatal("unfinished job was published")
	}
}

func TestPublicationRejectsStaticIDCollisionAndUnreviewedSnapshot(t *testing.T) {
	service, job, fixture := fixturePublication(t, "static")
	service.ReservedClipID = func(id string) bool { return id == "generated-"+job.ID }
	if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	if fixture.requests.Load() != 0 {
		t.Fatal("reserved clip started media verification")
	}
	service.ReservedClipID = nil
	memory := service.Store.(*publicationMemory)
	memory.mu.Lock()
	stored := memory.jobs[job.ID]
	stored.Snapshot.License = "unreviewed"
	memory.jobs[job.ID] = stored
	memory.mu.Unlock()
	if _, err := service.SetPublication(context.Background(), job.Owner, job.ID, true); !errors.Is(err, ErrArtifacts) {
		t.Fatal(err)
	}
	if fixture.requests.Load() != 0 {
		t.Fatal("unreviewed preset started media verification")
	}
}

func TestPublicationHTTPUsesSpecificUnavailableCode(t *testing.T) {
	service, job, fixture := fixturePublication(t, "dynamic")
	fixture.fail = "metrics.json"
	handler, err := NewHandler(service, func(*http.Request) (string, error) { return job.Owner, nil })
	if err != nil {
		t.Fatal(err)
	}
	response := call(t, handler, "PUT", "/api/v1/generation/jobs/"+job.ID+"/publication", `{"published":true}`, job.Owner, "")
	if response.StatusCode != 409 || string(decode(t, response)["error"]) != `"publication_unavailable"` {
		t.Fatal("publication error contract changed")
	}
}
