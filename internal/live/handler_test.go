package live

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testClock struct{ nanos atomic.Int64 }

func (c *testClock) now() time.Time          { return time.Unix(0, c.nanos.Load()) }
func (c *testClock) advance(d time.Duration) { c.nanos.Add(int64(d)) }

func testHandler(t *testing.T, read func(context.Context) ([]byte, error)) (*handler, *testClock) {
	t.Helper()
	h, err := NewHandler(Config{Read: read})
	if err != nil {
		t.Fatal(err)
	}
	clock := &testClock{}
	clock.nanos.Store(time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC).UnixNano())
	value := h.(*handler)
	value.now = clock.now
	return value, clock
}

func request(t *testing.T, client *http.Client, method, url, etag string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestHTTPIndexSessionHEADAndConditionalGET(t *testing.T) {
	data := marshal(t, validIndex())
	var calls atomic.Int32
	h, _ := testHandler(t, func(context.Context) ([]byte, error) { calls.Add(1); return data, nil })
	server := httptest.NewServer(h)
	defer server.Close()
	index, body := request(t, server.Client(), "GET", server.URL+"/api/v1/live", "")
	if index.StatusCode != 200 || index.Header.Get("Cache-Control") != cacheControl || index.Header.Get("X-Content-Type-Options") != "nosniff" || index.Header.Get("Content-Type") != "application/json; charset=utf-8" || index.Header.Get("ETag") == "" || index.ContentLength != int64(len(body)) {
		t.Fatalf("incorrect GET headers: %+v", index)
	}
	var parsed Index
	if err := json.Unmarshal(body, &parsed); err != nil || len(parsed.Sessions) != 1 {
		t.Fatalf("incorrect index: %s %v", body, err)
	}
	head, headBody := request(t, server.Client(), "HEAD", server.URL+"/api/v1/live", "")
	if head.StatusCode != 200 || len(headBody) != 0 || head.ContentLength != int64(len(body)) || head.Header.Get("ETag") != index.Header.Get("ETag") {
		t.Fatal("HEAD differs from GET metadata")
	}
	detail, detailBody := request(t, server.Client(), "GET", server.URL+"/api/v1/live/basketball-live", "")
	var session Session
	if err := json.Unmarshal(detailBody, &session); err != nil || session.ID != "basketball-live" {
		t.Fatalf("incorrect detail %s %v", detailBody, err)
	}
	if detail.Header.Get("ETag") == index.Header.Get("ETag") {
		t.Fatal("index/detail validators were conflated")
	}
	for _, method := range []string{"GET", "HEAD"} {
		for _, value := range []string{index.Header.Get("ETag"), "W/" + index.Header.Get("ETag"), `"other,opaque", W/` + index.Header.Get("ETag"), "*"} {
			resp, body := request(t, server.Client(), method, server.URL+"/api/v1/live", value)
			if resp.StatusCode != 304 || len(body) != 0 || resp.Header.Get("ETag") != index.Header.Get("ETag") || resp.Header.Get("Cache-Control") != cacheControl {
				t.Fatalf("incorrect conditional %s %q: %+v %s", method, value, resp, body)
			}
		}
	}
	resp, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live/basketball-live", index.Header.Get("ETag"))
	if resp.StatusCode != 200 {
		t.Fatal("used index ETag for session")
	}
	if calls.Load() != 1 {
		t.Fatalf("index/detail cache not shared: %d", calls.Load())
	}
}

func TestHTTPMethodsUnknownPathsAndUntrustedQueries(t *testing.T) {
	data := marshal(t, validIndex())
	var calls atomic.Int32
	h, _ := testHandler(t, func(context.Context) ([]byte, error) { calls.Add(1); return data, nil })
	server := httptest.NewServer(h)
	defer server.Close()
	for _, path := range []string{"/api/v1/live/", "/api/v1/live/basketball-live/extra", "/api/v1/live/%62asketball-live", "/api/v1/live/../secret", "/other"} {
		resp, body := request(t, server.Client(), "GET", server.URL+path, "")
		if resp.StatusCode != 404 || resp.Header.Get("Cache-Control") != "no-store" || !strings.Contains(string(body), "not_found") {
			t.Fatalf("incorrect unknown path %q: %d %s", path, resp.StatusCode, body)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid routes triggered provider calls")
	}
	for _, method := range []string{"POST", "PUT", "DELETE", "PATCH", "OPTIONS"} {
		resp, _ := request(t, server.Client(), method, server.URL+"/api/v1/live", "")
		if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET, HEAD" || resp.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("incorrect method response %s: %+v", method, resp)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("write methods triggered provider calls")
	}
	resp, body := request(t, server.Client(), "GET", server.URL+"/api/v1/live/missing", "")
	if resp.StatusCode != 404 || resp.Header.Get("ETag") != "" || !strings.Contains(string(body), "not_found") {
		t.Fatal("unknown ID did not return JSON404")
	}
	resp, body = request(t, server.Client(), "HEAD", server.URL+"/api/v1/live/missing", "")
	if resp.StatusCode != 404 || len(body) != 0 {
		t.Fatal("unknown HEAD returned a body")
	}
	resp, _ = request(t, server.Client(), "GET", server.URL+"/api/v1/live?url=http://169.254.169.254/", "")
	if resp.StatusCode != 200 || calls.Load() != 1 {
		t.Fatal("query parameters changed fixed provider/cache behavior")
	}
}

func TestHTTPRefreshRevalidationAndPerSessionETag(t *testing.T) {
	index := validIndex()
	second := index.Sessions[0]
	second.ID = "second"
	second.Title = "second"
	second.Segments = []Segment{}
	index.Sessions = append(index.Sessions, second)
	var data atomic.Value
	data.Store(marshal(t, index))
	var calls atomic.Int32
	h, clock := testHandler(t, func(context.Context) ([]byte, error) { calls.Add(1); return data.Load().([]byte), nil })
	server := httptest.NewServer(h)
	defer server.Close()
	before, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live", "")
	detail, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live/basketball-live", "")
	index.Sessions[1].Title = "changed second session"
	data.Store(marshal(t, index))
	clock.advance(time.Second - time.Nanosecond)
	resp, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live", before.Header.Get("ETag"))
	if resp.StatusCode != 304 || calls.Load() != 1 {
		t.Fatal("refreshed before one-second expiry")
	}
	clock.advance(time.Nanosecond)
	resp, _ = request(t, server.Client(), "GET", server.URL+"/api/v1/live", before.Header.Get("ETag"))
	if resp.StatusCode != 200 || resp.Header.Get("ETag") == before.Header.Get("ETag") {
		t.Fatal("did not revalidate changed snapshot")
	}
	updatedETag := resp.Header.Get("ETag")
	resp, _ = request(t, server.Client(), "GET", server.URL+"/api/v1/live/basketball-live", detail.Header.Get("ETag"))
	if resp.StatusCode != 304 {
		t.Fatal("unrelated session update changed detail ETag")
	}
	pretty, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data.Store(pretty)
	clock.advance(time.Second)
	again, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live", updatedETag)
	if again.StatusCode != 304 || again.Header.Get("ETag") != updatedETag {
		t.Fatal("whitespace changed the representation validator")
	}
	if calls.Load() != 3 {
		t.Fatalf("unexpected refresh calls: %d", calls.Load())
	}
}

func TestHTTPProviderFailureAndCorruptionNeverReturnStale304(t *testing.T) {
	data := marshal(t, validIndex())
	var mode atomic.Int32
	var calls atomic.Int32
	var logs bytes.Buffer
	h, clock := testHandler(t, func(context.Context) ([]byte, error) {
		calls.Add(1)
		switch mode.Load() {
		case 1:
			return nil, errors.New("provider unavailable")
		case 2:
			return []byte(`{"version":1,"sessions":null}`), nil
		default:
			return data, nil
		}
	})
	h.config.Logger = slog.New(slog.NewJSONHandler(&logs, nil))
	server := httptest.NewServer(h)
	defer server.Close()
	good, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live", "")
	for _, value := range []int32{1, 2} {
		mode.Store(value)
		clock.advance(time.Second)
		for _, method := range []string{"GET", "HEAD"} {
			resp, body := request(t, server.Client(), method, server.URL+"/api/v1/live", good.Header.Get("ETag"))
			if resp.StatusCode != 503 || resp.Header.Get("Cache-Control") != "no-store" || resp.Header.Get("ETag") != "" || resp.Header.Get("Retry-After") != "1" || (method == "HEAD" && len(body) != 0) {
				t.Fatalf("bad failure response: %+v %s", resp, body)
			}
		}
	}
	if calls.Load() != 3 || strings.Count(logs.String(), "live index refresh failed") != 2 {
		t.Fatalf("failure reads/logs not coalesced: %d %s", calls.Load(), logs.String())
	}
	mode.Store(0)
	clock.advance(time.Second)
	recovered, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live", good.Header.Get("ETag"))
	if recovered.StatusCode != 304 {
		t.Fatal("provider recovery did not restore valid representation")
	}
}

func TestConcurrentHTTPRefreshIsSingleFlightForSuccessAndFailure(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			data := marshal(t, validIndex())
			var calls atomic.Int32
			started, release := make(chan struct{}), make(chan struct{})
			h, clock := testHandler(t, func(context.Context) ([]byte, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release
				}
				if failed {
					return nil, errors.New("read failed")
				}
				return data, nil
			})
			server := httptest.NewServer(h)
			defer server.Close()
			batch := func(blocked bool) {
				var wg sync.WaitGroup
				responses := make(chan int, 64)
				for range 64 {
					wg.Add(1)
					go func() {
						defer wg.Done()
						resp, err := server.Client().Get(server.URL + "/api/v1/live")
						if err != nil {
							responses <- 0
							return
						}
						io.Copy(io.Discard, resp.Body)
						resp.Body.Close()
						responses <- resp.StatusCode
					}()
				}
				if blocked {
					<-started
					close(release)
				}
				wg.Wait()
				close(responses)
				expected := 200
				if failed {
					expected = 503
				}
				for code := range responses {
					if code != expected {
						t.Fatalf("concurrent status %d, expected %d", code, expected)
					}
				}
			}
			batch(true)
			if calls.Load() != 1 {
				t.Fatalf("concurrent duplicate fetches %d", calls.Load())
			}
			clock.advance(time.Second)
			batch(false)
			if calls.Load() != 2 {
				t.Fatalf("refresh stampede %d", calls.Load())
			}
		})
	}
}

func TestOneCanceledWaiterDoesNotCancelOtherRefreshWaiters(t *testing.T) {
	data := marshal(t, validIndex())
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	h, _ := testHandler(t, func(ctx context.Context) ([]byte, error) {
		calls.Add(1)
		close(started)
		<-release
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return data, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	first, second := make(chan result, 1), make(chan result, 1)
	go func() { first <- h.load(ctx) }()
	<-started
	go func() { second <- h.load(context.Background()) }()
	cancel()
	if value := <-first; !errors.Is(value.err, context.Canceled) {
		t.Fatal("canceled waiter stayed blocked")
	}
	close(release)
	if value := <-second; value.err != nil || value.snapshot == nil {
		t.Fatalf("initiator canceled shared refresh: %v", value.err)
	}
	if calls.Load() != 1 {
		t.Fatal("cancellation caused duplicate provider reads")
	}
}

func TestProviderTimeoutKeepsOneUnderlyingReadUntilItReturns(t *testing.T) {
	data := marshal(t, validIndex())
	release := make(chan struct{})
	var calls atomic.Int32
	h, clock := testHandler(t, func(context.Context) ([]byte, error) {
		if calls.Add(1) == 1 {
			<-release
		}
		return data, nil
	})
	h.timeout = 20 * time.Millisecond
	server := httptest.NewServer(h)
	defer server.Close()
	resp, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live", "")
	if resp.StatusCode != 503 {
		t.Fatal("ignored provider timeout")
	}
	clock.advance(10 * time.Second)
	for range 32 {
		resp, _ := request(t, server.Client(), "GET", server.URL+"/api/v1/live", "")
		if resp.StatusCode != 503 {
			t.Fatal("served expired/late data")
		}
	}
	if calls.Load() != 1 {
		t.Fatal("leaked new readers while timed-out provider was still running")
	}
	close(release)
	deadline := time.NewTimer(time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		h.mu.Lock()
		pending := h.inflight != nil
		h.mu.Unlock()
		if !pending {
			break
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("flight not released after provider returned")
		}
	}
	resp, _ = request(t, server.Client(), "GET", server.URL+"/api/v1/live", "")
	if resp.StatusCode != 200 || calls.Load() != 2 {
		t.Fatal("did not recover after timed-out reader returned")
	}
}

func TestProviderPanicAndConstructorBoundary(t *testing.T) {
	if _, err := NewHandler(Config{}); err == nil {
		t.Fatal("silently accepted absent provider")
	}
	if _, err := NewHandler(Config{Read: func(context.Context) ([]byte, error) { return nil, nil }, AssetOrigin: "http://media.example.com"}); err == nil {
		t.Fatal("accepted insecure origin")
	}
	h, _ := testHandler(t, func(context.Context) ([]byte, error) { panic("private panic detail") })
	server := httptest.NewServer(h)
	defer server.Close()
	resp, body := request(t, server.Client(), "GET", server.URL+"/api/v1/live", "")
	if resp.StatusCode != 503 || strings.Contains(string(body), "private") {
		t.Fatal("provider panic escaped error boundary")
	}
}

func TestEntityTagParser(t *testing.T) {
	current := `"current"`
	for _, value := range []string{current, "W/" + current, `"opaque,comma", W/"current"`, "*", " \tW/\"current\" \t"} {
		if !matchesETag(value, current) {
			t.Errorf("missed valid tag %q", value)
		}
	}
	for _, value := range []string{`"other"`, `"opaque,current"`, `"current",`, `"current" junk`, `W/current`, `"unterminated`, `*, "current"`, `"current", "unterminated`, `"opaque, "current"`} {
		if matchesETag(value, current) {
			t.Errorf("accepted malformed/mismatched tag %q", value)
		}
	}
}
