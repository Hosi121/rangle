package generation

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

type Service struct {
	ProgressReader ProgressReader
	Cleanup        AssetCleanup
	MediaClient    *http.Client
	ReservedClipID func(string) bool
	Config         Config
	Store          Store
	Provider       Provider
	Logger         *slog.Logger
	Now            func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Service) Create(ctx context.Context, owner, presetID, nonce string) (Job, error) {
	return s.CreateFor(ctx, OperatorPrincipal(owner), presetID, nonce)
}

func (s *Service) CreateFor(ctx context.Context, principal Principal, presetID, nonce string) (Job, error) {
	if !principal.allows(publisher.CreateGeneration) || !requestID.MatchString(nonce) {
		return Job{}, ErrInvalid
	}
	owner := principal.ownerKey()
	if err := s.principalAccess(ctx, principal); err != nil {
		return Job{}, err
	}
	id := requestJobID(owner, nonce)
	if existing, err := s.Store.Get(ctx, id); err == nil {
		if !principal.owns(existing) || existing.Preset != presetID {
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
	allowed, err := s.PresetsFor(ctx, principal)
	if err != nil {
		return Job{}, err
	}
	permitted := false
	for _, candidate := range allowed {
		if candidate.ID == presetID {
			permitted = true
			break
		}
	}
	if !permitted {
		return Job{}, ErrInvalid
	}
	if principal.Kind == "staff" {
		preset.PublisherAccounts = []publisher.ID{principal.Account.ID}
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
	preset = principal.snapshot(preset)
	var job Job
	var created bool
	if scoped, ok := s.Store.(ScopedAdmission); ok {
		job, created, err = scoped.AdmitScoped(ctx, principal, preset, nonce, s.Config.Attempts, s.now())
	} else if principal.Kind == "operator" {
		job, created, err = s.Store.Admit(ctx, owner, preset, nonce, s.Config.Attempts, s.now())
	} else {
		return Job{}, fmt.Errorf("scoped admission unavailable")
	}
	if err != nil {
		return Job{}, err
	}
	if !created {
		return s.refresh(ctx, job)
	}
	if s.Logger != nil {
		s.Logger.InfoContext(ctx, "generation job admitted", "job_id", job.ID, "preset_id", preset.ID, "publisher_account_id", job.PublisherAccountID, "actor_kind", principal.Kind, "actor_id", principal.ID, "deadline", job.CreatedAt.Add(MaxRuntime))
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
	return s.GetFor(ctx, OperatorPrincipal(owner), id)
}
func (s *Service) GetFor(ctx context.Context, p Principal, id string) (Job, error) {
	job, err := s.getFor(ctx, p, id)
	if err != nil {
		return Job{}, err
	}
	return s.withProgress(ctx, job), nil
}
func (s *Service) getFor(ctx context.Context, p Principal, id string) (Job, error) {
	if !p.allows(publisher.ReadGeneration) {
		return Job{}, ErrPublisherForbidden
	}
	if err := s.principalAccess(ctx, p); err != nil {
		return Job{}, err
	}
	job, err := s.Store.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if !p.owns(job) {
		return Job{}, ErrNotFound
	}
	return s.refresh(ctx, job)
}
func (s *Service) ListFor(ctx context.Context, p Principal) ([]Job, error) {
	if !p.allows(publisher.ReadGeneration) {
		return nil, ErrPublisherForbidden
	}
	if err := s.principalAccess(ctx, p); err != nil {
		return nil, err
	}
	rows, err := s.Store.List(ctx, p.ownerKey())
	if err != nil {
		return nil, err
	}
	for _, job := range rows {
		if !p.owns(job) {
			return nil, fmt.Errorf("invalid generation ownership index")
		}
	}
	return rows, nil
}
func (s *Service) Cancel(ctx context.Context, owner, id string) (Job, error) {
	return s.CancelFor(ctx, OperatorPrincipal(owner), id)
}
func (s *Service) CancelFor(ctx context.Context, p Principal, id string) (Job, error) {
	if !p.allows(publisher.CancelGeneration) {
		return Job{}, ErrPublisherForbidden
	}
	job, err := s.getFor(ctx, p, id)
	if err != nil || job.State.Terminal() || job.State == Cancelling {
		return job, err
	}
	if job.ProviderName == "" {
		return job, ErrBusy
	}
	if err := s.Provider.Cancel(ctx, job.ProviderName); err != nil {
		return Job{}, err
	}
	if s.Logger != nil {
		s.Logger.InfoContext(ctx, "generation cancellation requested", "job_id", id, "publisher_account_id", job.PublisherAccountID, "actor_kind", p.Kind, "actor_id", p.ID)
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
