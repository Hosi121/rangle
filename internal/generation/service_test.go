package generation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type memoryStore struct {
	mu   sync.Mutex
	jobs map[string]Job
	a    admission
}

func newMemory() *memoryStore { return &memoryStore{jobs: map[string]Job{}} }
func (m *memoryStore) Admit(_ context.Context, owner string, preset Preset, nonce string, maximum int, now time.Time) (Job, bool, error) {
	return m.AdmitScoped(context.Background(), OperatorPrincipal(owner), preset, nonce, maximum, now)
}
func (m *memoryStore) AdmitScoped(_ context.Context, p Principal, preset Preset, nonce string, maximum int, now time.Time) (Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !p.valid() || !p.permitsPreset(preset) {
		return Job{}, false, ErrInvalid
	}
	owner := p.ownerKey()
	id := requestJobID(owner, nonce)
	if old, ok := m.jobs[id]; ok {
		if !p.owns(old) || old.Preset != preset.ID {
			return Job{}, false, ErrConflict
		}
		return old, false, nil
	}
	if m.a.Used >= maximum {
		return Job{}, false, ErrBudget
	}
	if m.a.Active != "" {
		return Job{}, false, ErrBusy
	}
	job := Job{ID: id, Owner: owner, PublisherAccountID: p.accountID(), CreatedBy: p.actor(), Preset: preset.ID, Snapshot: preset, State: Submitting, CreatedAt: now, UpdatedAt: now}
	m.jobs[id] = job
	m.a.Used++
	m.a.Active = id
	return job, true, nil
}
func (m *memoryStore) Get(_ context.Context, id string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	return job, nil
}
func (m *memoryStore) List(_ context.Context, owner string) ([]Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := []Job{}
	for _, job := range m.jobs {
		if job.Owner == owner {
			jobs = append(jobs, job)
		}
	}
	return jobs, nil
}
func (m *memoryStore) Active(ctx context.Context) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.a.Active == "" {
		return nil, nil
	}
	job := m.jobs[m.a.Active]
	return &job, nil
}
func (m *memoryStore) Transition(_ context.Context, id, name string, state State, now time.Time) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	old, ok := m.jobs[id]
	if !ok {
		return Job{}, ErrNotFound
	}
	job, err := nextJob(old, name, state, now)
	if err != nil {
		return Job{}, err
	}
	m.jobs[id] = job
	if job.State.Terminal() && m.a.Active == id {
		m.a.Active = ""
	}
	return job, nil
}

type fakeProvider struct {
	submits     atomic.Int32
	cancels     atomic.Int32
	submitError error
	found       string
	state       State
}

func (p *fakeProvider) Submit(context.Context, Job, Preset) (string, error) {
	p.submits.Add(1)
	if p.submitError != nil {
		return "", p.submitError
	}
	return "projects/example/locations/asia-northeast1/customJobs/1", nil
}
func (p *fakeProvider) Find(context.Context, string) (string, error)  { return p.found, nil }
func (p *fakeProvider) Status(context.Context, string) (State, error) { return p.state, nil }
func (p *fakeProvider) Cancel(context.Context, string) error          { p.cancels.Add(1); return nil }

func testConfig() Config {
	return Config{
		Project: "comin-up", ProjectNumber: "113704194486", Database: "rangle-stg", Image: "asia-northeast1-docker.pkg.dev/comin-up/rangle-stg/trainer@sha256:" + strings.Repeat("a", 64),
		ServiceAccount: "rangle-stg-trainer@comin-up.iam.gserviceaccount.com", OutputBucket: "rangle-stg-example-media", MediaOrigin: "https://media.rangle.test",
		Deadline: time.Date(2026, 11, 14, 15, 0, 0, 0, time.UTC), Attempts: 2,
		Presets: []Preset{{ID: "vru-frame0", Title: "VRU frame 0", Sport: "basketball", Description: "静止した時刻の再構成", InputURI: "gs://rangle-stg-example-inputs/datasets/vru.npz", InputSHA256: strings.Repeat("b", 64), PreviewURL: "https://media.rangle.test/preview.webm", Source: "https://huggingface.co/datasets/BestWJH/VRU_Basketball", License: "CC BY 4.0"}},
	}
}
func testService() (*Service, *fakeProvider) {
	p := &fakeProvider{state: Running}
	s := &Service{Config: testConfig(), Store: newMemory(), Provider: p, Now: func() time.Time { return time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC) }}
	return s, p
}
func nonce(n int) string { return fmt.Sprintf("%032x", n) }

func TestConcurrentRetrySubmitsOnlyOnce(t *testing.T) {
	s, p := testService()
	var wg sync.WaitGroup
	for range 40 {
		wg.Go(func() {
			job, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(1))
			if err != nil {
				t.Error(err)
			}
			if job.ID != requestJobID("viewer", nonce(1)) {
				t.Error("changed request identity")
			}
		})
	}
	wg.Wait()
	if got := p.submits.Load(); got != 1 {
		t.Fatalf("submitted %d jobs for one request", got)
	}
}

func TestAmbiguousCreateReconcilesWithoutResubmission(t *testing.T) {
	s, p := testService()
	p.submitError = context.DeadlineExceeded
	job, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(1))
	if err != nil || job.State != Submitting {
		t.Fatalf("%+v %v", job, err)
	}
	if _, err = s.Create(context.Background(), "other", "vru-frame0", nonce(2)); !errors.Is(err, ErrBusy) {
		t.Fatalf("unknown submission released slot: %v", err)
	}
	p.found = "projects/example/locations/asia-northeast1/customJobs/1"
	p.state = Succeeded
	job, err = s.Get(context.Background(), "viewer", job.ID)
	if err != nil || job.State != Succeeded {
		t.Fatalf("%+v %v", job, err)
	}
	_, err = s.Create(context.Background(), "viewer", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	if p.submits.Load() != 1 {
		t.Fatal("retried a potentially successful create")
	}
}

func TestBudgetIsNotRefundedByFailureAndOwnerIsEnforced(t *testing.T) {
	s, p := testService()
	p.submitError = &Rejected{Err: errors.New("quota exhausted")}
	for i := 1; i <= 2; i++ {
		job, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(i))
		if err != nil || job.State != Failed {
			t.Fatalf("%+v %v", job, err)
		}
		if _, err = s.Get(context.Background(), "other", job.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("other owner saw job: %v", err)
		}
	}
	if _, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(3)); !errors.Is(err, ErrBudget) {
		t.Fatalf("budget bypass: %v", err)
	}
	if _, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(1)); err != nil {
		t.Fatalf("retry lost original job: %v", err)
	}
	if p.submits.Load() != 2 {
		t.Fatal("budget failure created a resource")
	}
}

func TestTimeoutCancelsAndLateStateCannotReleaseActiveSlot(t *testing.T) {
	s, p := testService()
	job, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(1))
	if err != nil {
		t.Fatal(err)
	}
	s.Now = func() time.Time { return job.CreatedAt.Add(MaxRuntime) }
	job, err = s.Get(context.Background(), "viewer", job.ID)
	if err != nil || job.State != Cancelling || p.cancels.Load() != 1 {
		t.Fatalf("%+v %v cancels=%d", job, err, p.cancels.Load())
	}
	if _, err = s.Create(context.Background(), "other", "vru-frame0", nonce(2)); !errors.Is(err, ErrBusy) {
		t.Fatalf("admitted during cancellation: %v", err)
	}
	p.state = Cancelled
	if _, err = s.Get(context.Background(), "viewer", job.ID); err != nil {
		t.Fatal(err)
	}
	second, err := s.Create(context.Background(), "other", "vru-frame0", nonce(2))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.Transition(context.Background(), job.ID, job.ProviderName, Running, s.now()); err != nil {
		t.Fatal(err)
	}
	active, err := s.Store.Active(context.Background())
	if err != nil || active == nil || active.ID != second.ID {
		t.Fatalf("stale completion changed active slot: %+v %v", active, err)
	}
}

func TestExpiredWindowDoesNotSubmit(t *testing.T) {
	s, p := testService()
	s.Now = func() time.Time { return s.Config.Deadline.Add(-MaxRuntime) }
	if _, err := s.Create(context.Background(), "viewer", "vru-frame0", nonce(1)); !errors.Is(err, ErrExpired) {
		t.Fatalf("%v", err)
	}
	if p.submits.Load() != 0 {
		t.Fatal("submitted after authorized window")
	}
}
