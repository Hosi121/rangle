package community

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

func (h *Handler) staffManager(w http.ResponseWriter, r *http.Request, operator bool) (StaffManager, bool) {
	account := publisher.ID(r.PathValue("account"))
	if !account.Valid() {
		h.fail(w, r, 404, "not_found")
		return StaffManager{}, false
	}
	var manager StaffManager
	if operator {
		user, err := h.AdminUser(r)
		if errors.Is(err, ErrForbidden) {
			h.fail(w, r, 403, "operator_required")
			return manager, false
		}
		if err != nil {
			h.unavailable(w, r, "staff_operator", err)
			return manager, false
		}
		if user == nil {
			h.fail(w, r, 401, "not_authenticated")
			return manager, false
		}
		manager.OperatorID = user.ID
	} else {
		identity, err := h.CurrentStaff(r)
		if err != nil {
			h.unavailable(w, r, "staff_manager_session", err)
			return manager, false
		}
		if identity == nil {
			h.fail(w, r, 401, "not_authenticated")
			return manager, false
		}
		if identity.Account.ID != account {
			h.fail(w, r, 404, "not_found")
			return manager, false
		}
		if !identity.Account.Allows(identity.Principal, identity.Record.Staff, publisher.ManageStaff) {
			h.fail(w, r, 403, "forbidden")
			return manager, false
		}
		manager.Principal, manager.Version = identity.Principal, identity.Record.Version
	}
	key := manager.OperatorID + ":" + string(manager.Principal.StaffAccountID)
	if !h.allow("staff-management:"+string(account)+":"+key, time.Second, 10) {
		w.Header().Set("Retry-After", "1")
		h.fail(w, r, 429, "rate_limited")
		return manager, false
	}
	return manager, true
}
func (h *Handler) staffManagementError(w http.ResponseWriter, r *http.Request, operation string, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		h.fail(w, r, 403, "forbidden")
	case errors.Is(err, ErrNotFound):
		h.fail(w, r, 404, "not_found")
	case errors.Is(err, ErrConflict):
		h.fail(w, r, 409, "staff_exists")
	default:
		h.unavailable(w, r, operation, err)
	}
}

type staffCursor struct {
	Account publisher.ID      `json:"a"`
	ID      publisher.StaffID `json:"s"`
}

func (h *Handler) listStaff(w http.ResponseWriter, r *http.Request, operator bool) {
	manager, ok := h.staffManager(w, r, operator)
	if !ok {
		return
	}
	store, ok := h.cfg.Store.(StaffManagementStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	account := publisher.ID(r.PathValue("account"))
	value, err := urlQuery(r)
	if err != nil {
		h.fail(w, r, 400, "invalid_cursor")
		return
	}
	var after publisher.StaffID
	if value != "" {
		data, err := base64.RawURLEncoding.DecodeString(value)
		var c staffCursor
		if err != nil || json.Unmarshal(data, &c) != nil || c.Account != account || !c.ID.Valid() {
			h.fail(w, r, 400, "invalid_cursor")
			return
		}
		after = c.ID
	}
	rows, err := store.ListStaff(r.Context(), manager, account, after, 21)
	if err != nil {
		h.staffManagementError(w, r, "staff_list", err)
		return
	}
	cursor := ""
	if len(rows) > 20 {
		rows = rows[:20]
		data, _ := json.Marshal(staffCursor{account, rows[19].ID})
		cursor = base64.RawURLEncoding.EncodeToString(data)
	}
	views := make([]staffView, 0, len(rows))
	for _, row := range rows {
		views = append(views, staffPublic(row))
	}
	h.write(w, r, 200, map[string]any{"staff": views, "next_cursor": cursor})
}
func (h *Handler) createStaff(w http.ResponseWriter, r *http.Request, operator bool) {
	manager, ok := h.staffManager(w, r, operator)
	if !ok {
		return
	}
	store, ok := h.cfg.Store.(StaffManagementStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	var input struct {
		Username string         `json:"username"`
		Password string         `json:"password"`
		Role     publisher.Role `json:"role"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	account := publisher.ID(r.PathValue("account"))
	if !validUsername(normalizeUsername(input.Username)) || !validPassword(input.Password) || (input.Role != publisher.Administrator && input.Role != publisher.Editor) {
		h.fail(w, r, 400, "invalid_staff")
		return
	}
	if !h.acquireHash(w, r, "staff-create:"+string(account)) {
		return
	}
	defer func() { <-h.hashes }()
	row, err := newStaff(account, input.Username, input.Password, input.Role)
	if err != nil {
		h.unavailable(w, r, "staff_prepare", err)
		return
	}
	if err := store.CreateStaff(r.Context(), manager, account, row); err != nil {
		h.staffManagementError(w, r, "staff_create", err)
		return
	}
	h.logStaffChange(r, manager, account, row.Staff, "staff.create")
	h.write(w, r, 201, map[string]any{"staff": staffPublic(row.Staff)})
}
func (h *Handler) updateStaff(w http.ResponseWriter, r *http.Request, operator bool) {
	manager, ok := h.staffManager(w, r, operator)
	if !ok {
		return
	}
	store, ok := h.cfg.Store.(StaffManagementStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	id := publisher.StaffID(r.PathValue("staff"))
	if !id.Valid() {
		h.fail(w, r, 404, "not_found")
		return
	}
	var input struct {
		Role     publisher.Role `json:"role"`
		Disabled *bool          `json:"disabled"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	if input.Disabled == nil || (input.Role != publisher.Administrator && input.Role != publisher.Editor) {
		h.fail(w, r, 400, "invalid_staff")
		return
	}
	account := publisher.ID(r.PathValue("account"))
	row, err := store.UpdateStaff(r.Context(), manager, account, id, input.Role, *input.Disabled)
	if err != nil {
		h.staffManagementError(w, r, "staff_update", err)
		return
	}
	h.logStaffChange(r, manager, account, row.Staff, "staff.update")
	h.write(w, r, 200, map[string]any{"staff": staffPublic(row.Staff)})
}
func (h *Handler) logStaffChange(r *http.Request, m StaffManager, account publisher.ID, staff publisher.StaffAccount, operation string) {
	kind, actor := "staff", string(m.Principal.StaffAccountID)
	if m.OperatorID != "" {
		kind, actor = "operator", m.OperatorID
	}
	h.cfg.Logger.InfoContext(r.Context(), "staff account changed", "operation", operation, "account_id", account, "staff_id", staff.ID, "role", staff.Role, "disabled", staff.Disabled, "actor_kind", kind, "actor_id", actor)
}
