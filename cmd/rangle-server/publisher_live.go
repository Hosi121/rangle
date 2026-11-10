package main

import (
	"context"
	"encoding/json"

	"github.com/Hosi121/rangle/internal/community"
	"github.com/Hosi121/rangle/internal/live"
)

// Keep private source snapshots for audit, but remove deleted owned sessions
// from the public API. One batched tombstone lookup per refreshed index, never
// a catalog/manager fan-out for every viewer or segment.
func filterDeletedLive(read func(context.Context) ([]byte, error), store community.Store, origin string) func(context.Context) ([]byte, error) {
	s, ok := store.(interface {
		DeletedClips(context.Context, []string) (map[string]bool, error)
	})
	if !ok {
		return read
	}
	return func(ctx context.Context) ([]byte, error) {
		data, err := read(ctx)
		if err != nil {
			return nil, err
		}
		index, err := live.ParseIndex(data, origin)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(index.Sessions))
		for _, session := range index.Sessions {
			ids = append(ids, "generated-"+session.ID)
		}
		deleted, err := s.DeletedClips(ctx, ids)
		if err != nil {
			return nil, err
		}
		sessions := make([]live.Session, 0, len(index.Sessions))
		for _, session := range index.Sessions {
			if !deleted["generated-"+session.ID] {
				sessions = append(sessions, session)
			}
		}
		index.Sessions = sessions
		return json.Marshal(index)
	}
}
