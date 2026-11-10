package generation

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/publisher"
)

func scopedHandler(t *testing.T, s *Service, remote bool, principal *Principal) http.Handler {
	t.Helper()
	auth := func(*http.Request) (Principal, error) { return *principal, nil }
	var h http.Handler
	var err error
	if remote {
		client, _ := remoteFixture(t, s)
		h, err = NewPublisherRemoteHandler(client, auth)
	} else {
		h, err = NewPublisherHandler(s, auth)
	}
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func scopedRequest(t *testing.T, h http.Handler, method, path, body string, want int) []byte {
	t.Helper()
	r := httptest.NewRequest(method, "https://app.rangle.test"+path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	// None of these caller assertions may replace the trusted authenticator.
	r.Header.Set(ownerHeader, "admin")
	r.Header.Set(principalHeader, "forged")
	r.Header.Set("Authorization", "Bearer forged")
	r.Header.Set("Cookie", "rangle_session=forged;rangle_staff_session=forged")
	r.Header.Set("X-Admin", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	result := w.Result()
	defer result.Body.Close()
	data, err := io.ReadAll(result.Body)
	if err != nil || result.StatusCode != want || result.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("%s %s: status=%d body=%s error=%v", method, path, result.StatusCode, data, err)
	}
	for _, secret := range []string{"input_uri", "input_sha256", "provider_name", "created_by", "publisher_accounts", "publisher:club"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("private generation fields leaked", secret)
		}
	}
	return data
}

func TestPublisherAPIJobFlowAndSuccessorAccess(t *testing.T) {
	for name, remote := range map[string]bool{"local": false, "manager": true} {
		t.Run(name, func(t *testing.T) {
			s, provider := testService()
			p := staffPrincipal("club-a", "staff-a")
			s.Config.Presets[0].PublisherAccounts = []publisher.ID{"club-a"}
			h := scopedHandler(t, s, remote, &p)
			root := "/api/v1/publishers/club-a/generation/jobs"
			data := scopedRequest(t, h, "POST", root, `{"preset":"vru-frame0","request_id":"`+nonce(1)+`"}`, 202)
			var created struct{ Job Job }
			if json.Unmarshal(data, &created) != nil || created.Job.ID == "" {
				t.Fatal("missing job", string(data))
			}
			stored, _ := s.Store.Get(context.Background(), created.Job.ID)
			if stored.Owner != "publisher:club-a" || stored.CreatedBy.ID != "staff-a" || provider.submits.Load() != 1 {
				t.Fatal("browser claims replaced current staff authority", stored)
			}
			p = staffPrincipal("club-a", "staff-b")
			scopedRequest(t, h, "GET", root+"/"+created.Job.ID, "", 200)
			data = scopedRequest(t, h, "GET", root, "", 200)
			if !strings.Contains(string(data), created.Job.ID) {
				t.Fatal("successor list lost organization job")
			}
			scopedRequest(t, h, "DELETE", root+"/"+created.Job.ID, "", 202)
			if provider.cancels.Load() != 1 {
				t.Fatal("successor could not cancel")
			}
		})
	}
}

func TestPublisherAPIPublicationKeepsOrganizationAndProvenance(t *testing.T) {
	for name, remote := range map[string]bool{"local": false, "manager": true} {
		t.Run(name, func(t *testing.T) {
			creator := staffPrincipal("club-a", "staff-a")
			s, job, artifacts := fixturePublicationFor(t, "dynamic", creator)
			p := staffPrincipal("club-b", "staff-a")
			h := scopedHandler(t, s, remote, &p)
			scopedRequest(t, h, "PUT", "/api/v1/publishers/club-b/generation/jobs/"+job.ID+"/publication", `{"published":true}`, 404)
			if artifacts.requests.Load() != 0 {
				t.Fatal("foreign request fetched artifacts")
			}
			p = staffPrincipal("club-a", "staff-b")
			root := "/api/v1/publishers/club-a/generation/jobs/" + job.ID + "/publication"
			scopedRequest(t, h, "PUT", root, `{"published":true}`, 200)
			var clips []catalog.PublicClip
			var err error
			if remote {
				client, _ := remoteFixture(t, s)
				clips, err = client.PublicClips(context.Background())
			} else {
				clips, err = s.PublicClips(context.Background())
			}
			if err != nil || len(clips) != 1 || clips[0].Context.Publisher.ID != "club-a" || !strings.Contains(clips[0].Description, job.Snapshot.Source) {
				t.Fatal("public organization/source snapshot lost", clips, err)
			}
			scopedRequest(t, h, "PUT", root, `{"published":false}`, 200)
			stored, _ := s.Store.Get(context.Background(), job.ID)
			if stored.CreatedBy != job.CreatedBy || stored.PublisherAccountID != "club-a" || stored.Published {
				t.Fatal("successor changed ownership/creator")
			}
		})
	}
}

func TestPublisherAPIRefusesForeignPathsAndUnassignedInputs(t *testing.T) {
	for name, remote := range map[string]bool{"local": false, "manager": true} {
		for _, tc := range []struct {
			name, method, path, body string
			principal                Principal
			status                   int
		}{
			{"anonymous", "GET", "/api/v1/publishers/club-a/generation/presets", "", Principal{}, 401},
			{"operator cookie is separate", "GET", "/api/v1/publishers/club-a/generation/presets", "", OperatorPrincipal("admin"), 403},
			{"other organization path", "GET", "/api/v1/publishers/club-b/generation/presets", "", staffPrincipal("club-a", "staff-a"), 404},
			{"unassigned", "POST", "/api/v1/publishers/club-a/generation/jobs", `{"preset":"vru-frame0","request_id":"` + nonce(1) + `"}`, staffPrincipal("club-a", "staff-a"), 400},
			{"query on presets", "GET", "/api/v1/publishers/club-a/generation/presets?owner=club-b", "", staffPrincipal("club-a", "staff-a"), 400},
			{"query on job", "GET", "/api/v1/publishers/club-a/generation/jobs/" + nonce(1) + "?owner=club-b", "", staffPrincipal("club-a", "staff-a"), 404},
			{"forged body", "POST", "/api/v1/publishers/club-a/generation/jobs", `{"preset":"vru-frame0","request_id":"` + nonce(1) + `","publisher_account_id":"club-b"}`, staffPrincipal("club-a", "staff-a"), 400},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				s, provider := testService()
				h := scopedHandler(t, s, remote, &tc.principal)
				scopedRequest(t, h, tc.method, tc.path, tc.body, tc.status)
				if provider.submits.Load() != 0 || s.Store.(*memoryStore).a.Used != 0 {
					t.Fatal("denied request reserved GPU")
				}
			})
		}
		s, _ := testService()
		p := staffPrincipal("club-a", "staff-a")
		h := scopedHandler(t, s, remote, &p)
		data := scopedRequest(t, h, "GET", "/api/v1/publishers/club-a/generation/presets", "", 200)
		if !strings.Contains(string(data), `"presets":[]`) {
			t.Fatal("operator-only inputs exposed", string(data))
		}
	}
}
