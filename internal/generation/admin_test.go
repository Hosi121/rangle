package generation

import (
	"net/http"
	"strings"
	"testing"
)

func TestAllGenerationOperationsRequireAdministrator(t *testing.T) {
	s, provider := testService()
	h, err := NewHandler(s, func(*http.Request) (string, error) { return "", ErrForbidden })
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct{ method, path string }{
		{"GET", "/api/v1/generation/presets"},
		{"GET", "/api/v1/generation/jobs"},
		{"POST", "/api/v1/generation/jobs"},
		{"GET", "/api/v1/generation/jobs/" + nonce(1)},
		{"DELETE", "/api/v1/generation/jobs/" + nonce(1)},
		{"PUT", "/api/v1/generation/jobs/" + nonce(1) + "/publication"},
	} {
		t.Run(op.method+op.path, func(t *testing.T) {
			r := call(t, h, op.method, op.path, `{}`, "", "")
			if r.StatusCode != 403 {
				t.Fatalf("got %d", r.StatusCode)
			}
			if !strings.Contains(string(decode(t, r)["error"]), "admin_required") {
				t.Fatal("wrong access error")
			}
		})
	}
	if provider.submits.Load() != 0 {
		t.Fatal("denied request started a GPU job")
	}
	public, _, _ := testHandler(t)
	if got := call(t, public, "GET", "/api/v1/generation/presets", "", "", "").StatusCode; got != 401 {
		t.Fatalf("anonymous presets: %d", got)
	}
}
