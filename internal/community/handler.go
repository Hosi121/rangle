package community

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/time/rate"
)

const cookieName = "rangle_session"

// One shared budget bounds public read admission before any dependency work.
// These are initial guards for the presentation instance, not measured capacity.
const publicReadInterval = 100 * time.Millisecond
const publicReadBurst = 20

type Config struct {
	Store         Store
	ClipIDs       map[string]bool
	ClipExists    func(context.Context, string) (bool, error)
	DescribeClip  func(context.Context, string) (HistoryClip, error)
	SecureCookies bool
	Logger        *slog.Logger
}
type Handler struct {
	cfg      Config
	routes   http.Handler
	hashes   chan struct{}
	authRate *rate.Limiter
	readRate *rate.Limiter
	mu       sync.Mutex
	limits   map[string]*attemptLimit
	dummy    Account
}
type attemptLimit struct {
	limiter *rate.Limiter
	used    time.Time
}

func NewHandler(cfg Config) *Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	h := &Handler{
		cfg:      cfg,
		hashes:   make(chan struct{}, 2),
		authRate: rate.NewLimiter(5, 10),
		readRate: rate.NewLimiter(rate.Every(publicReadInterval), publicReadBurst),
		limits:   map[string]*attemptLimit{},
		dummy:    newAccount("dummy", "dummy", "not-a-real-user-password"),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/auth/me", h.me)
	mux.HandleFunc("POST /api/v1/operations/publishers", h.provisionPublisher)
	mux.HandleFunc("GET /api/v1/operations/publishers", h.listPublishers)
	mux.HandleFunc("GET /api/v1/publisher-auth/me", h.staffMe)
	mux.HandleFunc("POST /api/v1/publisher-auth/login", h.staffLogin)
	mux.HandleFunc("POST /api/v1/publisher-auth/logout", h.staffLogout)
	mux.HandleFunc("PUT /api/v1/publisher-auth/password", h.changeStaffPassword)
	mux.HandleFunc("GET /api/v1/publishers/{account}/deletion-request", h.publisherDeletionRequest)
	mux.HandleFunc("PUT /api/v1/publishers/{account}/deletion-request", h.publisherDeletionRequest)
	for _, prefix := range []string{"/api/v1/publishers/", "/api/v1/operations/publishers/"} {
		operator := prefix == "/api/v1/operations/publishers/"
		mux.HandleFunc("GET "+prefix+"{account}/staff", func(w http.ResponseWriter, r *http.Request) { h.listStaff(w, r, operator) })
		mux.HandleFunc("POST "+prefix+"{account}/staff", func(w http.ResponseWriter, r *http.Request) { h.createStaff(w, r, operator) })
		mux.HandleFunc("PUT "+prefix+"{account}/staff/{staff}", func(w http.ResponseWriter, r *http.Request) { h.updateStaff(w, r, operator) })
		mux.HandleFunc("PUT "+prefix+"{account}/staff/{staff}/password", func(w http.ResponseWriter, r *http.Request) { h.resetStaffPassword(w, r, operator) })
	}
	mux.HandleFunc("GET /api/v1/me/cheered-clips", h.cheerHistory)
	mux.HandleFunc("GET /api/v1/publishers/{account}", h.limitPublicRead(h.publicPublisher))
	mux.HandleFunc("GET /api/v1/publishers/{account}/subscription", h.limitPublicRead(h.publisherSubscription))
	mux.HandleFunc("PUT /api/v1/publishers/{account}/subscription", h.publisherSubscription)
	mux.HandleFunc("DELETE /api/v1/publishers/{account}/subscription", h.publisherSubscription)
	mux.HandleFunc("GET /api/v1/me/subscriptions", h.limitPublicRead(h.publisherSubscriptions))
	mux.HandleFunc("POST /api/v1/auth/register", h.register)
	mux.HandleFunc("POST /api/v1/auth/login", h.login)
	mux.HandleFunc("POST /api/v1/auth/logout", h.logout)
	mux.HandleFunc("GET /api/v1/clips/{clip}/engagement", h.limitPublicRead(h.engagement))
	mux.HandleFunc("GET /api/v1/clips/{clip}/cheers", h.limitPublicRead(h.cheers))
	mux.HandleFunc("POST /api/v1/clips/{clip}/cheers", h.cheers)
	mux.HandleFunc("PUT /api/v1/clips/{clip}/likes", h.like)
	mux.HandleFunc("DELETE /api/v1/clips/{clip}/likes", h.like)
	mux.HandleFunc("GET /api/v1/clips/{clip}/comments", h.limitPublicRead(h.comments))
	mux.HandleFunc("POST /api/v1/clips/{clip}/comments", h.comment)
	mux.HandleFunc("DELETE /api/v1/clips/{clip}/comments/{comment}", h.deleteComment)
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.fail(w, r, 403, "cross_origin_request") }))
	h.routes = protection.Handler(mux)
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	if h.cfg.Store == nil && r.URL.Path != "/api/v1/auth/me" && r.URL.Path != "/api/v1/publisher-auth/me" {
		h.fail(w, r, 503, "service_unavailable")
		return
	}
	h.routes.ServeHTTP(w, r)
}

func (h *Handler) limitPublicRead(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// GET patterns also match HEAD. Share a single budget across routes,
		// clip IDs and cookies without looking up a clip or session first.
		if !h.readRate.Allow() {
			w.Header().Set("Retry-After", "1")
			h.fail(w, r, http.StatusTooManyRequests, "rate_limited")
			return
		}
		next(w, r)
	}
}

func (h *Handler) write(w http.ResponseWriter, r *http.Request, status int, value any) {
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_ = json.NewEncoder(w).Encode(value)
	}
}
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, status int, code string) {
	h.write(w, r, status, map[string]string{"error": code})
}
func (h *Handler) unavailable(w http.ResponseWriter, r *http.Request, op string, err error) {
	h.cfg.Logger.Error("community request failed", "operation", op, "error", err)
	h.fail(w, r, 503, "service_unavailable")
}
func (h *Handler) decode(w http.ResponseWriter, r *http.Request, target any) bool {
	typeName, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || typeName != "application/json" {
		h.fail(w, r, 415, "json_required")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		h.fail(w, r, 400, "invalid_request")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		h.fail(w, r, 400, "invalid_request")
		return false
	}
	return true
}
func (h *Handler) allow(key string, every time.Duration, burst int) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	entry := h.limits[key]
	if entry == nil {
		if len(h.limits) >= 4096 {
			for id, value := range h.limits {
				if now.Sub(value.used) > 5*time.Minute {
					delete(h.limits, id)
				}
			}
			if len(h.limits) >= 4096 {
				return false
			}
		}
		entry = &attemptLimit{limiter: rate.NewLimiter(rate.Every(every), burst)}
		h.limits[key] = entry
	}
	entry.used = now
	return entry.limiter.Allow()
}
func (h *Handler) acquireHash(w http.ResponseWriter, r *http.Request, id string) bool {
	if !h.authRate.Allow() || !h.allow("auth:"+id, 6*time.Second, 5) {
		w.Header().Set("Retry-After", "6")
		h.fail(w, r, 429, "rate_limited")
		return false
	}
	select {
	case h.hashes <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		h.fail(w, r, 429, "rate_limited")
		return false
	}
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	id := normalizeUsername(input.Username)
	if id == "admin" {
		h.fail(w, r, 409, "username_unavailable")
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = id
	}
	if !validUsername(id) || !validPassword(input.Password) || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 40 {
		h.fail(w, r, 400, "invalid_account")
		return
	}
	if !h.acquireHash(w, r, id) {
		return
	}
	defer func() { <-h.hashes }()
	a := newAccount(id, name, input.Password)
	if err := h.cfg.Store.CreateAccount(r.Context(), a); err != nil {
		if errors.Is(err, ErrConflict) {
			h.fail(w, r, 409, "username_unavailable")
		} else {
			h.unavailable(w, r, "register", err)
		}
		return
	}
	h.startSession(w, r, a.User, 201)
}
func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	id := normalizeUsername(input.Username)
	if !validUsername(id) || len(input.Password) > 512 {
		h.fail(w, r, 401, "invalid_credentials")
		return
	}
	if !h.acquireHash(w, r, id) {
		return
	}
	defer func() { <-h.hashes }()
	a, err := h.cfg.Store.Account(r.Context(), id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		h.unavailable(w, r, "login", err)
		return
	}
	if errors.Is(err, ErrNotFound) {
		a = h.dummy
	}
	valid := checkPassword(a, input.Password)
	if err != nil || !valid {
		h.fail(w, r, 401, "invalid_credentials")
		return
	}
	h.startSession(w, r, a.User, 200)
}
func sessionKey(value string) string {
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) != 32 {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func (h *Handler) cookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true, Secure: h.cfg.SecureCookies, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}
}
func (h *Handler) startSession(w http.ResponseWriter, r *http.Request, user User, status int) {
	data := make([]byte, 32)
	_, _ = rand.Read(data)
	token := base64.RawURLEncoding.EncodeToString(data)
	if err := h.cfg.Store.PutSession(r.Context(), sessionKey(token), Session{user, time.Now().UTC().Add(7 * 24 * time.Hour)}); err != nil {
		h.unavailable(w, r, "create_session", err)
		return
	}
	if old, err := r.Cookie(cookieName); err == nil && sessionKey(old.Value) != "" {
		if err := h.cfg.Store.DeleteSession(r.Context(), sessionKey(old.Value)); err != nil {
			h.unavailable(w, r, "rotate_session", err)
			return
		}
	}
	http.SetCookie(w, h.cookie(token, 7*24*60*60))
	h.write(w, r, status, map[string]any{"user": user})
}

// SessionUser validates the same opaque, expiring session used by community
// actions. Other authenticated APIs reuse it without handling cookie secrets.
func (h *Handler) SessionUser(r *http.Request) (*User, error) {
	if h.cfg.Store == nil {
		return nil, nil
	}
	cookie, err := r.Cookie(cookieName)
	if err != nil {
		return nil, nil
	}
	key := sessionKey(cookie.Value)
	if key == "" {
		return nil, nil
	}
	s, err := h.cfg.Store.Session(r.Context(), key)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !s.ExpiresAt.After(time.Now()) {
		return nil, nil
	}
	return &s.User, nil
}
func (h *Handler) requireUser(w http.ResponseWriter, r *http.Request) *User {
	user, err := h.SessionUser(r)
	if err != nil {
		h.unavailable(w, r, "session", err)
		return nil
	}
	if user == nil {
		h.fail(w, r, 401, "not_authenticated")
		return nil
	}
	if !h.allow("write:"+user.ID, 2*time.Second, 10) {
		w.Header().Set("Retry-After", "2")
		h.fail(w, r, 429, "rate_limited")
		return nil
	}
	return user
}

// AccountUser refreshes privileges from the account instead of a session role.
func (h *Handler) AccountUser(r *http.Request) (*User, error) {
	user, err := h.SessionUser(r)
	if err != nil || user == nil {
		return user, err
	}
	a, err := h.cfg.Store.Account(r.Context(), user.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &a.User, nil
}

// AdminUser is the legacy authorization entry point for rangle operators.
// It refreshes the stored platform role and never authorizes organization staff.
func (h *Handler) AdminUser(r *http.Request) (*User, error) {
	user, err := h.AccountUser(r)
	if err == nil && user != nil && !user.Admin {
		return nil, ErrForbidden
	}
	return user, err
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	user, err := h.AccountUser(r)
	if err != nil {
		h.unavailable(w, r, "session", err)
		return
	}
	h.write(w, r, 200, map[string]any{"user": user, "available": h.cfg.Store != nil, "min_password_length": MinPasswordLength})
}
func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(cookieName); err == nil && sessionKey(cookie.Value) != "" {
		if err := h.cfg.Store.DeleteSession(r.Context(), sessionKey(cookie.Value)); err != nil {
			h.unavailable(w, r, "logout", err)
			return
		}
	}
	http.SetCookie(w, h.cookie("", -1))
	w.WriteHeader(204)
}
func (h *Handler) clip(w http.ResponseWriter, r *http.Request) string {
	id := r.PathValue("clip")
	exists := h.cfg.ClipIDs[id]
	if !exists && h.cfg.ClipExists != nil {
		var err error
		exists, err = h.cfg.ClipExists(r.Context(), id)
		if err != nil {
			h.unavailable(w, r, "clip", err)
			return ""
		}
	}
	if !exists {
		h.fail(w, r, 404, "clip_not_found")
		return ""
	}
	return id
}
func (h *Handler) engagement(w http.ResponseWriter, r *http.Request) {
	clip := h.clip(w, r)
	if clip == "" {
		return
	}
	user, err := h.SessionUser(r)
	if err != nil {
		h.unavailable(w, r, "session", err)
		return
	}
	counts, err := h.cfg.Store.Counts(r.Context(), clip)
	if err != nil {
		h.unavailable(w, r, "counts", err)
		return
	}
	liked := false
	if user != nil {
		liked, err = h.cfg.Store.Liked(r.Context(), clip, user.ID)
		if err != nil {
			h.unavailable(w, r, "liked", err)
			return
		}
	}
	h.write(w, r, 200, map[string]any{"likes": counts.Likes, "comments": counts.Comments, "liked": liked})
}
func (h *Handler) like(w http.ResponseWriter, r *http.Request) {
	clip := h.clip(w, r)
	if clip == "" {
		return
	}
	user := h.requireUser(w, r)
	if user == nil {
		return
	}
	if err := h.cfg.Store.SetLike(r.Context(), clip, user.ID, r.Method == http.MethodPut); err != nil {
		h.unavailable(w, r, "like", err)
		return
	}
	w.WriteHeader(204)
}
func (h *Handler) comment(w http.ResponseWriter, r *http.Request) {
	clip := h.clip(w, r)
	if clip == "" {
		return
	}
	user := h.requireUser(w, r)
	if user == nil {
		return
	}
	var input struct {
		Text      string `json:"text"`
		RequestID string `json:"request_id"`
	}
	if !h.decode(w, r, &input) {
		return
	}
	text := strings.TrimSpace(input.Text)
	if text == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > 500 || !validHexID(input.RequestID) {
		h.fail(w, r, 400, "invalid_comment")
		return
	}
	for _, c := range text {
		if unicode.IsControl(c) && c != '\n' && c != '\r' && c != '\t' {
			h.fail(w, r, 400, "invalid_comment")
			return
		}
	}
	sum := sha256.Sum256([]byte(user.ID + "\n" + input.RequestID))
	c := Comment{ID: hex.EncodeToString(sum[:16]), Author: *user, Text: text, CreatedAt: time.Now().UTC().Truncate(time.Microsecond)}
	c, err := h.cfg.Store.AddComment(r.Context(), clip, c)
	if errors.Is(err, ErrConflict) {
		h.fail(w, r, 409, "request_conflict")
		return
	}
	if err != nil {
		h.unavailable(w, r, "comment", err)
		return
	}
	h.write(w, r, 201, c)
}
func validHexID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil && strings.ToLower(id) == id
}
func (h *Handler) comments(w http.ResponseWriter, r *http.Request) {
	clip := h.clip(w, r)
	if clip == "" {
		return
	}
	q, err := urlQuery(r)
	if err != nil {
		h.fail(w, r, 400, "invalid_cursor")
		return
	}
	var after *CommentCursor
	if value := q; value != "" {
		data, err := base64.RawURLEncoding.DecodeString(value)
		var cursor CommentCursor
		if err != nil || len(value) > 256 || json.Unmarshal(data, &cursor) != nil || cursor.Clip != clip || !validHexID(cursor.ID) || cursor.CreatedAt.IsZero() {
			h.fail(w, r, 400, "invalid_cursor")
			return
		}
		after = &cursor
	}
	rows, err := h.cfg.Store.Comments(r.Context(), clip, after, 21)
	if err != nil {
		h.unavailable(w, r, "comments", err)
		return
	}
	cursor := ""
	if len(rows) > 20 {
		rows = rows[:20]
		last := rows[len(rows)-1]
		data, _ := json.Marshal(CommentCursor{clip, last.ID, last.CreatedAt})
		cursor = base64.RawURLEncoding.EncodeToString(data)
	}
	if rows == nil {
		rows = []Comment{}
	}
	h.write(w, r, 200, map[string]any{"comments": rows, "next_cursor": cursor})
}
func urlQuery(r *http.Request) (string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", err
	}
	for key, values := range q {
		if key != "cursor" || len(values) != 1 || len(values[0]) > 256 {
			return "", errors.New("invalid cursor")
		}
	}
	return q.Get("cursor"), nil
}
func (h *Handler) deleteComment(w http.ResponseWriter, r *http.Request) {
	clip := h.clip(w, r)
	if clip == "" {
		return
	}
	user := h.requireUser(w, r)
	if user == nil {
		return
	}
	id := r.PathValue("comment")
	if !validHexID(id) {
		h.fail(w, r, 404, "comment_not_found")
		return
	}
	err := h.cfg.Store.DeleteComment(r.Context(), clip, id, user.ID)
	switch {
	case errors.Is(err, ErrNotFound):
		h.fail(w, r, 404, "comment_not_found")
	case errors.Is(err, ErrForbidden):
		h.fail(w, r, 403, "forbidden")
	case err != nil:
		h.unavailable(w, r, "delete_comment", err)
	default:
		w.WriteHeader(204)
	}
}
