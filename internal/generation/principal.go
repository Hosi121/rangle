package generation

import (
	"context"
	"regexp"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
	"github.com/Hosi121/rangle/internal/sports"
)

var operatorIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// Principal is supplied by a trusted authenticator, never the request body.
// The public edge verifies current account/staff state; the private manager
// accepts this assertion only from its pinned runtime service identity.
type Principal struct {
	Kind       string             `json:"kind"`
	ID         string             `json:"id"`
	Account    *publisher.Account `json:"account,omitempty"`
	Role       publisher.Role     `json:"role,omitempty"`
	Generation int64              `json:"generation,omitempty"`
}
type Actor struct {
	Kind       string `firestore:"kind"`
	ID         string `firestore:"id"`
	Generation int64  `firestore:"generation,omitempty"`
}

func OperatorPrincipal(id string) Principal { return Principal{Kind: "operator", ID: id} }
func (p Principal) valid() bool {
	switch p.Kind {
	case "operator":
		return operatorIdentifier.MatchString(p.ID) && p.Account == nil && p.Role == "" && p.Generation == 0
	case "staff":
		return publisher.StaffID(p.ID).Valid() && p.Account != nil && p.Account.Validate() == nil && (p.Account.Status == "" || p.Account.Status == publisher.Active) && (p.Role == publisher.Administrator || p.Role == publisher.Editor) && p.Generation > 0
	default:
		return false
	}
}
func (p Principal) allows(permission publisher.Permission) bool {
	switch permission {
	case publisher.ReadPresets, publisher.CreateGeneration, publisher.ReadGeneration, publisher.CancelGeneration, publisher.PublishClip, publisher.WithdrawClip:
		return p.valid() && (p.Kind == "operator" || p.Role.Allows(permission))
	default:
		return false
	}
}

func (p Principal) ownerKey() string {
	if p.Kind == "staff" {
		return "publisher:" + string(p.Account.ID)
	}
	return p.ID
}
func (p Principal) accountID() publisher.ID {
	if p.Kind == "staff" {
		return p.Account.ID
	}
	return ""
}
func (p Principal) actor() Actor { return Actor{Kind: p.Kind, ID: p.ID, Generation: p.Generation} }
func (p Principal) owns(j Job) bool {
	return p.valid() && j.Owner == p.ownerKey() && j.PublisherAccountID == p.accountID()
}
func (p Principal) permitsPreset(preset Preset) bool {
	if p.Kind == "operator" {
		return p.valid()
	}
	if !p.valid() {
		return false
	}
	for _, id := range preset.PublisherAccounts {
		if id == p.Account.ID {
			return true
		}
	}
	return false
}
func (p Principal) snapshot(preset Preset) Preset {
	if p.Kind != "staff" {
		return preset
	}
	preset.Context = preset.Context.Clone()
	if preset.Context == nil {
		preset.Context = &sports.Context{}
	}
	preset.Context.Publisher = &publisher.Account{ID: p.Account.ID, Name: p.Account.Name, Kind: p.Account.Kind}
	return preset
}

// ScopedAdmission stores immutable organization ownership and the creator in
// the same reservation transaction. Legacy Store implementations cannot admit
// staff jobs until they implement this interface.
type ScopedAdmission interface {
	AdmitScoped(context.Context, Principal, Preset, string, int, time.Time) (Job, bool, error)
}
