// Package community owns accounts and social interactions independently of the
// immutable clip catalog and media delivery path.
package community

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrConflict  = errors.New("already exists")
	ErrForbidden = errors.New("forbidden")
)

// User is a personal viewing identity. The legacy Admin flag identifies a
// rangle platform operator; organization staff have separate account-local IDs.
type User struct {
	ID    string `json:"id" firestore:"id"`
	Name  string `json:"name" firestore:"name"`
	Admin bool   `json:"is_admin,omitempty" firestore:"admin,omitempty"`
}

// Account is a storage record. Only its User may be sent to a browser.
type Account struct {
	User        User   `firestore:"user"`
	Salt        []byte `firestore:"salt"`
	Hash        []byte `firestore:"hash"`
	HashVersion int    `firestore:"hash_version"`
}

type Session struct {
	User      User      `firestore:"user"`
	ExpiresAt time.Time `firestore:"expires_at"`
}

type Counts struct {
	Likes    int64 `json:"likes"`
	Comments int64 `json:"comments"`
}

type Comment struct {
	ID        string    `json:"id" firestore:"id"`
	Author    User      `json:"author" firestore:"author"`
	Text      string    `json:"text" firestore:"text"`
	CreatedAt time.Time `json:"created_at" firestore:"created_at"`
}

type CommentCursor struct {
	Clip      string    `json:"clip"`
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
}

// Store implementations must make account creation and likes idempotent under
// concurrent callers. Sessions are indexed by a hash, never the bearer cookie.
type Store interface {
	CreateAccount(context.Context, Account) error
	Account(context.Context, string) (Account, error)
	PutSession(context.Context, string, Session) error
	Session(context.Context, string) (Session, error)
	DeleteSession(context.Context, string) error
	SetLike(context.Context, string, string, bool) error
	Liked(context.Context, string, string) (bool, error)
	Counts(context.Context, string) (Counts, error)
	AddComment(context.Context, string, Comment) (Comment, error)
	Comments(context.Context, string, *CommentCursor, int) ([]Comment, error)
	DeleteComment(context.Context, string, string, string) error
}
