package community

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testHandler() (*Handler, *MemoryStore) {
	store := NewMemoryStore()
	return NewHandler(Config{Store: store, ClipIDs: map[string]bool{"basketball": true, "football": true}, SecureCookies: true,
		Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}), store
}
func call(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	r := httptest.NewRequest(method, "https://rangle.test"+path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	recorder := httptest.NewRecorder()
	h.ServeHTTP(recorder, r)
	response := recorder.Result()
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}
func registerUser(t *testing.T, h *Handler, id string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": id, "password": "a long test password 2026", "name": "テスト " + id})
	r, data := call(t, h, "POST", "/api/v1/auth/register", string(body), nil, nil)
	if r.StatusCode != 201 || len(r.Cookies()) != 1 {
		t.Fatalf("register: %d %s", r.StatusCode, data)
	}
	return r.Cookies()[0]
}

func TestAccountSessionLifecycle(t *testing.T) {
	h, store := testHandler()
	cookie := registerUser(t, h, "Hosi121")
	if !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("insecure cookie: %+v", cookie)
	}
	a, err := store.Account(context.Background(), "hosi121")
	if err != nil || len(a.Salt) != 16 || len(a.Hash) != 32 || bytes.Contains(a.Hash, []byte("password")) {
		t.Fatal("password was not stored as a salted hash")
	}
	if _, ok := store.sessions[cookie.Value]; ok {
		t.Fatal("bearer cookie stored in plaintext")
	}
	r, data := call(t, h, "GET", "/api/v1/auth/me", "", cookie, nil)
	if r.StatusCode != 200 || !bytes.Contains(data, []byte(`"id":"hosi121"`)) || bytes.Contains(data, []byte("hash")) || bytes.Contains(data, []byte("salt")) || r.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("me: %d %s", r.StatusCode, data)
	}
	r, _ = call(t, h, "POST", "/api/v1/auth/logout", "", cookie, nil)
	if r.StatusCode != 204 || r.Cookies()[0].MaxAge != -1 {
		t.Fatal("logout did not expire the cookie")
	}
	_, data = call(t, h, "GET", "/api/v1/auth/me", "", cookie, nil)
	if !bytes.Contains(data, []byte(`"user":null`)) {
		t.Fatal("logged-out session could be replayed")
	}
	r, data = call(t, h, "POST", "/api/v1/auth/login", `{"username":"hosi121","password":"a long test password 2026"}`, nil, nil)
	if r.StatusCode != 200 || r.Cookies()[0].Value == cookie.Value {
		t.Fatalf("login did not rotate session: %d %s", r.StatusCode, data)
	}
	cookie = r.Cookies()[0]
	s := store.sessions[sessionKey(cookie.Value)]
	s.ExpiresAt = time.Now().Add(-time.Second)
	store.sessions[sessionKey(cookie.Value)] = s
	r, _ = call(t, h, "PUT", "/api/v1/clips/basketball/likes", "", cookie, nil)
	if r.StatusCode != 401 {
		t.Fatal("expired session authorized a write")
	}
	var first []byte
	for _, id := range []string{"hosi121", "nobody"} {
		r, data = call(t, h, "POST", "/api/v1/auth/login", `{"username":"`+id+`","password":"incorrect"}`, nil, nil)
		if r.StatusCode != 401 {
			t.Fatal("bad credentials accepted")
		}
		if first == nil {
			first = data
		} else if !bytes.Equal(first, data) {
			t.Fatal("login discloses account existence")
		}
	}
}

func TestSocialWritesAreAuthenticatedIdempotentAndOwned(t *testing.T) {
	h, _ := testHandler()
	owner := registerUser(t, h, "owner")
	other := registerUser(t, h, "other")
	r, _ := call(t, h, "PUT", "/api/v1/clips/basketball/likes", "", nil, nil)
	if r.StatusCode != 401 {
		t.Fatal("anonymous like accepted")
	}
	for range 2 {
		r, _ = call(t, h, "PUT", "/api/v1/clips/basketball/likes", "", owner, nil)
		if r.StatusCode != 204 {
			t.Fatal(r.StatusCode)
		}
	}
	_, data := call(t, h, "GET", "/api/v1/clips/basketball/engagement", "", owner, nil)
	if !bytes.Contains(data, []byte(`"likes":1`)) || !bytes.Contains(data, []byte(`"liked":true`)) {
		t.Fatalf("duplicate like inflated count: %s", data)
	}
	body := `{"text":"見たい角度に動かせた！ <script>text only</script>","request_id":"0123456789abcdef0123456789abcdef"}`
	var c Comment
	for range 2 {
		r, data = call(t, h, "POST", "/api/v1/clips/basketball/comments", body, owner, nil)
		if r.StatusCode != 201 || json.Unmarshal(data, &c) != nil {
			t.Fatalf("comment: %d %s", r.StatusCode, data)
		}
	}
	_, data = call(t, h, "GET", "/api/v1/clips/basketball/engagement", "", nil, nil)
	if !bytes.Contains(data, []byte(`"comments":1`)) {
		t.Fatalf("retry duplicated comment: %s", data)
	}
	r, _ = call(t, h, "DELETE", "/api/v1/clips/basketball/comments/"+c.ID, "", other, nil)
	if r.StatusCode != 403 {
		t.Fatal("another user deleted comment")
	}
	r, _ = call(t, h, "DELETE", "/api/v1/clips/basketball/comments/"+c.ID, "", owner, nil)
	if r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	r, _ = call(t, h, "DELETE", "/api/v1/clips/basketball/likes", "", owner, nil)
	if r.StatusCode != 204 {
		t.Fatal(r.StatusCode)
	}
	_, data = call(t, h, "GET", "/api/v1/clips/basketball/engagement", "", owner, nil)
	if !bytes.Contains(data, []byte(`"likes":0`)) || !bytes.Contains(data, []byte(`"comments":0`)) {
		t.Fatalf("delete did not update counts: %s", data)
	}
	r, _ = call(t, h, "PUT", "/api/v1/clips/unknown/likes", "", owner, nil)
	if r.StatusCode != 404 {
		t.Fatal("unknown clip accepted")
	}
}

func TestCrossOriginAndInputBounds(t *testing.T) {
	h, _ := testHandler()
	cookie := registerUser(t, h, "owner")
	for _, headers := range []map[string]string{{"Origin": "https://evil.example"}, {"Sec-Fetch-Site": "cross-site"}} {
		for _, route := range []struct{ method, path string }{{"POST", "/api/v1/auth/logout"}, {"PUT", "/api/v1/clips/basketball/likes"}, {"DELETE", "/api/v1/clips/basketball/likes"}} {
			r, _ := call(t, h, route.method, route.path, "", cookie, headers)
			if r.StatusCode != 403 {
				t.Fatalf("cross-origin %s accepted", route.path)
			}
		}
	}
	for _, body := range []string{`{"username":"abc","password":"short"}`, `{"username":"abc","password":"a long test password 2026","admin":true}`, strings.Repeat("x", 5000)} {
		r, _ := call(t, h, "POST", "/api/v1/auth/register", body, nil, nil)
		if r.StatusCode != 400 {
			t.Fatalf("invalid body accepted: %d", r.StatusCode)
		}
	}
	r, _ := call(t, h, "POST", "/api/v1/auth/login", `{}`, nil, map[string]string{"Content-Type": "text/plain"})
	if r.StatusCode != 415 {
		t.Fatal("non-JSON credentials accepted")
	}
	h.hashes <- struct{}{}
	h.hashes <- struct{}{}
	r, _ = call(t, h, "POST", "/api/v1/auth/login", `{"username":"owner","password":"a long test password 2026"}`, nil, nil)
	if r.StatusCode != 429 || r.Header.Get("Retry-After") == "" {
		t.Fatal("unbounded password hashing")
	}
}

func TestCommentPaginationAndCursorBinding(t *testing.T) {
	h, store := testHandler()
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 25; i++ {
		id := strings.Repeat("0", 30) + string("0123456789abcdef"[i/16]) + string("0123456789abcdef"[i%16])
		_, err := store.AddComment(context.Background(), "basketball", Comment{ID: id, Author: User{ID: "owner", Name: "Owner"}, Text: "hello", CreatedAt: now.Add(time.Duration(i) * time.Microsecond)})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, data := call(t, h, "GET", "/api/v1/clips/basketball/comments", "", nil, nil)
	var page struct {
		Comments []Comment `json:"comments"`
		Next     string    `json:"next_cursor"`
	}
	if json.Unmarshal(data, &page) != nil || len(page.Comments) != 20 || page.Next == "" {
		t.Fatalf("first page: %s", data)
	}
	cursor := page.Next
	_, data = call(t, h, "GET", "/api/v1/clips/basketball/comments?cursor="+cursor, "", nil, nil)
	if json.Unmarshal(data, &page) != nil || len(page.Comments) != 5 || page.Next != "" {
		t.Fatalf("second page: %s", data)
	}
	r, _ := call(t, h, "GET", "/api/v1/clips/football/comments?cursor="+cursor, "", nil, nil)
	if r.StatusCode != 400 {
		t.Fatal("cursor from another clip accepted")
	}
}
