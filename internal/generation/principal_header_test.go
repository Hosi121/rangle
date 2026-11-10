package generation

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManagerRequiresTrustedServiceAndUnambiguousFreshPrincipal(t *testing.T) {
	s, provider := testService()
	h := managerFixture(t, s)
	p := staffPrincipal("club-a", "staff-a")
	valid, err := encodePrincipal(p, s.now())
	if err != nil {
		t.Fatal(err)
	}
	stale, _ := encodePrincipal(p, s.now().Add(-31*time.Second))
	future, _ := encodePrincipal(p, s.now().Add(6*time.Second))
	raw, _ := base64.RawURLEncoding.DecodeString(valid)
	encode := func(raw []byte) string { return base64.RawURLEncoding.EncodeToString(raw) }
	for _, tc := range []struct {
		name         string
		values       []string
		owner, token string
		status       int
	}{
		{"missing Google identity", []string{valid}, "", "", 401},
		{"forged Google identity", []string{valid}, "", "forged", 401},
		{"missing actor", nil, "", "signed", 403},
		{"stale", []string{stale}, "", "signed", 403},
		{"future", []string{future}, "", "signed", 403},
		{"empty", []string{""}, "", "signed", 403},
		{"malformed", []string{"!"}, "", "signed", 403},
		{"oversized", []string{strings.Repeat("a", 4097)}, "", "signed", 403},
		{"repeated header", []string{valid, valid}, "", "signed", 403},
		{"legacy and new", []string{valid}, "admin", "signed", 403},
		{"unknown field", []string{encode(append(append([]byte(nil), raw[:len(raw)-1]...), []byte(`,"admin":true}`)...))}, "", "signed", 403},
		{"duplicate field", []string{encode([]byte(strings.Replace(string(raw), `"v":1`, `"v":1,"v":1`, 1)))}, "", "signed", 403},
		{"unknown role", []string{encode([]byte(strings.Replace(string(raw), `"role":"editor"`, `"role":"operator"`, 1)))}, "", "signed", 403},
		{"unsupported version", []string{encode([]byte(strings.Replace(string(raw), `"v":1`, `"v":2`, 1)))}, "", "signed", 403},
		{"legacy publisher namespace", nil, "publisher:club-a", "signed", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", managerURL+"/api/v1/generation/jobs", strings.NewReader(`{"preset":"vru-frame0","request_id":"`+nonce(1)+`"}`))
			r.Header.Set("Content-Type", "application/json")
			for _, value := range tc.values {
				r.Header.Add(principalHeader, value)
			}
			if tc.owner != "" {
				r.Header.Set(ownerHeader, tc.owner)
			}
			if tc.token != "" {
				r.Header.Set(serviceAuthorizationHeader, "Bearer "+tc.token)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			result := w.Result()
			defer result.Body.Close()
			if result.StatusCode != tc.status || result.Header.Get("Cache-Control") != "no-store" {
				t.Fatal(result.StatusCode, w.Body.String())
			}
		})
	}
	if provider.submits.Load() != 0 || s.Store.(*memoryStore).a.Used != 0 {
		t.Fatal("untrusted assertions consumed admission")
	}
	for _, offset := range []time.Duration{-30 * time.Second, 5 * time.Second} {
		value, _ := encodePrincipal(p, s.now().Add(offset))
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set(principalHeader, value)
		got, err := managerPrincipal(r, s.now())
		if err != nil || got.ID != p.ID || got.Account.ID != p.Account.ID {
			t.Fatal("valid lifetime boundary rejected", offset, err)
		}
	}
}

func TestPublisherRemoteChecksSessionAndCSRFAheadOfManager(t *testing.T) {
	s, provider := testService()
	remote, tokens := remoteFixture(t, s)
	p := Principal{}
	h, err := NewPublisherRemoteHandler(remote, func(*http.Request) (Principal, error) { return p, nil })
	if err != nil {
		t.Fatal(err)
	}
	scopedRequest(t, h, "POST", "/api/v1/publishers/club-a/generation/jobs", `{}`, 401)
	p = staffPrincipal("club-a", "staff-a")
	r := httptest.NewRequest("POST", "https://app.rangle.test/api/v1/publishers/club-a/generation/jobs", strings.NewReader(`{}`))
	r.Header.Set("Origin", "https://other.invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || tokens.calls.Load() != 0 || provider.submits.Load() != 0 {
		t.Fatal("untrusted browser reached manager", w.Code)
	}
}
