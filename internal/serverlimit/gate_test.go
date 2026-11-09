package serverlimit

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateBoundsWorkAndRejectsWithoutReadingOrWaiting(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, MaxConcurrentAPIRequests)
	release := make(chan struct{})
	var once sync.Once
	finish := func() { once.Do(func() { close(release) }) }
	defer finish()
	var calls atomic.Int32
	var logs bytes.Buffer
	handler := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("ETag", `"unchanged"`)
		_, _ = io.WriteString(w, "unchanged bytes\n")
	}), slog.New(slog.NewJSONHandler(&logs, nil)))
	completed := make(chan *httptest.ResponseRecorder, MaxConcurrentAPIRequests)
	for range MaxConcurrentAPIRequests {
		go func() {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
			completed <- response
		}()
	}
	for range MaxConcurrentAPIRequests {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("admitted requests did not enter")
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		request := httptest.NewRequestWithContext(ctx, method, "/", nil)
		request.Body = unreadBody{}
		response := httptest.NewRecorder()
		response.Header().Set("ETag", `"must-not-cache-error"`)
		handler.ServeHTTP(response, request)
		if response.Code != 503 || response.Header().Get("Retry-After") != "1" || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("ETag") != "" {
			t.Fatalf("overload response: %d %v", response.Code, response.Header())
		}
		if response.Header().Get("Content-Type") != "application/json; charset=utf-8" || response.Header().Get("Content-Length") != strconv.Itoa(len("{\"error\":\"api_overloaded\"}\n")) {
			t.Fatal("overload JSON representation changed", response.Header())
		}
		want := "{\"error\":\"api_overloaded\"}\n"
		if method == http.MethodHead {
			want = ""
		}
		if response.Body.String() != want {
			t.Fatalf("%s overload body = %q", method, response.Body.String())
		}
	}
	if calls.Load() != MaxConcurrentAPIRequests || strings.Count(logs.String(), "API concurrency limit reached") != 1 {
		t.Fatal("overload entered a handler or produced unbounded warnings", calls.Load(), logs.String())
	}
	finish()
	for range MaxConcurrentAPIRequests {
		select {
		case response := <-completed:
			if response.Code != 200 || response.Body.String() != "unchanged bytes\n" || response.Header().Get("ETag") != `"unchanged"` {
				t.Fatal("admitted HTTP bytes or headers changed")
			}
		case <-ctx.Done():
			t.Fatal("admitted requests did not finish")
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != 200 {
		t.Fatal("completed work did not return capacity", response.Code)
	}
}

type unreadBody struct{}

func (unreadBody) Read([]byte) (int, error) { panic("rejected request body was read") }
func (unreadBody) Close() error             { return nil }

func TestCancellationDoesNotReturnCapacityBeforeHandlerExits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{}, MaxConcurrentAPIRequests)
	observedCancel := make(chan struct{}, MaxConcurrentAPIRequests)
	release := make(chan struct{})
	var work sync.WaitGroup
	defer func() { close(release); cancel(); work.Wait() }()
	handler := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/extra" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		entered <- struct{}{}
		<-r.Context().Done()
		observedCancel <- struct{}{}
		<-release // Deliberately continue working after cancellation.
	}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for range MaxConcurrentAPIRequests {
		work.Go(func() {
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(ctx, http.MethodGet, "/", nil))
		})
	}
	for range MaxConcurrentAPIRequests {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("requests did not enter")
		}
	}
	cancel()
	for range MaxConcurrentAPIRequests {
		select {
		case <-observedCancel:
		case <-time.After(5 * time.Second):
			t.Fatal("handlers did not observe cancellation")
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/extra", nil))
	if response.Code != 503 {
		t.Fatal("cancellation admitted more work while canceled handlers were still running", response.Code)
	}
}

func TestGateReturnsCapacityOnPanicAndIgnoresAlreadyCanceledRequests(t *testing.T) {
	handler := New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panic" {
			panic("handler panic")
		}
		w.WriteHeader(204)
	}), slog.New(slog.NewTextHandler(io.Discard, nil)))
	for range MaxConcurrentAPIRequests {
		func() {
			defer func() {
				if recovered := recover(); recovered != "handler panic" {
					t.Errorf("unexpected panic: %v", recovered)
				}
			}()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
		}()
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for range MaxConcurrentAPIRequests * 2 {
		// An already-canceled request must not reach the panicking handler.
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(canceled, http.MethodGet, "/panic", nil))
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ok", nil))
	if response.Code != 204 {
		t.Fatal("panic or canceled request leaked a slot", response.Code)
	}
}
