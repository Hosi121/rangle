package community

import (
	"context"
	"errors"
	"fmt"

	"cloud.google.com/go/firestore"
	"cloud.google.com/go/firestore/apiv1/firestorepb"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// FirestoreStore uses a dedicated named database. It uses the runtime identity
// on Google Cloud, or FIRESTORE_EMULATOR_HOST for explicit local verification.
type FirestoreStore struct{ client *firestore.Client }

func NewFirestoreStore(ctx context.Context, project, database string) (*FirestoreStore, error) {
	if project == "" || database == "" {
		return nil, fmt.Errorf("Firestore project and database are required")
	}
	client, err := firestore.NewClientWithDatabase(ctx, project, database)
	if err != nil {
		return nil, fmt.Errorf("open community database: %w", err)
	}
	return &FirestoreStore{client: client}, nil
}
func (s *FirestoreStore) Close() error { return s.client.Close() }
func storeError(err error) error {
	switch status.Code(err) {
	case codes.NotFound:
		return ErrNotFound
	case codes.AlreadyExists:
		return ErrConflict
	default:
		return err
	}
}
func (s *FirestoreStore) CreateAccount(ctx context.Context, a Account) error {
	doc := s.client.Collection("accounts").Doc(a.User.ID)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		_, err := tx.Get(doc)
		if err == nil {
			return ErrConflict
		}
		if status.Code(err) != codes.NotFound {
			return err
		}
		return tx.Create(doc, a)
	})
}
func (s *FirestoreStore) Account(ctx context.Context, id string) (Account, error) {
	doc, err := s.client.Collection("accounts").Doc(id).Get(ctx)
	if err != nil {
		return Account{}, storeError(err)
	}
	var a Account
	err = doc.DataTo(&a)
	return a, err
}
func (s *FirestoreStore) PutSession(ctx context.Context, id string, value Session) error {
	_, err := s.client.Collection("sessions").Doc(id).Create(ctx, value)
	return storeError(err)
}
func (s *FirestoreStore) Session(ctx context.Context, id string) (Session, error) {
	doc, err := s.client.Collection("sessions").Doc(id).Get(ctx)
	if err != nil {
		return Session{}, storeError(err)
	}
	var session Session
	err = doc.DataTo(&session)
	return session, err
}
func (s *FirestoreStore) DeleteSession(ctx context.Context, id string) error {
	_, err := s.client.Collection("sessions").Doc(id).Delete(ctx)
	return err
}
func (s *FirestoreStore) clip(id string) *firestore.DocumentRef {
	return s.client.Collection("clips").Doc(id)
}

// One document per user avoids a shared counter document on popular clips.
// Repeated PUT/DELETE requests keep the same state and never inflate counts.
func (s *FirestoreStore) SetLike(ctx context.Context, clip, user string, liked bool) error {
	doc := s.clip(clip).Collection("likes").Doc(user)
	if liked {
		_, err := doc.Set(ctx, map[string]bool{"liked": true})
		return err
	}
	_, err := doc.Delete(ctx)
	return err
}
func (s *FirestoreStore) Liked(ctx context.Context, clip, user string) (bool, error) {
	_, err := s.clip(clip).Collection("likes").Doc(user).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return false, nil
	}
	return err == nil, err
}
func count(ctx context.Context, collection *firestore.CollectionRef) (int64, error) {
	result, err := collection.NewAggregationQuery().WithCount("count").Get(ctx)
	if err != nil {
		return 0, err
	}
	value, ok := result["count"].(*firestorepb.Value)
	if !ok {
		return 0, fmt.Errorf("invalid count response")
	}
	return value.GetIntegerValue(), nil
}
func (s *FirestoreStore) Counts(ctx context.Context, clip string) (Counts, error) {
	likes, err := count(ctx, s.clip(clip).Collection("likes"))
	if err != nil {
		return Counts{}, err
	}
	comments, err := count(ctx, s.clip(clip).Collection("comments"))
	return Counts{likes, comments}, err
}
func (s *FirestoreStore) AddComment(ctx context.Context, clip string, c Comment) (Comment, error) {
	doc := s.clip(clip).Collection("comments").Doc(c.ID)
	_, err := doc.Create(ctx, c)
	if status.Code(err) != codes.AlreadyExists {
		return c, err
	}
	old, err := doc.Get(ctx)
	if err != nil {
		return Comment{}, err
	}
	var previous Comment
	if err := old.DataTo(&previous); err != nil {
		return Comment{}, err
	}
	if previous.Author.ID != c.Author.ID || previous.Text != c.Text {
		return Comment{}, ErrConflict
	}
	return previous, nil
}
func (s *FirestoreStore) Comments(ctx context.Context, clip string, after *CommentCursor, limit int) ([]Comment, error) {
	collection := s.clip(clip).Collection("comments")
	query := collection.OrderBy("created_at", firestore.Desc).OrderBy(firestore.DocumentID, firestore.Desc).Limit(limit)
	if after != nil {
		query = query.StartAfter(after.CreatedAt, collection.Doc(after.ID))
	}
	iter := query.Documents(ctx)
	defer iter.Stop()
	comments := make([]Comment, 0)
	for {
		doc, err := iter.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return nil, err
		}
		var c Comment
		if err := doc.DataTo(&c); err != nil {
			return nil, err
		}
		comments = append(comments, c)
	}
	return comments, nil
}
func (s *FirestoreStore) DeleteComment(ctx context.Context, clip, id, user string) error {
	doc := s.clip(clip).Collection("comments").Doc(id)
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		snapshot, err := tx.Get(doc)
		if err != nil {
			return storeError(err)
		}
		var c Comment
		if err := snapshot.DataTo(&c); err != nil {
			return err
		}
		if c.Author.ID != user {
			return ErrForbidden
		}
		return tx.Delete(doc)
	})
}
