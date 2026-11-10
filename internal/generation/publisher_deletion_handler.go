package generation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

const publisherDeletionPath = "/api/v1/operations/publishers/{account}/deletion"

func newPublisherDeletionHandler(s *Service, auth func(*http.Request) (Principal, error)) (http.Handler, error) {
	h, err := newHandlerBase(s, auth)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	for _, method := range []string{"GET", "DELETE"} {
		mux.HandleFunc(method+" "+publisherDeletionPath, func(w http.ResponseWriter, r *http.Request) {
			p, ok := h.authority(w, r, publisher.ReadPresets)
			if !ok {
				return
			}
			if p.Kind != "operator" {
				h.fail(w, r, ErrForbidden)
				return
			}
			account := publisher.ID(r.PathValue("account"))
			if !account.Valid() || r.URL.RawQuery != "" {
				h.fail(w, r, ErrInvalid)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
			defer cancel()
			var d PublisherLifecycle
			var err error
			if r.Method == "DELETE" {
				var body struct {
					RequestID string `json:"request_id"`
				}
				typ, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
				decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
				decoder.DisallowUnknownFields()
				if e != nil || typ != "application/json" || decoder.Decode(&body) != nil || decoder.Decode(new(any)) != io.EOF || !requestID.MatchString(body.RequestID) {
					h.fail(w, r, ErrInvalid)
					return
				}
				d, err = s.StartPublisherDeletion(ctx, p.ID, account, body.RequestID)
			} else {
				d, err = s.PublisherDeletionStatus(ctx, p.ID, account)
			}
			if err != nil {
				h.fail(w, r, err)
				return
			}
			h.write(w, r, 200, d)
		})
	}
	return mux, nil
}

func (c *Remote) deletionCall(ctx context.Context, actor string, account publisher.ID, rid string) (PublisherLifecycle, error) {
	var d PublisherLifecycle
	if !operatorIdentifier.MatchString(actor) || !account.Valid() {
		return d, ErrInvalid
	}
	method := "GET"
	var body io.Reader
	if rid != "" {
		if !requestID.MatchString(rid) {
			return d, ErrInvalid
		}
		method = "DELETE"
		data, _ := json.Marshal(map[string]string{"request_id": rid})
		body = bytes.NewReader(data)
	}
	code, _, payload, err := c.call(ctx, method, "/api/v1/operations/publishers/"+string(account)+"/deletion", actor, "application/json", body)
	if err != nil {
		return d, err
	}
	// Private response omits audit/operator/revision fields; validate its public
	// shape separately, before it can finalize the community lifecycle.
	if code != 200 || json.Unmarshal(payload, &d) != nil || d.Account != account || !validDeletionResponse(d) {
		return d, fmt.Errorf("publisher cleanup unavailable")
	}
	return d, nil
}
func validDeletionResponse(d PublisherLifecycle) bool {
	if !d.Account.Valid() || len(d.Jobs) > MaxAttempts || d.Prefix < 0 || d.Prefix > 3*len(d.Jobs) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range d.Jobs {
		if !requestID.MatchString(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	if d.State == publisher.Active {
		return d.RequestID == "" && len(d.Jobs) == 0 && d.Stage == ""
	}
	if !requestID.MatchString(d.RequestID) {
		return false
	}
	if d.State == publisher.Deleted {
		return d.Stage == "complete" && d.Prefix == 3*len(d.Jobs)
	}
	return d.State == publisher.Deleting && (d.Stage == "jobs" || d.Stage == "objects" || d.Stage == "cache")
}
func (c *Remote) PublisherDeletionStatus(ctx context.Context, actor string, account publisher.ID) (PublisherLifecycle, error) {
	return c.deletionCall(ctx, actor, account, "")
}
func (c *Remote) StartPublisherDeletion(ctx context.Context, actor string, account publisher.ID, rid string) (PublisherLifecycle, error) {
	return c.deletionCall(ctx, actor, account, rid)
}
