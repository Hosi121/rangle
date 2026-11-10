package generation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
)

func progressJob(now time.Time) Job {
	return Job{ID: nonce(1), Owner: "operator", ProviderName: "projects/comin-up/locations/asia-northeast1/customJobs/123", CreatedAt: now.Add(-5 * time.Minute), State: Running}
}
func progressEntry(now time.Time, payload string) *logging.LogEntry {
	return &logging.LogEntry{LogName: "projects/comin-up/logs/workerpool0-0", Timestamp: now.Format(time.RFC3339Nano), Resource: &logging.MonitoredResource{Type: "ml_job", Labels: map[string]string{"job_id": "123", "project_id": "113704194486", "task_name": "workerpool0-0"}}, JsonPayload: []byte(payload)}
}
func TestProgressUsesMeasuredStepsAndStopsExpiredETA(t *testing.T) {
	now := time.Now().UTC()
	r := &LogProgressReader{config: testConfig()}
	job := progressJob(now)
	e := progressEntry(now, `{"service":"rangle-trainer","event":"training_progress","iteration":499,"iterations":1000,"elapsed_seconds":100}`)
	sample := r.parse(e, job, "123", now)
	p := sampledProgress(sample, now)
	if p == nil || p.Stage != "training" || p.CompletedSteps != 500 || p.TotalSteps != 1000 || p.ETASeconds == nil || *p.ETASeconds != 100 || p.Stale {
		t.Fatal(p)
	}
	p = sampledProgress(sample, now.Add(20*time.Second))
	if p.ETASeconds == nil || *p.ETASeconds != 80 {
		t.Fatal("cached observation reset ETA", p)
	}
	p = sampledProgress(sample, now.Add(61*time.Second))
	if !p.Stale || p.ETASeconds != nil {
		t.Fatal("stale ETA shown", p)
	}
	last := r.parse(progressEntry(now, `{"service":"rangle-trainer","event":"training_progress","iteration":999,"iterations":1000,"elapsed_seconds":100}`), job, "123", now)
	if p := sampledProgress(last, now); p == nil || p.CompletedSteps != 1000 || p.ETASeconds != nil {
		t.Fatal("finished steps are not a completed job", p)
	}
	for _, event := range []string{"training_started", "training_completed"} {
		p := sampledProgress(r.parse(progressEntry(now, fmt.Sprintf(`{"service":"rangle-trainer","event":%q}`, event)), job, "123", now), now)
		if p == nil || p.ETASeconds != nil || p.TotalSteps != 0 || p.Stage == "training" {
			t.Fatal(p)
		}
	}
}
func TestProgressRejectsMalformedForeignOrFutureObservations(t *testing.T) {
	now := time.Now().UTC()
	r := &LogProgressReader{config: testConfig()}
	job := progressJob(now)
	for _, payload := range []string{
		`{"service":"other","event":"training_progress","iteration":2,"iterations":3,"elapsed_seconds":3}`,
		`{"service":"rangle-trainer","event":"training_progress","iteration":-1,"iterations":3,"elapsed_seconds":3}`,
		`{"service":"rangle-trainer","event":"training_progress","iteration":3,"iterations":3,"elapsed_seconds":3}`,
		`{"service":"rangle-trainer","event":"training_progress","iteration":1.5,"iterations":3,"elapsed_seconds":3}`,
		`{"service":"rangle-trainer","event":"training_progress","iteration":2,"iterations":1000001,"elapsed_seconds":3}`,
		`{"service":"rangle-trainer","event":"training_progress","iteration":2,"iterations":3,"elapsed_seconds":-3}`,
		`{"service":"rangle-trainer","event":"training_progress","iteration":2,"iterations":3}`,
	} {
		if r.parse(progressEntry(now, payload), job, "123", now) != nil {
			t.Fatal("accepted", payload)
		}
	}
	payload := `{"service":"rangle-trainer","event":"training_progress","iteration":499,"iterations":1000,"elapsed_seconds":100}`
	for _, mutate := range []func(*logging.LogEntry){
		func(e *logging.LogEntry) { e.Resource.Labels["job_id"] = "456" },
		func(e *logging.LogEntry) { e.Resource.Labels["project_id"] = "other" },
		func(e *logging.LogEntry) { e.Resource.Labels["task_name"] = "workerpool1-0" },
		func(e *logging.LogEntry) { e.LogName = "projects/other/logs/workerpool0-0" },
		func(e *logging.LogEntry) { e.Timestamp = now.Add(2 * time.Minute).Format(time.RFC3339Nano) },
	} {
		e := progressEntry(now, payload)
		mutate(e)
		if r.parse(e, job, "123", now) != nil {
			t.Fatal("accepted foreign/future log")
		}
	}
}
func TestProgressCoalescesPollsAndCachesMissingAndErrors(t *testing.T) {
	now := time.Now().UTC()
	job := progressJob(now)
	for _, status := range []int{200, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var count atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				count.Add(1)
				var q logging.ListLogEntriesRequest
				if json.NewDecoder(r.Body).Decode(&q) != nil || q.PageSize != 2 || q.PageToken != "" || q.OrderBy != "timestamp desc" || len(q.ResourceNames) != 1 || !strings.HasSuffix(q.ResourceNames[0], "/views/generation-progress") || !strings.Contains(q.Filter, `resource.labels.job_id="123"`) {
					t.Error("unbounded or unscoped query", q)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					_, _ = w.Write([]byte(`{"entries":[]}`))
				} else {
					_, _ = w.Write([]byte(`{"error":{"code":403,"message":"denied"}}`))
				}
			}))
			defer srv.Close()
			r, err := NewLogProgressReader(context.Background(), testConfig(), option.WithEndpoint(srv.URL+"/"), option.WithoutAuthentication())
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					p, err := r.ReadProgress(context.Background(), job, now)
					if p != nil || (err != nil) != (status == 403) {
						t.Error(p, err)
					}
				})
			}
			wg.Wait()
			if count.Load() != 1 {
				t.Fatal("poll fanout", count.Load())
			}
			_, _ = r.ReadProgress(context.Background(), job, now.Add(11*time.Second))
			if count.Load() != 2 {
				t.Fatal("cache never refreshed")
			}
			bad := job
			bad.ProviderName = "projects/other/locations/asia-northeast1/customJobs/123"
			if _, err := r.ReadProgress(context.Background(), bad, now); err == nil || count.Load() != 2 {
				t.Fatal("foreign resource queried")
			}
		})
	}
}

type progressStub struct {
	calls int
	err   error
}

func (p *progressStub) ReadProgress(context.Context, Job, time.Time) (*Progress, error) {
	p.calls++
	return &Progress{Stage: "training"}, p.err
}
func TestProgressRunsOnlyAfterOwnershipCheckAndCannotBreakCancelOrResults(t *testing.T) {
	s, provider := testService()
	ctx := context.Background()
	job, err := s.Create(ctx, "operator", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	p := &progressStub{err: errors.New("logs offline")}
	s.ProgressReader = p
	if _, err := s.Get(ctx, "foreign", job.ID); !errors.Is(err, ErrNotFound) || p.calls != 0 {
		t.Fatal("foreign progress read", err)
	}
	if j, err := s.Get(ctx, "operator", job.ID); err != nil || j.State != Running || j.Progress != nil || p.calls != 1 {
		t.Fatal("log outage hid job state", err)
	}
	if _, err := s.Cancel(ctx, "operator", job.ID); err != nil || p.calls != 1 {
		t.Fatal("cancellation queried logs", err)
	}
	provider.state = Succeeded
	if j, err := s.Get(ctx, "operator", job.ID); err != nil || j.State != Succeeded || p.calls != 1 {
		t.Fatal("terminal job queried logs", err)
	}
}
