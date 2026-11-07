package delivery_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/Hosi121/rangle/internal/delivery"
	"github.com/Hosi121/rangle/internal/stream"
)

const immutableCacheControl = "public, max-age=31536000, immutable"

func TestNewHandlerRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*stream.Manifest, map[string][]byte)
	}{
		{"manifest version", func(m *stream.Manifest, _ map[string][]byte) { m.Version++ }},
		{"invalid asset path", func(m *stream.Manifest, _ map[string][]byte) { m.Representations[0].Segments[0].URL = "/etc/passwd" }},
		{"invalid timeline", func(m *stream.Manifest, _ map[string][]byte) { m.Representations[0].Segments[0].StartSeconds = 1 }},
		{"dependent segment", func(m *stream.Manifest, _ map[string][]byte) { m.Representations[0].Segments[0].Independent = false }},
		{"malformed checksum", func(m *stream.Manifest, _ map[string][]byte) { m.Representations[0].Segments[0].SHA256 = "xyz" }},
		{"missing segment", func(m *stream.Manifest, assets map[string][]byte) {
			delete(assets, m.Representations[0].Segments[0].URL)
		}},
		{"wrong segment length", func(m *stream.Manifest, assets map[string][]byte) {
			url := m.Representations[0].Segments[0].URL
			assets[url] = assets[url][:len(assets[url])-1]
		}},
		{"wrong segment checksum", func(m *stream.Manifest, assets map[string][]byte) {
			assets[m.Representations[0].Segments[0].URL][0] ^= 0xff
		}},
		{"missing init", func(m *stream.Manifest, assets map[string][]byte) { delete(assets, m.Representations[0].InitURL) }},
		{"wrong init length", func(m *stream.Manifest, assets map[string][]byte) {
			assets[m.Representations[0].InitURL] = []byte("wrong size")
		}},
		{"unadvertised asset", func(_ *stream.Manifest, assets map[string][]byte) {
			assets["/assets/unadvertised.bin"] = []byte("extra")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest, assets := delivery.DemoFixture()
			test.mutate(&manifest, assets)
			handler, err := delivery.NewHandler(manifest, assets)
			if err == nil || handler != nil {
				t.Fatalf("NewHandler() = (%v, %v), want nil handler and validation error", handler, err)
			}
		})
	}
	manifest, _ := delivery.DemoFixture()
	if _, err := delivery.NewHandler(manifest, nil); err == nil {
		t.Fatal("NewHandler accepted a missing asset map")
	}
}

func TestNewHandlerAcceptsOptionalInitAndUppercaseChecksum(t *testing.T) {
	manifest, assets := delivery.DemoFixture()
	representation := &manifest.Representations[0]
	removed := representation.InitURL
	delete(assets, removed)
	representation.InitURL = ""
	representation.InitSizeBytes = 0
	representation.Segments[0].SHA256 = strings.ToUpper(representation.Segments[0].SHA256)
	handler, err := delivery.NewHandler(manifest, assets)
	if err != nil {
		t.Fatal(err)
	}
	if response := request(t, handler, http.MethodGet, removed, nil); response.StatusCode != http.StatusNotFound {
		t.Fatalf("removed init status = %d, want 404", response.StatusCode)
	}
}

func TestGetAndHeadResources(t *testing.T) {
	manifest, assets := delivery.DemoFixture()
	handler := mustHandler(t, manifest, assets)
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, url, contentType string
		data                   []byte
	}{
		{"manifest", manifest.URL(), "application/json", manifestData},
		{"segment", manifest.Representations[0].Segments[0].URL, "application/octet-stream", assets[manifest.Representations[0].Segments[0].URL]},
		{"init", manifest.Representations[0].InitURL, "application/octet-stream", assets[manifest.Representations[0].InitURL]},
	}
	for _, test := range tests {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			t.Run(test.name+" "+method, func(t *testing.T) {
				response := request(t, handler, method, test.url, nil)
				if response.StatusCode != http.StatusOK {
					t.Fatalf("status = %d, want 200", response.StatusCode)
				}
				if got := response.Header.Get("Content-Type"); got != test.contentType {
					t.Errorf("Content-Type = %q, want %q", got, test.contentType)
				}
				if got := response.Header.Get("Content-Length"); got != strconv.Itoa(len(test.data)) {
					t.Errorf("Content-Length = %q, want %d", got, len(test.data))
				}
				if got := response.Header.Get("Accept-Ranges"); got != "bytes" {
					t.Errorf("Accept-Ranges = %q, want bytes", got)
				}
				assertImmutable(t, response, test.data)
				body := readBody(t, response)
				if method == http.MethodHead {
					if len(body) != 0 {
						t.Error("HEAD response contains a body")
					}
				} else if !bytes.Equal(body, test.data) {
					t.Error("GET response bytes differ from input")
				}
			})
		}
	}
}

func TestRangeAndConditionals(t *testing.T) {
	manifest, assets := delivery.DemoFixture()
	handler := mustHandler(t, manifest, assets)
	url := manifest.Representations[0].Segments[0].URL
	data := assets[url]
	etag := request(t, handler, http.MethodGet, url, nil).Header.Get("ETag")
	tests := []struct {
		name        string
		method      string
		headers     map[string]string
		status      int
		body        []byte
		rangeHeader string
	}{
		{"byte range", http.MethodGet, map[string]string{"Range": "bytes=1-4"}, http.StatusPartialContent, data[1:5], fmt.Sprintf("bytes 1-4/%d", len(data))},
		{"suffix range", http.MethodGet, map[string]string{"Range": "bytes=-3"}, http.StatusPartialContent, data[len(data)-3:], fmt.Sprintf("bytes %d-%d/%d", len(data)-3, len(data)-1, len(data))},
		{"range HEAD", http.MethodHead, map[string]string{"Range": "bytes=1-4"}, http.StatusPartialContent, nil, fmt.Sprintf("bytes 1-4/%d", len(data))},
		{"If-None-Match", http.MethodGet, map[string]string{"If-None-Match": etag}, http.StatusNotModified, nil, ""},
		{"weak If-None-Match", http.MethodGet, map[string]string{"If-None-Match": "W/" + etag}, http.StatusNotModified, nil, ""},
		{"conditional HEAD", http.MethodHead, map[string]string{"If-None-Match": etag}, http.StatusNotModified, nil, ""},
		{"different If-None-Match", http.MethodGet, map[string]string{"If-None-Match": `"other"`}, http.StatusOK, data, ""},
		{"If-Match", http.MethodGet, map[string]string{"If-Match": etag}, http.StatusOK, data, ""},
		{"matching If-Range", http.MethodGet, map[string]string{"If-Range": etag, "Range": "bytes=1-4"}, http.StatusPartialContent, data[1:5], fmt.Sprintf("bytes 1-4/%d", len(data))},
		{"different If-Range", http.MethodGet, map[string]string{"If-Range": `"other"`, "Range": "bytes=1-4"}, http.StatusOK, data, ""},
		{"weak If-Range", http.MethodGet, map[string]string{"If-Range": "W/" + etag, "Range": "bytes=1-4"}, http.StatusOK, data, ""},
		{"precondition before invalid range", http.MethodGet, map[string]string{"If-None-Match": etag, "Range": "bytes=bad"}, http.StatusNotModified, nil, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, handler, test.method, url, test.headers)
			body := readBody(t, response)
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d; body %q", response.StatusCode, test.status, body)
			}
			if !bytes.Equal(body, test.body) {
				t.Errorf("body = %q, want %q", body, test.body)
			}
			if got := response.Header.Get("Content-Range"); got != test.rangeHeader {
				t.Errorf("Content-Range = %q, want %q", got, test.rangeHeader)
			}
			assertImmutable(t, response, data)
		})
	}
}

func TestMultipartRange(t *testing.T) {
	// Keep this multipart-specific test independent of the toolchain's default
	// range limit. The server retains the standard library's runtime policy.
	t.Setenv("GODEBUG", "httpservecontentmaxranges=200")
	manifest, assets := delivery.DemoFixture()
	handler := mustHandler(t, manifest, assets)
	url := manifest.Representations[0].Segments[0].URL
	response := request(t, handler, http.MethodGet, url, map[string]string{"Range": "bytes=0-1,5-7"})
	if response.StatusCode != http.StatusPartialContent {
		t.Fatalf("multipart status = %d, want 206", response.StatusCode)
	}
	mediaType, params, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/byteranges" {
		t.Fatalf("Content-Type = %q: %v", response.Header.Get("Content-Type"), err)
	}
	reader := multipart.NewReader(response.Body, params["boundary"])
	for index, span := range [][2]int{{0, 1}, {5, 7}} {
		part, err := reader.NextPart()
		if err != nil {
			t.Fatalf("part %d: %v", index, err)
		}
		body := new(bytes.Buffer)
		if _, err := body.ReadFrom(part); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(body.Bytes(), assets[url][span[0]:span[1]+1]) {
			t.Errorf("part %d has wrong bytes", index)
		}
		wantRange := fmt.Sprintf("bytes %d-%d/%d", span[0], span[1], len(assets[url]))
		if got := part.Header.Get("Content-Range"); got != wantRange {
			t.Errorf("part %d Content-Range = %q, want %q", index, got, wantRange)
		}
	}
	assertImmutable(t, response, assets[url])
}

func TestErrorsDoNotAdvertiseImmutableCaching(t *testing.T) {
	// Cache policy must hold even if the Go runtime is configured to retain
	// ServeContent headers on errors.
	t.Setenv("GODEBUG", "httpservecontentkeepheaders=1")
	manifest, assets := delivery.DemoFixture()
	handler := mustHandler(t, manifest, assets)
	url := manifest.Representations[0].Segments[0].URL
	etag := request(t, handler, http.MethodGet, url, nil).Header.Get("ETag")
	tests := []struct {
		name, method, url string
		headers           map[string]string
		status            int
	}{
		{"unknown", http.MethodGet, "/missing", nil, http.StatusNotFound},
		{"traversal", http.MethodGet, "/assets/../etc/passwd", nil, http.StatusNotFound},
		{"filesystem", http.MethodGet, "/etc/passwd", nil, http.StatusNotFound},
		{"encoded alias", http.MethodGet, strings.Replace(url, "/assets/", "/%61ssets/", 1), nil, http.StatusNotFound},
		{"query alias", http.MethodGet, url + "?other=1", nil, http.StatusNotFound},
		{"empty query alias", http.MethodGet, url + "?", nil, http.StatusNotFound},
		{"wrong revision", http.MethodGet, strings.Replace(url, manifest.Revision, "other", 1), nil, http.StatusNotFound},
		{"HEAD unknown", http.MethodHead, "/missing", nil, http.StatusNotFound},
		{"POST", http.MethodPost, url, nil, http.StatusMethodNotAllowed},
		{"OPTIONS", http.MethodOptions, manifest.URL(), nil, http.StatusMethodNotAllowed},
		{"unsatisfiable range", http.MethodGet, url, map[string]string{"Range": "bytes=99999-"}, http.StatusRequestedRangeNotSatisfiable},
		{"invalid range", http.MethodGet, url, map[string]string{"Range": "bytes=bad"}, http.StatusRequestedRangeNotSatisfiable},
		{"HEAD invalid range", http.MethodHead, url, map[string]string{"Range": "bytes=bad"}, http.StatusRequestedRangeNotSatisfiable},
		{"If-Range invalid range", http.MethodGet, url, map[string]string{"If-Range": etag, "Range": "bytes=bad"}, http.StatusRequestedRangeNotSatisfiable},
		{"If-Match failure", http.MethodGet, url, map[string]string{"If-Match": `"other"`}, http.StatusPreconditionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := request(t, handler, test.method, test.url, test.headers)
			if response.StatusCode != test.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, test.status)
			}
			if got := response.Header.Get("Cache-Control"); got != "no-store" {
				t.Errorf("error Cache-Control = %q, want no-store", got)
			}
			if got := response.Header.Get("ETag"); got != "" {
				t.Errorf("error ETag = %q, want empty", got)
			}
			if test.status == http.StatusMethodNotAllowed && response.Header.Get("Allow") != "GET, HEAD" {
				t.Errorf("405 Allow = %q", response.Header.Get("Allow"))
			}
			if test.method == http.MethodHead && len(readBody(t, response)) != 0 {
				t.Error("HEAD error response contains a body")
			}
		})
	}
}

func TestHandlerSnapshotsMutableInput(t *testing.T) {
	manifest, assets := delivery.DemoFixture()
	handler := mustHandler(t, manifest, assets)
	manifestURL := manifest.URL()
	segmentURL := manifest.Representations[0].Segments[0].URL
	initURL := manifest.Representations[0].InitURL
	urls := []string{manifestURL, segmentURL, initURL}
	type snapshot struct {
		response *http.Response
		body     []byte
	}
	before := make(map[string]snapshot)
	for _, url := range urls {
		response := request(t, handler, http.MethodGet, url, nil)
		before[url] = snapshot{response: response, body: readBody(t, response)}
	}
	manifest.ClipID = "changed"
	manifest.Codec = "changed"
	manifest.Representations[0].ID = "changed"
	manifest.Representations[0].Segments[0].URL = "/changed.bin"
	manifest.Representations[0].Segments[0].SHA256 = strings.Repeat("0", 64)
	assets[segmentURL][0] ^= 0xff
	assets[initURL][0] ^= 0xff
	assets[segmentURL] = []byte("replaced")
	delete(assets, initURL)
	assets["/changed.bin"] = []byte("added")
	for _, url := range urls {
		after := request(t, handler, http.MethodGet, url, nil)
		if after.StatusCode != before[url].response.StatusCode || !bytes.Equal(readBody(t, after), before[url].body) || !reflect.DeepEqual(after.Header, before[url].response.Header) {
			t.Errorf("resource %q changed after constructor input mutation", url)
		}
	}
	if got := request(t, handler, http.MethodGet, "/changed.bin", nil).StatusCode; got != http.StatusNotFound {
		t.Errorf("mutated map added a route: status %d", got)
	}
}

func TestRequestIgnoresHeaderChangesAfterWriteHeader(t *testing.T) {
	tests := []struct {
		name, header, committed, late string
	}{
		{"late cache directive", "Cache-Control", "", immutableCacheControl},
		{"replaced cache directive", "Cache-Control", "no-store", immutableCacheControl},
		{"removed validator", "ETag", `"committed"`, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.committed != "" {
					w.Header().Set(test.header, test.committed)
				}
				w.WriteHeader(http.StatusOK)
				if test.late == "" {
					w.Header().Del(test.header)
				} else {
					w.Header().Set(test.header, test.late)
				}
			})
			response := request(t, handler, http.MethodGet, "/", nil)
			if got := response.Header.Get(test.header); got != test.committed {
				t.Errorf("sent %s = %q, want committed value %q; late mutation was %q", test.header, got, test.committed, test.late)
			}
		})
	}
}

func mustHandler(t *testing.T, manifest stream.Manifest, assets map[string][]byte) http.Handler {
	t.Helper()
	handler, err := delivery.NewHandler(manifest, assets)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func request(t *testing.T, handler http.Handler, method, url string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(method, url, nil)
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	// Result snapshots the headers sent by the completed handler. Header() is
	// the handler's mutable map and can include changes made after WriteHeader.
	response := recorder.Result()
	t.Cleanup(func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close response body: %v", err)
		}
	})
	return response
}

func readBody(t *testing.T, response *http.Response) []byte {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func assertImmutable(t *testing.T, response *http.Response, resourceBytes []byte) {
	t.Helper()
	if got := response.Header.Get("Cache-Control"); got != immutableCacheControl {
		t.Errorf("Cache-Control = %q, want %q", got, immutableCacheControl)
	}
	sum := sha256.Sum256(resourceBytes)
	wantETag := `"sha256-` + hex.EncodeToString(sum[:]) + `"`
	if got := response.Header.Get("ETag"); got != wantETag {
		t.Errorf("ETag = %q, want strong content hash %q", got, wantETag)
	}
}
