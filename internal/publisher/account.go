// Package publisher models organization publishing accounts separately from
// the personal identities used to sign in and participate in the community.
package publisher

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ID is a publisher account identity, never a personal username or session ID.
type ID string

type Status string

const (
	Active    Status = "active"
	Suspended Status = "suspended"
	Deleting  Status = "deleting"
	Deleted   Status = "deleted"
)

type Account struct {
	DeletionRequested bool `json:"-" firestore:"deletion_requested,omitempty"`
	// Status is private lifecycle state. Public catalog profiles omit it and
	// therefore cannot act as authoritative account records for authorization.
	Status Status `json:"-" firestore:"status,omitempty"`
	ID     ID     `json:"id" firestore:"id"`
	Name   string `json:"name" firestore:"name"`
	Kind   string `json:"kind" firestore:"kind"` // company or organization
}

var accountID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var staffUsername = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)

func (a Account) Validate() error {
	if !accountID.MatchString(string(a.ID)) || strings.TrimSpace(a.Name) == "" || !utf8.ValidString(a.Name) || utf8.RuneCountInString(a.Name) > 120 || strings.ContainsFunc(a.Name, unicode.IsControl) || (a.Kind != "company" && a.Kind != "organization") {
		return fmt.Errorf("invalid publisher account")
	}
	switch a.Status {
	case "", Active, Suspended, Deleting, Deleted:
		return nil
	default:
		return fmt.Errorf("invalid publisher account status")
	}
}

// StaffID is local to one publisher account. It has no relationship to a
// community viewer ID, even when two names or IDs happen to be equal.
type StaffID string

type Role string

const (
	Administrator Role = "administrator"
	Editor        Role = "editor"
)

// StaffAccount belongs to publisher_accounts/{account}/staff_accounts/{id}.
// Credentials belong to a separate private storage record, never this profile.
// Refresh this record for mutations instead of copying roles into a session.
type StaffAccount struct {
	ID                 StaffID `json:"-" firestore:"id"`
	PublisherAccountID ID      `json:"-" firestore:"publisher_account_id"`
	Username           string  `json:"-" firestore:"username"`
	Role               Role    `json:"-" firestore:"role"`
	Disabled           bool    `json:"-" firestore:"disabled"`
}

// StaffPrincipal identifies the authenticated account-local staff identity.
// It carries no permission claims and cannot represent a viewer or operator.
type StaffPrincipal struct {
	PublisherAccountID ID
	StaffAccountID     StaffID
}

type Permission string

const (
	ReadPresets      Permission = "generation.presets.read"
	CreateGeneration Permission = "generation.jobs.create"
	ReadGeneration   Permission = "generation.jobs.read"
	CancelGeneration Permission = "generation.jobs.cancel"
	PublishClip      Permission = "clips.publish"
	WithdrawClip     Permission = "clips.withdraw"
	ManageStaff      Permission = "staff.manage"
	RequestDeletion  Permission = "account.deletion.request"
)

// Allows authenticates the account/staff relationship and resolves organization
// RBAC. The caller must refresh the owning account and staff from trusted data.
// The account receiver must be the resource's owning account from trusted data.
func (a Account) Allows(actor StaffPrincipal, staff StaffAccount, permission Permission) bool {
	if a.Validate() != nil || a.Status != Active || actor.PublisherAccountID != a.ID || staff.PublisherAccountID != a.ID ||
		actor.StaffAccountID != staff.ID || !accountID.MatchString(string(staff.ID)) || !staffUsername.MatchString(staff.Username) || staff.Disabled {
		return false
	}
	return staff.Role.Allows(permission)
}

// Allows resolves only the role-to-permission matrix, without authenticating
// the caller or replacing account/staff lifecycle checks.
func (role Role) Allows(permission Permission) bool {
	switch permission {
	case ReadPresets, CreateGeneration, ReadGeneration, CancelGeneration, PublishClip, WithdrawClip:
		return role == Administrator || role == Editor
	case ManageStaff, RequestDeletion:
		return role == Administrator
	default:
		return false
	}
}

func (id ID) Valid() bool      { return accountID.MatchString(string(id)) }
func (id StaffID) Valid() bool { return accountID.MatchString(string(id)) }

func (s StaffAccount) Validate() error {
	if !s.ID.Valid() || !s.PublisherAccountID.Valid() || !staffUsername.MatchString(s.Username) || (s.Role != Administrator && s.Role != Editor) {
		return fmt.Errorf("invalid staff account")
	}
	return nil
}
