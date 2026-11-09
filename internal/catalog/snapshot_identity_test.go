package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func sourceDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestSourceIdentityDoesNotChangeResponseOrCursorBytes(t *testing.T) {
	compact := []byte(fixtureJSON(t, 3))
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, compact, "", "  "); err != nil {
		t.Fatal(err)
	}
	var previousBody []byte
	var previousETag, canonicalSnapshot string
	for _, data := range [][]byte{compact, formatted.Bytes()} {
		c := mustCatalog(t, string(data))
		if c.SourceSHA256() != sourceDigest(data) {
			t.Fatal("source identity does not match the exact catalog bytes")
		}
		if canonicalSnapshot != "" && c.snapshot != canonicalSnapshot {
			t.Fatal("formatting changed canonical cursor identity")
		}
		canonicalSnapshot = c.snapshot
		h, err := NewHandler(c, "https://media.example.test")
		if err != nil {
			t.Fatal(err)
		}
		response, body := request(t, h, http.MethodGet, feedPath+"?limit=1", nil)
		if response.Header.Get(catalogSHAHeader) != c.SourceSHA256() {
			t.Fatal("HTTP response did not identify the loaded source")
		}
		if previousBody != nil && (!bytes.Equal(previousBody, body) || previousETag != response.Header.Get("ETag")) {
			t.Fatal("source observability changed existing JSON, ETag or cursor bytes")
		}
		previousBody, previousETag = body, response.Header.Get("ETag")
	}
	if sourceDigest(compact) == sourceDigest(formatted.Bytes()) {
		t.Fatal("fixture did not exercise distinct source bytes")
	}
}

func TestStaticSourceIdentityStaysSeparateFromGeneratedRegistry(t *testing.T) {
	data := fixtureJSON(t, 3)
	var rows []PublicClip
	var loadError error
	h, err := WithPublicClips(mustHTTP(t, data, ""), func(context.Context) ([]PublicClip, error) {
		return rows, loadError
	})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := request(t, h, http.MethodGet, feedPath, nil)
	rows = []PublicClip{generatedMetadata(strings.Repeat("a", 32))}
	after, _ := request(t, h, http.MethodGet, feedPath, nil)
	if before.Header.Get("ETag") == after.Header.Get("ETag") {
		t.Fatal("registry update did not change the feed validator")
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		response, body := request(t, h, method, feedPath, map[string]string{"If-None-Match": after.Header.Get("ETag")})
		if response.StatusCode != http.StatusNotModified || len(body) != 0 || response.Header.Get(catalogSHAHeader) != sourceDigest([]byte(data)) {
			t.Fatal("conditional generated feed lost its static source identity")
		}
	}
	loadError = errors.New("registry unavailable")
	response, _ := request(t, h, http.MethodGet, feedPath, nil)
	if response.StatusCode != http.StatusServiceUnavailable || response.Header.Get(catalogSHAHeader) != sourceDigest([]byte(data)) || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("registry failure hid the selected static source or became cacheable")
	}
}
