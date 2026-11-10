package community

import (
	"net/http"

	"github.com/Hosi121/rangle/internal/publisher"
)

func (h *Handler) publisherDeletionRequest(w http.ResponseWriter, r *http.Request) {
	m, ok := h.staffManager(w, r, false)
	if !ok {
		return
	}
	store, ok := h.cfg.Store.(PublisherDeletionStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	id := publisher.ID(r.PathValue("account"))
	if r.URL.RawQuery != "" {
		h.fail(w, r, 400, "invalid_request")
		return
	}
	if r.Method == http.MethodGet {
		a, err := store.Publisher(r.Context(), id)
		if err != nil {
			h.staffManagementError(w, r, "publisher_deletion_request", err)
			return
		}
		h.write(w, r, 200, map[string]bool{"requested": a.DeletionRequested})
		return
	}
	var body struct {
		Requested    *bool  `json:"requested"`
		Confirmation string `json:"confirmation"`
	}
	if !h.decode(w, r, &body) {
		return
	}
	if body.Requested == nil || body.Confirmation != string(id) {
		h.fail(w, r, 400, "confirmation_required")
		return
	}
	a, err := store.RequestPublisherDeletion(r.Context(), m, id, *body.Requested)
	if err != nil {
		h.staffManagementError(w, r, "publisher_deletion_request", err)
		return
	}
	h.cfg.Logger.InfoContext(r.Context(), "publisher deletion request changed", "publisher_account_id", id, "staff_id", m.Principal.StaffAccountID, "requested", a.DeletionRequested)
	h.write(w, r, 200, map[string]bool{"requested": a.DeletionRequested})
}
