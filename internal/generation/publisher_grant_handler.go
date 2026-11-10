package generation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

const publisherGrantPath = "/api/v1/operations/publishers/{account}/generation/presets"

func operatorAuthority(user func(*http.Request) (string, error)) func(*http.Request) (Principal, error) {
	return func(r *http.Request) (Principal, error) {
		id, err := user(r)
		if id == "" {
			return Principal{}, err
		}
		return OperatorPrincipal(id), err
	}
}

func NewPublisherGrantHandler(s *Service, user func(*http.Request) (string, error)) (http.Handler, error) {
	if user == nil {
		return nil, ErrInvalid
	}
	return newPublisherGrantHandler(s, operatorAuthority(user))
}

func newPublisherGrantHandler(s *Service, auth func(*http.Request) (Principal, error)) (http.Handler, error) {
	h, err := newHandlerBase(s, auth)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	for _, method := range []string{"GET", "PUT"} {
		mux.HandleFunc(method+" "+publisherGrantPath, func(w http.ResponseWriter, r *http.Request) {
			p, ok := h.authority(w, r, publisher.ReadPresets)
			if !ok {
				return
			}
			if p.Kind != "operator" {
				h.fail(w, r, ErrForbidden)
				return
			}
			account := publisher.ID(r.PathValue("account"))
			if !account.Valid() {
				h.fail(w, r, ErrNotFound)
				return
			}
			if r.URL.RawQuery != "" {
				h.fail(w, r, ErrInvalid)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			if r.Method == "PUT" {
				typ, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
				if err != nil || typ != "application/json" {
					h.fail(w, r, ErrInvalid)
					return
				}
				var body struct {
					Presets *[]string `json:"presets"`
				}
				d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
				d.DisallowUnknownFields()
				if d.Decode(&body) != nil || d.Decode(new(any)) != io.EOF || body.Presets == nil {
					h.fail(w, r, ErrInvalid)
					return
				}
				if err := s.SetInputGrants(ctx, p, account, *body.Presets); err != nil {
					h.fail(w, r, err)
					return
				}
			}
			var grants *PublisherGrants
			var err error
			if store, ok := s.Store.(PublisherGrantStore); ok {
				grants, err = store.PublisherGrants(ctx, account)
			} else {
				h.write(w, r, 503, map[string]string{"error": "generation_unavailable"})
				return
			}
			if err != nil {
				h.fail(w, r, err)
				return
			}
			rows := make([]map[string]any, 0, len(s.Config.Presets))
			for _, preset := range s.Config.Presets {
				enabled := false
				if grants != nil {
					enabled = grants.allows(preset.ID)
				} else {
					for _, id := range preset.PublisherAccounts {
						if id == account {
							enabled = true
							break
						}
					}
				}
				rows = append(rows, map[string]any{"id": preset.ID, "title": preset.Title, "mode": preset.ModeName(), "source": preset.Source, "license": preset.License, "enabled": enabled})
			}
			h.write(w, r, 200, map[string]any{"presets": rows})
		})
	}
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		protected.ServeHTTP(w, r)
	}), nil
}

func NewPublisherGrantRemoteHandler(remote *Remote, user func(*http.Request) (string, error)) (http.Handler, error) {
	if remote == nil || user == nil {
		return nil, ErrInvalid
	}
	h := &RemoteHandler{remote: remote}
	mux := http.NewServeMux()
	for _, method := range []string{"GET", "PUT"} {
		mux.HandleFunc(method+" "+publisherGrantPath, func(w http.ResponseWriter, r *http.Request) {
			id, err := user(r)
			if err != nil {
				if errors.Is(err, ErrForbidden) {
					remoteError(w, r, 403, "admin_required")
				} else if errors.Is(err, ErrNotFound) {
					remoteError(w, r, 404, "not_found")
				} else {
					remoteError(w, r, 503, "generation_unavailable")
				}
				return
			}
			if id == "" {
				remoteError(w, r, 401, "not_authenticated")
				return
			}
			account := publisher.ID(r.PathValue("account"))
			if !account.Valid() {
				remoteError(w, r, 404, "not_found")
				return
			}
			if r.URL.RawQuery != "" {
				remoteError(w, r, 400, "invalid_request")
				return
			}
			h.forwardAs(w, r, "/api/v1/operations/publishers/"+string(account)+"/generation/presets", id, nil)
		})
	}
	protected := http.NewCrossOriginProtection().Handler(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		protected.ServeHTTP(w, r)
	}), nil
}
