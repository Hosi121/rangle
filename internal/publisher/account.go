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

type Account struct {
	ID   ID     `json:"id" firestore:"id"`
	Name string `json:"name" firestore:"name"`
	Kind string `json:"kind" firestore:"kind"` // company or organization
}

var accountID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var userID = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)

func (a Account) Validate() error {
	if !accountID.MatchString(string(a.ID)) || strings.TrimSpace(a.Name) == "" || !utf8.ValidString(a.Name) || utf8.RuneCountInString(a.Name) > 120 || strings.ContainsFunc(a.Name, unicode.IsControl) || (a.Kind != "company" && a.Kind != "organization") {
		return fmt.Errorf("invalid publisher account")
	}
	return nil
}

type Role string

const (
	Owner  Role = "owner"
	Editor Role = "editor"
)

// Membership is private authorization data, not part of the public clip profile.
// A future repository must load it from trusted storage for each mutation;
// request bodies, profile metadata and cached session flags cannot supply it.
type Membership struct {
	PublisherAccountID ID     `json:"-" firestore:"publisher_account_id"`
	UserID             string `json:"-" firestore:"user_id"`
	Role               Role   `json:"-" firestore:"role"`
}

// CanPublish is a domain rule. HTTP/storage integration remains separate from
// this model: the current generation API still requires a platform admin.
func (a Account) CanPublish(actorUserID string, membership Membership) bool {
	return a.Validate() == nil && userID.MatchString(actorUserID) && membership.UserID == actorUserID && membership.PublisherAccountID == a.ID && (membership.Role == Owner || membership.Role == Editor)
}
