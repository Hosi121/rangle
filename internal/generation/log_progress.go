package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"strings"
	"sync"
	"time"

	"google.golang.org/api/logging/v2"
	"google.golang.org/api/option"
)

const ProgressViewName = "generation-progress"
const progressFreshness = time.Minute
const progressCacheTTL = 10 * time.Second

var numericJobID = regexp.MustCompile(`^[0-9]{1,32}$`)

type progressSample struct {
	stage            string
	completed, total int64
	elapsed          float64
	at               time.Time
}
type progressCache struct {
	done   chan struct{}
	until  time.Time
	sample *progressSample
	err    error
}

// Concurrent pollers share one bounded worker-view read every ten seconds.
// Missing/error observations are cached too; this never reads project-wide logs.
type LogProgressReader struct {
	Logger *slog.Logger
	api    *logging.Service
	config Config
	mu     sync.Mutex
	cache  map[string]*progressCache
}

func NewLogProgressReader(ctx context.Context, cfg Config, opts ...option.ClientOption) (*LogProgressReader, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	api, err := logging.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &LogProgressReader{api: api, config: cfg, cache: map[string]*progressCache{}}, nil
}
func (r *LogProgressReader) ReadProgress(ctx context.Context, job Job, now time.Time) (*Progress, error) {
	id, err := r.providerID(job)
	if err != nil {
		return nil, err
	}
	key := job.ID + ":" + id
	r.mu.Lock()
	if cached, ok := r.cache[key]; ok {
		if cached.done != nil {
			done := cached.done
			r.mu.Unlock()
			select {
			case <-done:
				return sampledProgress(cached.sample, now), cached.err
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if now.Before(cached.until) {
			r.mu.Unlock()
			return sampledProgress(cached.sample, now), cached.err
		}
		delete(r.cache, key)
	}
	if len(r.cache) >= MaxAttempts {
		oldest := ""
		var expiry time.Time
		for k, c := range r.cache {
			if c.done == nil && (oldest == "" || c.until.Before(expiry)) {
				oldest = k
				expiry = c.until
			}
		}
		if oldest == "" {
			r.mu.Unlock()
			return nil, ErrBusy
		}
		delete(r.cache, oldest)
	}
	cached := &progressCache{done: make(chan struct{}), until: now.Add(progressCacheTTL)}
	r.cache[key] = cached
	r.mu.Unlock()
	sample, err := r.query(ctx, job, id, now)
	if err != nil && r.Logger != nil {
		r.Logger.WarnContext(ctx, "generation progress unavailable", "job_id", job.ID, "error", err)
	}
	r.mu.Lock()
	cached.sample = sample
	cached.err = err
	close(cached.done)
	cached.done = nil
	r.mu.Unlock()
	return sampledProgress(sample, now), err
}
func (r *LogProgressReader) providerID(job Job) (string, error) {
	if !requestID.MatchString(job.ID) || job.CreatedAt.IsZero() {
		return "", ErrInvalid
	}
	for _, project := range []string{r.config.Project, r.config.ProjectNumber} {
		id, ok := strings.CutPrefix(job.ProviderName, "projects/"+project+"/locations/"+Location+"/customJobs/")
		if ok && numericJobID.MatchString(id) {
			return id, nil
		}
	}
	return "", ErrInvalid
}
func (r *LogProgressReader) query(ctx context.Context, job Job, id string, now time.Time) (*progressSample, error) {
	view := "projects/" + r.config.Project + "/locations/" + Location + "/buckets/rangle-stg-logs/views/" + ProgressViewName
	until := now.Add(time.Minute)
	if deadline := job.CreatedAt.Add(MaxRuntime + time.Minute); deadline.Before(until) {
		until = deadline
	}
	filter := fmt.Sprintf(`resource.type="ml_job" AND resource.labels.job_id="%s" AND resource.labels.task_name="workerpool0-0" AND logName="projects/%s/logs/workerpool0-0" AND jsonPayload.service="rangle-trainer" AND jsonPayload.event=("training_started" OR "training_progress" OR "training_completed") AND timestamp>="%s" AND timestamp<="%s"`, id, r.config.Project, job.CreatedAt.Add(-time.Minute).Format(time.RFC3339Nano), until.Format(time.RFC3339Nano))
	response, err := r.api.Entries.List(&logging.ListLogEntriesRequest{ResourceNames: []string{view}, Filter: filter, OrderBy: "timestamp desc", PageSize: 2}).Fields("entries(jsonPayload,timestamp,logName,resource)").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	// No pagination: a snapshot of the newest observation, not a history export.
	for _, entry := range response.Entries {
		if sample := r.parse(entry, job, id, now); sample != nil {
			return sample, nil
		}
	}
	return nil, nil
}
func (r *LogProgressReader) parse(entry *logging.LogEntry, job Job, id string, now time.Time) *progressSample {
	if entry == nil || entry.Resource == nil || entry.Resource.Type != "ml_job" || entry.Resource.Labels["job_id"] != id || (entry.Resource.Labels["project_id"] != r.config.Project && entry.Resource.Labels["project_id"] != r.config.ProjectNumber) || len(entry.JsonPayload) > 8192 {
		return nil
	}
	if entry.LogName != "projects/"+r.config.Project+"/logs/workerpool0-0" || entry.Resource.Labels["task_name"] != "workerpool0-0" {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
	if err != nil || at.Before(job.CreatedAt.Add(-time.Minute)) || at.After(now.Add(time.Minute)) || at.After(job.CreatedAt.Add(MaxRuntime+time.Minute)) {
		return nil
	}
	var event struct {
		Service, Event, Mode string
		Iteration            *int64   `json:"iteration"`
		Iterations           *int64   `json:"iterations"`
		Elapsed              *float64 `json:"elapsed_seconds"`
	}
	if json.Unmarshal(entry.JsonPayload, &event) != nil || event.Service != "rangle-trainer" || event.Mode != "" && event.Mode != job.Snapshot.ModeName() {
		return nil
	}
	sample := &progressSample{at: at}
	switch event.Event {
	case "training_started":
		sample.stage = "preparing"
	case "training_completed":
		sample.stage = "finalizing"
	case "training_progress":
		if event.Iteration == nil || event.Iterations == nil || event.Elapsed == nil || *event.Iteration < 0 || *event.Iterations < 1 || *event.Iterations > 1_000_000 || *event.Iteration >= *event.Iterations || math.IsNaN(*event.Elapsed) || math.IsInf(*event.Elapsed, 0) || *event.Elapsed < 0 || *event.Elapsed > MaxRuntime.Seconds() {
			return nil
		}
		sample.stage = "training"
		sample.completed = *event.Iteration + 1
		sample.total = *event.Iterations
		sample.elapsed = *event.Elapsed
	default:
		return nil
	}
	return sample
}
func sampledProgress(s *progressSample, now time.Time) *Progress {
	if s == nil {
		return nil
	}
	p := &Progress{Stage: s.stage, CompletedSteps: s.completed, TotalSteps: s.total, ObservedAt: s.at, Stale: now.Sub(s.at) > progressFreshness}
	if s.stage == "training" && !p.Stale && s.completed >= 100 && s.elapsed >= 2 {
		remaining := s.elapsed*float64(s.total-s.completed)/float64(s.completed) - math.Max(0, now.Sub(s.at).Seconds())
		if remaining > 0 && remaining <= MaxRuntime.Seconds() {
			seconds := int64(math.Ceil(remaining))
			p.ETASeconds = &seconds
		}
	}
	return p
}
