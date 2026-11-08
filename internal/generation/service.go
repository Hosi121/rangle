package generation

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

type Service struct {
	Config   Config
	Store    Store
	Provider Provider
	Logger   *slog.Logger
	Now      func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) Create(ctx context.Context, owner, presetID, nonce string) (Job, error) {
	if owner == "" || !requestID.MatchString(nonce) {
		return Job{}, ErrInvalid
	}
	id := requestJobID(owner, nonce)
	if existing, err := s.Store.Get(ctx, id); err == nil {
		if existing.Preset != presetID {
			return Job{}, ErrConflict
		}
		return s.refresh(ctx, existing)
	} else if !errors.Is(err, ErrNotFound) {
		return Job{}, err
	}
	preset, ok := s.Config.preset(presetID)
	if !ok {
		return Job{}, ErrInvalid
	}
	if !s.now().Add(MaxRuntime + time.Minute).Before(s.Config.Deadline) {
		return Job{}, ErrExpired
	}
	if active, err := s.Store.Active(ctx); err != nil {
		return Job{}, err
	} else if active != nil {
		if _, err := s.refresh(ctx, *active); err != nil {
			return Job{}, err
		}
	}
	job, created, err := s.Store.Admit(ctx, owner, preset, nonce, s.Config.Attempts, s.now())
	if err != nil {
		return Job{}, err
	}
	if !created {
		return s.refresh(ctx, job)
	}
	if s.Logger != nil {
		s.Logger.InfoContext(ctx, "generation job admitted", "job_id", job.ID, "preset_id", preset.ID, "deadline", job.CreatedAt.Add(MaxRuntime))
	}
	name, err := s.Provider.Submit(ctx, job, preset)
	if err != nil {
		if s.Logger != nil {
			s.Logger.ErrorContext(ctx, "generation submission failed", "job_id", job.ID, "error", err)
		}
		var rejected *Rejected
		if errors.As(err, &rejected) {
			return s.Store.Transition(ctx, job.ID, "", Failed, s.now())
		}
		// The server might have accepted a timed-out create. Never submit again:
		// subsequent reads reconcile via the stable application job label.
		return job, nil
	}
	if s.Logger != nil {
		s.Logger.InfoContext(ctx, "generation job submitted", "job_id", job.ID, "provider_name", name)
	}
	return s.Store.Transition(ctx, job.ID, name, Queued, s.now())
}

func (s *Service) Get(ctx context.Context, owner, id string) (Job, error) {
	job, err := s.Store.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if job.Owner != owner {
		return Job{}, ErrNotFound
	}
	return s.refresh(ctx, job)
}

func (s *Service) Cancel(ctx context.Context, owner, id string) (Job, error) {
	job, err := s.Get(ctx, owner, id)
	if err != nil || job.State.Terminal() || job.State == Cancelling {
		return job, err
	}
	if job.ProviderName == "" {
		return job, ErrBusy
	}
	if err := s.Provider.Cancel(ctx, job.ProviderName); err != nil {
		return Job{}, err
	}
	return s.Store.Transition(ctx, job.ID, job.ProviderName, Cancelling, s.now())
}

func (s *Service) refresh(ctx context.Context, job Job) (Job, error) {
	if job.State.Terminal() {
		return job, nil
	}
	if job.ProviderName == "" {
		name, err := s.Provider.Find(ctx, job.ID)
		if err != nil {
			return Job{}, err
		}
		if name == "" {
			return job, nil
		}
		job.ProviderName = name
	}
	state, err := s.Provider.Status(ctx, job.ProviderName)
	if err != nil {
		return Job{}, err
	}
	if !state.Terminal() && state != Cancelling && job.State != Cancelling && !s.now().Before(job.CreatedAt.Add(MaxRuntime)) {
		if err := s.Provider.Cancel(ctx, job.ProviderName); err != nil {
			return Job{}, err
		}
		state = Cancelling
	}
	updated, err := s.Store.Transition(ctx, job.ID, job.ProviderName, state, s.now())
	if err == nil && updated.State != job.State && s.Logger != nil {
		s.Logger.InfoContext(ctx, "generation job state changed", "job_id", job.ID, "state", updated.State)
	}
	return updated, err
}
