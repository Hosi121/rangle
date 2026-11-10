package community

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Hosi121/rangle/internal/publisher"
)

const staffCookieName = "rangle_staff_session"

type StaffIdentity struct {
	Account   publisher.Account
	Record    StaffRecord
	Principal publisher.StaffPrincipal
}
type staffView struct {
	ID       publisher.StaffID `json:"id"`
	Username string            `json:"username"`
	Role     publisher.Role    `json:"role"`
	Disabled bool              `json:"disabled"`
}

func staffPublic(s publisher.StaffAccount) staffView {
	return staffView{s.ID, s.Username, s.Role, s.Disabled}
}
func newStaff(account publisher.ID, username, password string, role publisher.Role) (StaffRecord, error) {
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return StaffRecord{}, err
	}
	row := StaffRecord{Staff: publisher.StaffAccount{ID: publisher.StaffID(hex.EncodeToString(id)), PublisherAccountID: account, Username: normalizeUsername(username), Role: role}, Version: 1}
	if row.Staff.Validate() != nil || !validPassword(password) {
		return row, fmt.Errorf("invalid staff credentials")
	}
	crypt := newAccount(row.Staff.Username, "", password)
	row.Salt, row.Hash, row.HashVersion = crypt.Salt, crypt.Hash, crypt.HashVersion
	return row, nil
}
func staffPassword(s StaffRecord) Account {
	return Account{Salt: s.Salt, Hash: s.Hash, HashVersion: s.HashVersion}
}
func (h *Handler) staffCookie(value string, maxAge int) *http.Cookie {
	c := h.cookie(value, maxAge)
	c.Name = staffCookieName
	return c
}

// CurrentStaff loads the current owning organization and staff record on every
// request. A viewer/operator cookie cannot substitute for a staff session.
func (h *Handler) CurrentStaff(r *http.Request) (*StaffIdentity, error) {
	store, ok := h.cfg.Store.(PublisherStore)
	if !ok {
		return nil, nil
	}
	cookie, err := r.Cookie(staffCookieName)
	if err != nil {
		return nil, nil
	}
	key := sessionKey(cookie.Value)
	if key == "" {
		return nil, nil
	}
	session, err := store.StaffSession(r.Context(), key)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !session.ExpiresAt.After(time.Now()) || session.Version < 1 || !session.Principal.PublisherAccountID.Valid() || !session.Principal.StaffAccountID.Valid() {
		return nil, nil
	}
	account, err := store.Publisher(r.Context(), session.Principal.PublisherAccountID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	record, err := store.Staff(r.Context(), account.ID, session.Principal.StaffAccountID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if account.Status != publisher.Active || record.Staff.Disabled || !record.valid() || record.Version != session.Version || record.Staff.PublisherAccountID != account.ID || record.Staff.ID != session.Principal.StaffAccountID {
		return nil, nil
	}
	return &StaffIdentity{account, record, session.Principal}, nil
}
func (h *Handler) staffMe(w http.ResponseWriter, r *http.Request) {
	identity, err := h.CurrentStaff(r)
	if err != nil {
		h.unavailable(w, r, "staff_session", err)
		return
	}
	var view any
	if identity != nil {
		view = map[string]any{"account": identity.Account, "staff": staffPublic(identity.Record.Staff)}
	}
	_, available := h.cfg.Store.(PublisherStore)
	h.write(w, r, 200, map[string]any{"identity": view, "available": available})
}
func (h *Handler) staffLogin(w http.ResponseWriter, r *http.Request) {
	store, ok := h.cfg.Store.(PublisherStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	var input struct {
		Account  string `json:"account_id"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	accountID := publisher.ID(strings.ToLower(strings.TrimSpace(input.Account)))
	username := normalizeUsername(input.Username)
	if !accountID.Valid() || !validUsername(username) || len(input.Password) > 512 {
		h.fail(w, r, 401, "invalid_credentials")
		return
	}
	if !h.acquireHash(w, r, "staff:"+string(accountID)+":"+username) {
		return
	}
	defer func() { <-h.hashes }()
	account, accountErr := store.Publisher(r.Context(), accountID)
	if accountErr != nil && !errors.Is(accountErr, ErrNotFound) {
		h.unavailable(w, r, "staff_account", accountErr)
		return
	}
	record, err := store.StaffByUsername(r.Context(), accountID, username)
	if err != nil && !errors.Is(err, ErrNotFound) {
		h.unavailable(w, r, "staff_credentials", err)
		return
	}
	crypt := staffPassword(record)
	if err != nil || accountErr != nil {
		crypt = h.dummy
	}
	valid := checkPassword(crypt, input.Password)
	if !valid || err != nil || accountErr != nil || account.Status != publisher.Active || record.Staff.Disabled || !record.valid() {
		h.fail(w, r, 401, "invalid_credentials")
		return
	}
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		h.unavailable(w, r, "staff_session_random", err)
		return
	}
	token := base64.RawURLEncoding.EncodeToString(data)
	session := StaffSession{Principal: publisher.StaffPrincipal{PublisherAccountID: account.ID, StaffAccountID: record.Staff.ID}, Version: record.Version, ExpiresAt: time.Now().UTC().Add(7 * 24 * time.Hour)}
	if err := store.PutStaffSession(r.Context(), sessionKey(token), session); err != nil {
		h.unavailable(w, r, "staff_session_create", err)
		return
	}
	if previous, err := r.Cookie(staffCookieName); err == nil && sessionKey(previous.Value) != "" {
		if err := store.DeleteStaffSession(r.Context(), sessionKey(previous.Value)); err != nil {
			_ = store.DeleteStaffSession(r.Context(), sessionKey(token))
			h.unavailable(w, r, "staff_session_rotate", err)
			return
		}
	}
	http.SetCookie(w, h.staffCookie(token, 7*24*60*60))
	h.write(w, r, 200, map[string]any{"identity": map[string]any{"account": account, "staff": staffPublic(record.Staff)}})
}
func (h *Handler) staffLogout(w http.ResponseWriter, r *http.Request) {
	store, ok := h.cfg.Store.(PublisherStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	if cookie, err := r.Cookie(staffCookieName); err == nil && sessionKey(cookie.Value) != "" {
		if err := store.DeleteStaffSession(r.Context(), sessionKey(cookie.Value)); err != nil {
			h.unavailable(w, r, "staff_logout", err)
			return
		}
	}
	http.SetCookie(w, h.staffCookie("", -1))
	w.WriteHeader(204)
}
func (h *Handler) provisionPublisher(w http.ResponseWriter, r *http.Request) {
	operator, err := h.AdminUser(r)
	if errors.Is(err, ErrForbidden) {
		h.fail(w, r, 403, "operator_required")
		return
	}
	if err != nil {
		h.unavailable(w, r, "publisher_operator", err)
		return
	}
	if operator == nil {
		h.fail(w, r, 401, "not_authenticated")
		return
	}
	store, ok := h.cfg.Store.(PublisherStore)
	if !ok {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	var input struct {
		ID       publisher.ID `json:"id"`
		Name     string       `json:"name"`
		Kind     string       `json:"kind"`
		Username string       `json:"staff_username"`
		Password string       `json:"staff_password"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	account := publisher.Account{ID: input.ID, Name: strings.TrimSpace(input.Name), Kind: input.Kind, Status: publisher.Active}
	if account.Validate() != nil || !validUsername(normalizeUsername(input.Username)) || !validPassword(input.Password) {
		h.fail(w, r, 400, "invalid_publisher")
		return
	}
	if !h.acquireHash(w, r, "staff-create:"+string(input.ID)) {
		return
	}
	defer func() { <-h.hashes }()
	record, err := newStaff(account.ID, input.Username, input.Password, publisher.Administrator)
	if err != nil {
		h.unavailable(w, r, "staff_prepare", err)
		return
	}
	err = store.ProvisionPublisher(r.Context(), account, record)
	if errors.Is(err, ErrConflict) {
		h.fail(w, r, 409, "publisher_exists")
		return
	}
	if err != nil {
		h.unavailable(w, r, "publisher_provision", err)
		return
	}
	h.cfg.Logger.InfoContext(r.Context(), "publisher provisioned", "account_id", account.ID, "operator_id", operator.ID, "staff_id", record.Staff.ID)
	h.write(w, r, 201, map[string]any{"account": account, "staff": staffPublic(record.Staff)})
}
