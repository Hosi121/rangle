package generation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"google.golang.org/api/option"
)

func TestCloudCleanupUsesExactVersionsAndFailsClosedOnForeignObjects(t *testing.T) {
	prefix := "generated/" + strings.Repeat("a", 32) + "/"
	for _, tc := range []struct {
		name, object string
		deleteStatus int
		wantErr      bool
		deletes      int
	}{
		{"owned", prefix + "model.rgs", 204, false, 1},
		{"already absent", prefix + "model.rgs", 404, false, 1},
		{"generation changed", prefix + "model.rgs", 412, true, 1},
		{"foreign", "datasets/shared.npz", 204, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deletes := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" {
					if r.URL.Query().Get("prefix") != prefix || r.URL.Query().Get("versions") != "true" || r.URL.Query().Get("maxResults") != "100" {
						t.Error("unbounded listing", r.URL)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{"name": tc.object, "generation": "123"}}})
					return
				}
				deletes++
				if r.Method != "DELETE" || r.URL.Query().Get("generation") != "123" || r.URL.Query().Get("ifGenerationMatch") != "123" || !strings.HasSuffix(r.URL.Path, tc.object) {
					t.Error("unsafe object delete", r.URL)
				}
				w.WriteHeader(tc.deleteStatus)
				if tc.deleteStatus != 204 {
					_, _ = w.Write([]byte(`{"error":{"code":` + strconv.Itoa(tc.deleteStatus) + `,"message":"failed"}}`))
				}
			}))
			defer srv.Close()
			c, err := NewCloudAssetCleanup(context.Background(), testConfig(), "rangle-stg-https", option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
			if err != nil {
				t.Fatal(err)
			}
			empty, err := c.Purge(context.Background(), prefix)
			if (err != nil) != tc.wantErr || empty || deletes != tc.deletes {
				t.Fatal(empty, err, deletes)
			}
			if _, err := c.Purge(context.Background(), "generated/"); err == nil {
				t.Fatal("broad prefix allowed")
			}
		})
	}
}

func TestCloudCleanupConfirmsEmptyAndScopesIdempotentInvalidation(t *testing.T) {
	prefix := "live-index/" + strings.Repeat("a", 32) + ".json"
	rid := invalidationID(nonce(1), 0)
	calls := 0
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/invalidateCache") {
			calls++
			var body struct{ Host, Path string }
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Host != "media.rangle.test" || body.Path != "/"+prefix || r.URL.Query().Get("requestId") != rid || !strings.Contains(r.URL.Path, "/urlMaps/rangle-stg-https/") {
				t.Error("unscoped invalidation", r.URL, body)
			}
			_, _ = w.Write([]byte(`{"name":"operation-1","status":"PENDING"}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/operations/operation-1") {
			if fail {
				_, _ = w.Write([]byte(`{"name":"operation-1","status":"DONE","error":{"errors":[{"code":"FAILED"}]}}`))
			} else {
				_, _ = w.Write([]byte(`{"name":"operation-1","status":"DONE"}`))
			}
			return
		}
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	c, err := NewCloudAssetCleanup(context.Background(), testConfig(), "rangle-stg-https", option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	empty, err := c.Purge(context.Background(), prefix)
	if err != nil || !empty {
		t.Fatal(empty, err)
	}
	for range 2 {
		op, err := c.Invalidate(context.Background(), prefix, rid)
		if err != nil || op != "operation-1" {
			t.Fatal(op, err)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
	done, err := c.InvalidationDone(context.Background(), "operation-1")
	if err != nil || !done {
		t.Fatal(done, err)
	}
	fail = true
	done, err = c.InvalidationDone(context.Background(), "operation-1")
	if err == nil || done {
		t.Fatal("failed operation completed deletion")
	}
}
