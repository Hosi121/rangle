// Package generation coordinates finite GPU training jobs separately from HTTP
// delivery. Dataset inputs and GPU settings are server-controlled presets.
package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

var (
	ErrNotFound           = errors.New("generation job not found")
	ErrBusy               = errors.New("a generation job is already active")
	ErrBudget             = errors.New("generation attempt budget exhausted")
	ErrConflict           = errors.New("generation request conflicts with existing job")
	ErrExpired            = errors.New("generation authorization window ended")
	ErrInvalid            = errors.New("invalid generation request")
	ErrForbidden          = errors.New("generation requires an administrator")
	ErrPublisherForbidden = errors.New("publisher generation access forbidden")
)

type State string

const (
	Submitting State = "submitting"
	Queued     State = "queued"
	Running    State = "running"
	Cancelling State = "cancelling"
	Succeeded  State = "succeeded"
	Failed     State = "failed"
	Cancelled  State = "cancelled"
)

func (s State) Terminal() bool { return s == Succeeded || s == Failed || s == Cancelled }

type Job struct {
	Progress *Progress `firestore:"-" json:"-"`
	// Owner remains the indexed storage key. Legacy operator IDs are preserved;
	// publisher jobs use a disjoint publisher:<id> key and explicit owner field.
	PublisherAccountID publisher.ID `firestore:"publisher_account_id,omitempty" json:"-"`
	CreatedBy          Actor        `firestore:"created_by,omitempty" json:"-"`
	Published          bool         `firestore:"published" json:"published"`
	ID                 string       `firestore:"id" json:"id"`
	Owner              string       `firestore:"owner" json:"-"`
	Preset             string       `firestore:"preset" json:"preset"`
	Snapshot           Preset       `firestore:"preset_snapshot" json:"-"`
	State              State        `firestore:"state" json:"state"`
	ProviderName       string       `firestore:"provider_name" json:"-"`
	CreatedAt          time.Time    `firestore:"created_at" json:"created_at"`
	UpdatedAt          time.Time    `firestore:"updated_at" json:"updated_at"`
}

func requestJobID(owner, requestID string) string {
	sum := sha256.Sum256([]byte(owner + ":" + requestID))
	return hex.EncodeToString(sum[:16])
}

// Store.Admit atomically reserves the one active slot and one of the finite
// attempts. A repeated owner/request ID returns its original job, even when the
// budget is exhausted. Attempts are never refunded, including failed starts.
type Store interface {
	Admit(context.Context, string, Preset, string, int, time.Time) (Job, bool, error)
	Get(context.Context, string) (Job, error)
	List(context.Context, string) ([]Job, error)
	Active(context.Context) (*Job, error)
	Transition(context.Context, string, string, State, time.Time) (Job, error)
}

type Provider interface {
	Submit(context.Context, Job, Preset) (string, error)
	Find(context.Context, string) (string, error)
	Status(context.Context, string) (State, error)
	Cancel(context.Context, string) error
}

// Rejected means the provider explicitly refused to create a resource. An
// ambiguous network/5xx failure must retain the slot until reconciliation.
type Rejected struct{ Err error }

func (e *Rejected) Error() string { return e.Err.Error() }
func (e *Rejected) Unwrap() error { return e.Err }
