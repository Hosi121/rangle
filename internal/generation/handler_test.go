package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testHandler(t *testing.T) (*Handler, *Service, *fakeProvider) {
	t.Helper()
	s, p := testService()
	h, err := NewHandler(s, func(r *http.Request) (string, error) { return r.Header.Get("X-Test-User"), nil })
	if err != nil {
		t.Fatal(err)
	}
	return h, s, p
}
func call(t *testing.T, h http.Handler, method, path, body, owner, origin string) *http.Response {
	t.Helper()
	r := httptest.NewRequest(method, "https://app.rangle.test"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json; charset=utf-8")
	if owner != "" {
		r.Header.Set("X-Test-User", owner)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	result := w.Result()
	t.Cleanup(func() { result.Body.Close() })
	if got := result.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("response cache policy = %q", got)
	}
	return result
}
func decode(t *testing.T, r *http.Response) map[string]json.RawMessage {
	t.Helper()
	var v map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestHTTPRejectsAnonymousAndCrossOriginWithoutGPU(t *testing.T) {
	h, _, p := testHandler(t)
	body := `{"preset":"vru-frame0","request_id":"` + nonce(1) + `"}`
	for _, tc := range []struct {
		owner, origin string
		status        int
	}{{"", "", 401}, {"viewer", "https://other.invalid", 403}} {
		result := call(t, h, "POST", "/api/v1/generation/jobs", body, tc.owner, tc.origin)
		if result.StatusCode != tc.status {
			t.Fatalf("got %d want %d", result.StatusCode, tc.status)
		}
	}
	if p.submits.Load() != 0 {
		t.Fatal("rejected request submitted a GPU job")
	}
}

func TestHTTPJobFlowKeepsInternalInputsPrivate(t *testing.T) {
	h, s, p := testHandler(t)
	presets := call(t, h, "GET", "/api/v1/generation/presets", "", "viewer", "")
	public := decode(t, presets)
	if strings.Contains(string(public["presets"]), "gs://") {
		t.Fatal("public preset exposed internal inputs")
	}
	result := call(t, h, "POST", "/api/v1/generation/jobs", `{"preset":"vru-frame0","request_id":"`+nonce(1)+`"}`, "viewer", "https://app.rangle.test")
	if result.StatusCode != 202 {
		t.Fatalf("%d", result.StatusCode)
	}
	row := decode(t, result)
	var job Job
	if err := json.Unmarshal(row["job"], &job); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(row["job"]), "provider_name") || strings.Contains(string(row["job"]), "owner") {
		t.Fatal("private job metadata exposed")
	}
	if _, ok := row["viewer_url"]; ok {
		t.Fatal("unfinished job exposed a viewer")
	}
	other := call(t, h, "GET", "/api/v1/generation/jobs/"+job.ID, "", "other", "")
	if other.StatusCode != 404 {
		t.Fatalf("other owner: %d", other.StatusCode)
	}
	p.state = Succeeded
	result = call(t, h, "GET", "/api/v1/generation/jobs/"+job.ID, "", "viewer", "")
	row = decode(t, result)
	var viewer string
	_ = json.Unmarshal(row["viewer_url"], &viewer)
	if viewer != "/demo/?clip=generated-"+job.ID {
		t.Fatalf("viewer = %q", viewer)
	}
	stored, err := s.Store.Get(context.Background(), job.ID)
	if err != nil || stored.State != Succeeded {
		t.Fatalf("%+v %v", stored, err)
	}
}

func TestHTTPStrictRequestDoesNotAcceptArbitraryTrainingParameters(t *testing.T) {
	for _, body := range []string{
		`{"preset":"vru-frame0","request_id":"` + nonce(1) + `","image":"attacker/image"}`,
		`{"preset":"unknown","request_id":"` + nonce(1) + `"}`,
		`{"preset":"vru-frame0","request_id":"not-a-random-request-id"}`,
		`{"preset":"vru-frame0","request_id":"` + nonce(1) + `"} {}`,
	} {
		h, _, p := testHandler(t)
		result := call(t, h, "POST", "/api/v1/generation/jobs", body, "viewer", "")
		if result.StatusCode != 400 {
			t.Fatalf("body %s returned %d", body, result.StatusCode)
		}
		if p.submits.Load() != 0 {
			t.Fatal("invalid input created a job")
		}
	}
}

func TestGeneratedClipMetadataWorksWithExistingDeepLinkAndFallback(t *testing.T) {
	h, s, p := testHandler(t)
	job, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	p.state = Succeeded
	if _, err = s.Get(context.Background(), "viewer", job.ID); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	fallbackCalls := 0
	mux.Handle("/api/v1/clips/{clip}", h.ClipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fallbackCalls++
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(204)
	})))
	result := call(t, mux, "GET", "/api/v1/clips/generated-"+job.ID, "", "", "")
	if result.StatusCode != 200 {
		t.Fatalf("generated clip: %d", result.StatusCode)
	}
	data := decode(t, result)
	var id, scene string
	_ = json.Unmarshal(data["id"], &id)
	_ = json.Unmarshal(data["scene_url"], &scene)
	if id != "generated-"+job.ID || !strings.HasSuffix(scene, "/generated/"+job.ID+"/scene.json") {
		t.Fatalf("%s %s", id, scene)
	}
	result = call(t, mux, "GET", "/api/v1/clips/basketball", "", "", "")
	if result.StatusCode != 204 || fallbackCalls != 1 {
		t.Fatal("existing catalog detail was intercepted")
	}
}
