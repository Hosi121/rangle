package generation

import (
	"context"
	"time"
)

// Progress measures training work, not provisioning, evaluation or upload.
// ETA estimates training time remaining; unavailable observations stay unknown.
type Progress struct {
	Stage          string    `json:"stage"`
	CompletedSteps int64     `json:"completed_steps,omitempty"`
	TotalSteps     int64     `json:"total_steps,omitempty"`
	ObservedAt     time.Time `json:"observed_at"`
	ETASeconds     *int64    `json:"eta_seconds,omitempty"`
	Stale          bool      `json:"stale"`
}
type ProgressReader interface {
	ReadProgress(context.Context, Job, time.Time) (*Progress, error)
}

func (s *Service) withProgress(ctx context.Context, job Job) Job {
	job.Progress = nil
	if s.ProgressReader == nil || job.State != Running || job.Snapshot.ModeName() == "live" || job.ProviderName == "" {
		return job
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	progress, err := s.ProgressReader.ReadProgress(ctx, job, s.now())
	if err == nil && progress != nil {
		value := *progress
		if value.ETASeconds != nil && s.now().Add(time.Duration(*value.ETASeconds)*time.Second).After(job.CreatedAt.Add(MaxRuntime)) {
			value.ETASeconds = nil
		}
		job.Progress = &value
	}
	// Metrics availability never hides job state, cancellation or its result.
	return job
}
