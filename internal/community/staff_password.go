package community

import (
	"errors"
	"net/http"

	"github.com/Hosi121/rangle/internal/publisher"
)

func (h *Handler) resetStaffPassword(w http.ResponseWriter, r *http.Request, operator bool) {
	m, ok := h.staffManager(w, r, operator)
	if !ok {
		return
	}
	store, ok := h.cfg.Store.(StaffPasswordStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	account, id := publisher.ID(r.PathValue("account")), publisher.StaffID(r.PathValue("staff"))
	if !id.Valid() {
		h.fail(w, r, 404, "not_found")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, 400, "invalid_request")
		return
	}
	if !h.decode(w, r, &input) {
		return
	}
	if !validPassword(input.Password) {
		h.fail(w, r, 400, "invalid_password")
		return
	}
	if !h.acquireHash(w, r, "staff-password:"+string(account)+":"+string(id)) {
		return
	}
	defer func() { <-h.hashes }()
	password, err := newStaffPassword(input.Password)
	if err != nil {
		h.unavailable(w, r, "staff_password_prepare", err)
		return
	}
	row, err := store.ResetStaffPassword(r.Context(), m, account, id, password)
	if err != nil {
		h.staffPasswordError(w, r, err)
		return
	}
	if m.Principal.PublisherAccountID == account && m.Principal.StaffAccountID == id {
		http.SetCookie(w, h.staffCookie("", -1))
	}
	h.logStaffChange(r, m, account, row.Staff, "staff.password.reset")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) changeStaffPassword(w http.ResponseWriter, r *http.Request) {
	identity, err := h.CurrentStaff(r)
	if err != nil {
		h.unavailable(w, r, "staff_password_session", err)
		return
	}
	if identity == nil {
		h.fail(w, r, 401, "not_authenticated")
		return
	}
	store, ok := h.cfg.Store.(StaffPasswordStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	var input struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if r.URL.RawQuery != "" {
		h.fail(w, r, 400, "invalid_request")
		return
	}
	if !h.decode(w, r, &input) {
		return
	}
	if !validPassword(input.New) || len(input.Current) > 512 {
		h.fail(w, r, 400, "invalid_password")
		return
	}
	key := "staff-password:" + string(identity.Account.ID) + ":" + string(identity.Record.Staff.ID)
	if !h.acquireHash(w, r, key) {
		return
	}
	defer func() { <-h.hashes }()
	if !checkPassword(staffPassword(identity.Record), input.Current) {
		h.fail(w, r, 401, "invalid_credentials")
		return
	}
	password, err := newStaffPassword(input.New)
	if err != nil {
		h.unavailable(w, r, "staff_password_prepare", err)
		return
	}
	row, err := store.ChangeStaffPassword(r.Context(), identity.Principal, identity.Record.Version, password)
	if err != nil {
		h.staffPasswordError(w, r, err)
		return
	}
	http.SetCookie(w, h.staffCookie("", -1))
	h.logStaffChange(r, StaffManager{Principal: identity.Principal, Version: identity.Record.Version}, identity.Account.ID, row.Staff, "staff.password.change")
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) staffPasswordError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrConflict) {
		h.fail(w, r, 409, "credentials_changed")
		return
	}
	h.staffManagementError(w, r, "staff_password", err)
}
