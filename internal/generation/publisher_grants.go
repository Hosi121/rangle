package generation

import (
	"context"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/Hosi121/rangle/internal/publisher"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// A missing override falls back to immutable config. An empty stored override
// revokes all input grants, without withdrawing access to existing jobs.
type PublisherGrants struct {
	Account   publisher.ID `firestore:"publisher_account_id"`
	Presets   []string     `firestore:"presets"`
	UpdatedAt time.Time    `firestore:"updated_at"`
	UpdatedBy string       `firestore:"updated_by"`
}
type PublisherGrantStore interface {
	PublisherGrants(context.Context, publisher.ID) (*PublisherGrants, error)
	SetPublisherGrants(context.Context, publisher.ID, []string, string, time.Time) error
}

func (g PublisherGrants) valid(account publisher.ID) bool {
	if !account.Valid() || g.Account != account || len(g.Presets) > 10 || !operatorIdentifier.MatchString(g.UpdatedBy) || g.UpdatedAt.IsZero() {
		return false
	}
	seen := map[string]bool{}
	for _, id := range g.Presets {
		if !identifier.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (g PublisherGrants) allows(preset string) bool {
	for _, id := range g.Presets {
		if id == preset {
			return true
		}
	}
	return false
}
func (s *FirestoreStore) grantsDoc(account publisher.ID) *firestore.DocumentRef {
	return s.client.Collection("generation_publisher_grants").Doc(string(account))
}
func decodeGrants(doc *firestore.DocumentSnapshot, err error, account publisher.ID) (*PublisherGrants, error) {
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var grants PublisherGrants
	if err := doc.DataTo(&grants); err != nil {
		return nil, err
	}
	if !grants.valid(account) {
		return nil, fmt.Errorf("invalid publisher input grants")
	}
	return &grants, nil
}
func (s *FirestoreStore) PublisherGrants(ctx context.Context, account publisher.ID) (*PublisherGrants, error) {
	if !account.Valid() {
		return nil, ErrNotFound
	}
	doc, err := s.grantsDoc(account).Get(ctx)
	return decodeGrants(doc, err, account)
}
func (s *FirestoreStore) SetPublisherGrants(ctx context.Context, account publisher.ID, presets []string, operator string, now time.Time) error {
	g := PublisherGrants{account, append([]string{}, presets...), now, operator}
	if !g.valid(account) {
		return ErrInvalid
	}
	sort.Strings(g.Presets)
	_, err := s.grantsDoc(account).Set(ctx, g)
	return err
}
func (s *Service) PresetsFor(ctx context.Context, p Principal) ([]Preset, error) {
	if !p.allows(publisher.ReadPresets) {
		return nil, ErrPublisherForbidden
	}
	if err := s.principalAccess(ctx, p); err != nil {
		return nil, err
	}
	var grants *PublisherGrants
	if store, ok := s.Store.(PublisherGrantStore); ok && p.Kind == "staff" {
		var err error
		grants, err = store.PublisherGrants(ctx, p.Account.ID)
		if err != nil {
			return nil, err
		}
	}
	rows := make([]Preset, 0)
	for _, preset := range s.Config.Presets {
		allowed := p.permitsPreset(preset)
		if grants != nil {
			allowed = grants.allows(preset.ID)
		}
		if allowed {
			rows = append(rows, preset)
		}
	}
	return rows, nil
}

func (s *Service) SetInputGrants(ctx context.Context, p Principal, account publisher.ID, ids []string) error {
	if p.Kind != "operator" || !p.valid() {
		return ErrForbidden
	}
	if !account.Valid() || len(ids) > len(s.Config.Presets) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if _, ok := s.Config.preset(id); !ok || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	store, ok := s.Store.(PublisherGrantStore)
	if !ok {
		return fmt.Errorf("publisher input assignment unavailable")
	}
	if err := store.SetPublisherGrants(ctx, account, ids, p.ID, s.now()); err != nil {
		return err
	}
	if s.Logger != nil {
		s.Logger.InfoContext(ctx, "publisher input grants changed", "publisher_account_id", account, "actor_id", p.ID, "preset_count", len(ids))
	}
	return nil
}
