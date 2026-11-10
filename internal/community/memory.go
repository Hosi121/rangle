package community

import (
	"context"
	"sort"
	"sync"

	"github.com/Hosi121/rangle/internal/publisher"
)

// MemoryStore is for tests and explicit local demos. Cloud deployments must use
// persistent storage; no interaction survives a MemoryStore process restart.
type MemoryStore struct {
	publishers    map[publisher.ID]publisher.Account
	staff         map[publisher.ID]map[publisher.StaffID]StaffRecord
	staffNames    map[publisher.ID]map[string]publisher.StaffID
	staffSessions map[string]StaffSession
	deletions     map[publisher.ID]PublisherDeletion
	deletedClips  map[string]publisher.ID
	mu            sync.Mutex
	accounts      map[string]Account
	sessions      map[string]Session
	likes         map[string]map[string]bool
	comments      map[string]map[string]Comment
	cheers        map[string]map[string]CheerBatch
	history       map[string]map[string]CheeredClip
	follows       map[string]map[publisher.ID]bool
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{accounts: map[string]Account{}, sessions: map[string]Session{},
		likes: map[string]map[string]bool{}, comments: map[string]map[string]Comment{}}
}

func (s *MemoryStore) CreateAccount(_ context.Context, a Account) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.accounts[a.User.ID]; ok {
		return ErrConflict
	}
	s.accounts[a.User.ID] = a
	return nil
}
func (s *MemoryStore) Account(_ context.Context, id string) (Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return a, nil
}
func (s *MemoryStore) PutSession(_ context.Context, id string, value Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[id] = value
	return nil
}
func (s *MemoryStore) Session(_ context.Context, id string) (Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.sessions[id]
	if !ok {
		return Session{}, ErrNotFound
	}
	return value, nil
}
func (s *MemoryStore) DeleteSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}
func (s *MemoryStore) SetLike(_ context.Context, clip, user string, liked bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.likes[clip] == nil {
		s.likes[clip] = map[string]bool{}
	}
	if liked {
		s.likes[clip][user] = true
	} else {
		delete(s.likes[clip], user)
	}
	return nil
}
func (s *MemoryStore) Liked(_ context.Context, clip, user string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.likes[clip][user], nil
}
func (s *MemoryStore) Counts(_ context.Context, clip string) (Counts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Counts{int64(len(s.likes[clip])), int64(len(s.comments[clip]))}, nil
}
func (s *MemoryStore) AddComment(_ context.Context, clip string, c Comment) (Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.comments[clip] == nil {
		s.comments[clip] = map[string]Comment{}
	}
	if old, ok := s.comments[clip][c.ID]; ok {
		if old.Author.ID != c.Author.ID || old.Text != c.Text {
			return Comment{}, ErrConflict
		}
		return old, nil
	}
	s.comments[clip][c.ID] = c
	return c, nil
}
func (s *MemoryStore) Comments(_ context.Context, clip string, after *CommentCursor, limit int) ([]Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows := make([]Comment, 0)
	for _, c := range s.comments[clip] {
		if after == nil || c.CreatedAt.Before(after.CreatedAt) || c.CreatedAt.Equal(after.CreatedAt) && c.ID < after.ID {
			rows = append(rows, c)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].ID > rows[j].ID
		}
		return rows[i].CreatedAt.After(rows[j].CreatedAt)
	})
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}
func (s *MemoryStore) DeleteComment(_ context.Context, clip, id, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.comments[clip][id]
	if !ok {
		return ErrNotFound
	}
	if c.Author.ID != user {
		return ErrForbidden
	}
	delete(s.comments[clip], id)
	return nil
}
