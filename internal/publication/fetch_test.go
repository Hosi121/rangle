package publication

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func fixtureRemote(t *testing.T, f *fixture, change func(http.ResponseWriter, *http.Request, []byte) bool) (*remoteReader, map[string]int) {
	t.Helper()
	calls := map[string]int{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls[r.URL.Path]++
		if r.Header.Get("Accept-Encoding") != "identity" || r.Header.Get("Cache-Control") != "no-cache" {
			t.Error("remote checks must revalidate exact wire bytes")
		}
		data, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", mediaType(r.URL.Path))
		w.Header().Set("Cache-Control", "public,max-age=86400")
		if change != nil && change(w, r, data) {
			return
		}
		w.Write(data)
	}))
	t.Cleanup(server.Close)
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	r := newRemoteReader(origin)
	// Only the fixture replaces the secure network transport. The public API
	// offers no transport/resolver bypass or HTTP/private-origin switch.
	r.client.Transport = server.Client().Transport
	f.opts.Origin = origin.String()
	f.doc.Clips[0].SceneURL = origin.String() + f.prefix + "scene.json"
	f.doc.Clips[0].PreviewURL = origin.String() + f.prefix + "preview.webm"
	f.save()
	return r, calls
}

func TestHTTPSAllFilesAndCDNCacheRewrite(t *testing.T) {
	f := newFixture(t)
	r, calls := fixtureRemote(t, f, nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	receipt, err := verify(ctx, f.opts, r.origin, r.read)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Scope != "local-and-remote" || len(calls) != len(f.files) {
		t.Fatalf("not all references fetched: %v", calls)
	}
	for filename := range f.files {
		if calls[filename] != 1 {
			t.Fatalf("unexpected fetch count: %v", calls)
		}
	}
	// Reruns reverify all bytes; they don't trust an old receipt or advertise an
	// intermediate catalog. The deterministic result is identical.
	again, err := verify(ctx, f.opts, r.origin, r.read)
	if err != nil {
		t.Fatal(err)
	}
	if contentHash(receipt) != contentHash(again) {
		t.Fatal("rerun changed receipt")
	}
	for filename := range f.files {
		if calls[filename] != 2 {
			t.Fatalf("rerun skipped verification: %v", calls)
		}
	}
}

func TestHTTPSGateRejectsRemoteFailures(t *testing.T) {
	tests := []struct {
		name   string
		change func(http.ResponseWriter, *http.Request, []byte) bool
	}{
		{"missing", func(w http.ResponseWriter, r *http.Request, b []byte) bool { http.NotFound(w, r); return true }},
		{"tampered", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			copy := append([]byte{}, b...)
			copy[len(copy)-1] ^= 1
			w.Write(copy)
			return true
		}},
		{"truncated", func(w http.ResponseWriter, r *http.Request, b []byte) bool { w.Write(b[:len(b)-1]); return true }},
		{"oversized", func(w http.ResponseWriter, r *http.Request, b []byte) bool { w.Write(append(b, 1)); return true }},
		{"redirect to same origin", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
			return true
		}},
		{"redirect to private", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			http.Redirect(w, r, "http://169.254.169.254/", http.StatusFound)
			return true
		}},
		{"private cache", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			w.Header().Set("Cache-Control", "private,max-age=86400")
			return false
		}},
		{"no cache", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			w.Header().Set("Cache-Control", "public,max-age=86400,no-cache")
			return false
		}},
		{"no store", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			w.Header().Set("Cache-Control", "public,max-age=86400,no-store")
			return false
		}},
		{"substring public", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			w.Header().Set("Cache-Control", "not-public,max-age=86400")
			return false
		}},
		{"wrong content type", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			w.Header().Set("Content-Type", "text/html")
			return false
		}},
		{"unexpected compression", func(w http.ResponseWriter, r *http.Request, b []byte) bool {
			w.Header().Set("Content-Encoding", "gzip")
			return false
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			r, calls := fixtureRemote(t, f, tt.change)
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			if receipt, err := verify(ctx, f.opts, r.origin, r.read); err == nil || receipt != nil {
				t.Fatal("remote failure yielded receipt")
			}
			if calls["/other"] != 0 {
				t.Fatal("followed same-origin redirect")
			}
		})
	}
}

func TestFixedOriginAndPublicDial(t *testing.T) {
	for _, value := range []string{"http://media.example.com", "https://user@media.example.com", "https://media.example.com/path", "https://media.example.com?token=secret", "https://media.example.com#fragment", "https://media.example.com:8443"} {
		if _, err := originURL(value); err == nil {
			t.Errorf("accepted invalid origin %q", value)
		}
	}
	origin, _ := originURL("https://media.example.com")
	for _, value := range []string{"//private.example/model.rgs", "https://private.example/model.rgs", "http://media.example.com/model.rgs", "/assets/a/../model.rgs", "/assets/a/%2e%2e/model.rgs", "/assets/a/model.rgs?token=x", "/assets/a/model.rgs#x", "/assets/a\\model.rgs", "/assets/a/model.rgs%00"} {
		if _, err := assetPath(value, origin); err == nil {
			t.Errorf("accepted escaped asset %q", value)
		}
	}
	for _, value := range []string{"127.0.0.1", "10.0.0.1", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "224.0.0.1", "198.18.0.1", "192.0.2.1", "::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "2002:0a00:0001::", "2001:db8::1"} {
		if publicIP(netip.MustParseAddr(value)) {
			t.Errorf("accepted non-public IP %s", value)
		}
	}
	for _, value := range []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888", "2606:4700:4700::1111"} {
		if !publicIP(netip.MustParseAddr(value)) {
			t.Errorf("rejected public IP %s", value)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, address := range []string{"127.0.0.1:443", "[::1]:443", "169.254.169.254:443", "localhost:443", "1.1.1.1:80"} {
		if conn, err := publicDial(ctx, "tcp", address); err == nil {
			conn.Close()
			t.Errorf("dialed private/disallowed address %s", address)
		}
	}
	transport := newRemoteReader(origin).client.Transport.(*http.Transport)
	if transport.Proxy != nil || !transport.DisableCompression {
		t.Fatal("transport permits proxy/automatic decoding")
	}
}

func TestCacheDirectives(t *testing.T) {
	for _, header := range []string{"public,max-age=86400", "public,max-age=31536000,immutable", `public,max-age="86400"`, "public,max-age=0,s-maxage=60", `public,max-age=60,x="comma,value"`} {
		if !cacheable([]string{header}) {
			t.Errorf("rejected valid cache policy %q", header)
		}
	}
	for _, header := range []string{"not-public,max-age=60", "public,max-age=60,no-store", `public,max-age=60,private="x"`, "public,max-age=60,no-cache", "public,max-age=0", "public,max-age=60,s-maxage=0", "public,max-age=60,max-age=60", "public,max-age=-1", "public,max-age=garbage", "public,max-age=99999999999999999999999999999", `x="public,max-age=60"`, `x="ignore,public,max-age=60,foo="`, `public,max-age="60`, "public=1,max-age=60"} {
		if cacheable([]string{header}) {
			t.Errorf("accepted invalid cache policy %q", header)
		}
	}
	if cacheable([]string{"public,max-age=60", "no-store"}) {
		t.Fatal("ignored second header")
	}
	if !cacheable([]string{"public", "s-maxage=60"}) {
		t.Fatal("rejected shared TTL")
	}
}

func TestDeadlineRequiredAndCanceled(t *testing.T) {
	f := newFixture(t)
	if _, err := Verify(context.Background(), f.opts); err == nil || !strings.Contains(err.Error(), "deadline") {
		t.Fatal("unbounded verification accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	cancel()
	if _, err := Verify(ctx, f.opts); err == nil {
		t.Fatal("canceled verification accepted")
	}
}
