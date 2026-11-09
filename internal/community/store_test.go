package community

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func storeContract(t *testing.T, s Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a := Account{User: User{ID: "owner", Name: "Owner"}, Salt: []byte("0123456789abcdef"), Hash: make([]byte, 32), HashVersion: 1}
	var created atomic.Int32
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			err := s.CreateAccount(ctx, a)
			if err == nil {
				created.Add(1)
			} else if !errors.Is(err, ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if created.Load() != 1 {
		t.Fatal("account uniqueness is not atomic")
	}
	if got, err := s.Account(ctx, a.User.ID); err != nil || got.User != a.User {
		t.Fatalf("account roundtrip: %+v %v", got, err)
	}
	session := Session{a.User, time.Now().UTC().Truncate(time.Microsecond)}
	if err := s.PutSession(ctx, "session-hash", session); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Session(ctx, "session-hash"); err != nil || got.User != a.User || !got.ExpiresAt.Equal(session.ExpiresAt) {
		t.Fatalf("session roundtrip: %+v %v", got, err)
	}
	if err := s.DeleteSession(ctx, "session-hash"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, "session-hash"); !errors.Is(err, ErrNotFound) {
		t.Fatal("deleted session remained usable")
	}
	for range 8 {
		wg.Go(func() {
			if err := s.SetLike(ctx, "clip", "owner", true); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if counts, err := s.Counts(ctx, "clip"); err != nil || counts.Likes != 1 {
		t.Fatalf("duplicate likes: %+v %v", counts, err)
	}
	if liked, err := s.Liked(ctx, "clip", "owner"); err != nil || !liked {
		t.Fatal("like state missing")
	}
	if err := s.SetLike(ctx, "clip", "owner", false); err != nil {
		t.Fatal(err)
	}
	if counts, err := s.Counts(ctx, "clip"); err != nil || counts.Likes != 0 {
		t.Fatal("unlike failed")
	}
	comment := Comment{ID: "0123456789abcdef0123456789abcdef", Author: a.User, Text: "test", CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	for range 2 {
		if _, err := s.AddComment(ctx, "clip", comment); err != nil {
			t.Fatal(err)
		}
	}
	if counts, err := s.Counts(ctx, "clip"); err != nil || counts.Comments != 1 {
		t.Fatalf("comment retry: %+v %v", counts, err)
	}
	changed := comment
	changed.Text = "different"
	if _, err := s.AddComment(ctx, "clip", changed); !errors.Is(err, ErrConflict) {
		t.Fatal("same request ID accepted with different text")
	}
	comments, err := s.Comments(ctx, "clip", nil, 20)
	if err != nil || len(comments) != 1 || comments[0].ID != comment.ID {
		t.Fatalf("comments: %+v %v", comments, err)
	}
	comments, err = s.Comments(ctx, "clip", &CommentCursor{"clip", comment.ID, comment.CreatedAt}, 20)
	if err != nil || len(comments) != 0 {
		t.Fatalf("cursor repeated a comment: %+v %v", comments, err)
	}
	if err := s.DeleteComment(ctx, "clip", comment.ID, "other"); !errors.Is(err, ErrForbidden) {
		t.Fatal("another user deleted comment")
	}
	if err := s.DeleteComment(ctx, "clip", comment.ID, "owner"); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryStoreContract(t *testing.T) { storeContract(t, NewMemoryStore()) }

func TestFirestoreStoreContract(t *testing.T) {
	endpoint := os.Getenv("FIRESTORE_EMULATOR_HOST")
	if endpoint == "" {
		t.Skip("set FIRESTORE_EMULATOR_HOST to a loopback emulator")
	}
	host, _, err := net.SplitHostPort(endpoint)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || !ip.IsLoopback() {
		t.Fatal("integration tests require a loopback emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	project := fmt.Sprintf("demo-rangle-%x", time.Now().UnixNano())
	s, err := NewFirestoreStore(ctx, project, "community")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	storeContract(t, s)
	second, err := NewFirestoreStore(ctx, project, "community")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if a, err := second.Account(ctx, "owner"); err != nil || a.User.ID != "owner" {
		t.Fatal("account did not survive reopening the client")
	}
}
