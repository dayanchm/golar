package auth_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dayanchm/golar/auth"
	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/orm"
	"github.com/dayanchm/golar/database/schema"
	"github.com/dayanchm/golar/http/session"
)

type User struct {
	orm.Model
	Email           string
	Password        string
	EmailVerifiedAt *time.Time
	Roles           string
}

func (User) TableName() string       { return "auth_users" }
func (u *User) AuthID() string       { return strconv.FormatInt(u.ID, 10) }
func (u *User) AuthPassword() string { return u.Password }
func (u *User) AuthEmail() string    { return u.Email }
func (u *User) EmailVerified() bool  { return u.EmailVerifiedAt != nil }
func (u *User) AuthRoles() []string  { return strings.Split(u.Roles, ",") }

func TestMain(m *testing.M) {
	// Cheap parameters keep the suite fast; production uses the defaults.
	auth.DefaultHasher = auth.Passwords{Argon2id: auth.Argon2id{Memory: 64, Iterations: 1}}
	os.Exit(m.Run())
}

type env struct {
	db       *database.DB
	provider *auth.ORMProvider[User]
	ada      *User
}

func setup(t *testing.T) *env {
	t.Helper()
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(t.TempDir(), "auth.db")}
	if driver != "sqlite" {
		config.Database, config.DSN = "", os.Getenv("GOLAR_TEST_DSN")
	}
	db, err := database.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := schema.New(db.DB, driver)
	drop := func() {
		for _, table := range []string{"auth_users", auth.TokensTable, auth.ResetsTable} {
			if err := s.DropIfExists(table); err != nil {
				t.Fatal(err)
			}
		}
	}
	drop()
	t.Cleanup(drop)
	err = s.Create("auth_users", func(b *schema.Blueprint) {
		b.ID()
		b.String("email", 191).Unique()
		b.String("password", 255)
		b.Timestamp("email_verified_at").Nullable()
		b.String("roles", 255)
		b.Timestamps()
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.CreateTables(s); err != nil {
		t.Fatal(err)
	}
	if err := auth.CreateTables(s); err != nil { // idempotent
		t.Fatal(err)
	}
	e := &env{db: db, provider: &auth.ORMProvider[User]{DB: db}}
	e.ada = e.user(t, "ada@example.com", "correct horse", "admin")
	return e
}

func (e *env) user(t *testing.T, email, password, roles string) *User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	u := &User{Email: email, Password: hash, Roles: roles}
	if err := orm.Create(context.Background(), e.db, u); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestAttempt(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	a := &auth.Authenticator{Provider: e.provider, Limiter: auth.NewLimiter(3, time.Minute)}
	user, err := a.Attempt(ctx, "ada@example.com", "correct horse")
	if err != nil || user.AuthID() != e.ada.AuthID() {
		t.Fatalf("valid credentials: %v %v", user, err)
	}
	if _, err := a.Attempt(ctx, "ada@example.com", "wrong"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	if _, err := a.Attempt(ctx, "nobody@example.com", "wrong"); !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Fatalf("unknown user must look like a wrong password: %v", err)
	}
	a.Attempt(ctx, "ADA@example.com ", "wrong") // identifiers are normalized for throttling
	a.Attempt(ctx, "ada@example.com", "wrong")
	_, err = a.Attempt(ctx, "ada@example.com", "correct horse")
	var limited *auth.RateLimitError
	if !errors.Is(err, auth.ErrTooManyAttempts) || !errors.As(err, &limited) || limited.RetryAfter <= 0 {
		t.Fatalf("brute force must be throttled even with the right password: %v", err)
	}
	if _, err := a.Attempt(ctx, "nobody@example.com", "x"); errors.Is(err, auth.ErrTooManyAttempts) {
		t.Fatal("throttling is per identifier")
	}

	// A bcrypt hash (e.g. imported from Laravel) is upgraded to argon2id on login.
	legacy, _ := auth.Bcrypt{Cost: 4}.Hash("old secret")
	bob := &User{Email: "bob@example.com", Password: legacy}
	if err := orm.Create(ctx, e.db, bob); err != nil {
		t.Fatal(err)
	}
	if _, err := (&auth.Authenticator{Provider: e.provider}).Attempt(ctx, "bob@example.com", "old secret"); err != nil {
		t.Fatal(err)
	}
	upgraded, _ := orm.Find[User](ctx, e.db, bob.ID)
	if !strings.HasPrefix(upgraded.Password, "$argon2id$") || !auth.VerifyPassword(upgraded.Password, "old secret") {
		t.Fatalf("rehash on login: %s", upgraded.Password)
	}
	if _, err := e.provider.RetrieveByID(ctx, "not-a-number"); !errors.Is(err, auth.ErrUserNotFound) {
		t.Fatalf("malformed id: %v", err)
	}
}

// client is a browser-like HTTP client with a cookie jar.
type client struct {
	t    *testing.T
	base string
	http *http.Client
}

func (c *client) do(method, path, csrf string, form url.Values) (int, string) {
	c.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	r, _ := http.NewRequest(method, c.base+path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if csrf != "" {
		r.Header.Set(auth.CSRFHeader, csrf)
	}
	res, err := c.http.Do(r)
	if err != nil {
		c.t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, strings.TrimSpace(string(b))
}

func (c *client) sessionID() string {
	u, _ := url.Parse(c.base)
	for _, cookie := range c.http.Jar.Cookies(u) {
		if cookie.Name == "golar_session" {
			return cookie.Value
		}
	}
	return ""
}

func TestSessionLoginCSRFAndAuthorization(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	e.user(t, "eve@example.com", "eve password", "member")
	sessions := &session.Manager{Store: session.NewMemoryStore(), Insecure: true}
	guard := &auth.SessionGuard{Provider: e.provider}
	login := &auth.Authenticator{Provider: e.provider}
	rbac := auth.NewRBAC()
	rbac.Grant("member", "posts.read")
	rbac.Grant("admin", "posts.*")

	mux := http.NewServeMux()
	web := func(h http.HandlerFunc) http.Handler { return sessions.Middleware(auth.CSRF(h)) }
	protected := func(h http.Handler) http.Handler { return sessions.Middleware(auth.Required(guard)(h)) }
	mux.Handle("GET /csrf", web(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, auth.CSRFToken(r)) }))
	mux.Handle("POST /login", web(func(w http.ResponseWriter, r *http.Request) {
		user, err := login.Attempt(r.Context(), r.PostFormValue("email"), r.PostFormValue("password"))
		if err != nil {
			http.Error(w, "invalid", http.StatusUnauthorized)
			return
		}
		if err := guard.Login(r, user); err != nil {
			t.Fatal(err)
		}
	}))
	mux.Handle("POST /logout", web(func(w http.ResponseWriter, r *http.Request) { guard.Logout(r) }))
	mux.Handle("POST /posts", sessions.Middleware(auth.CSRF(auth.Required(guard)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "created")
	})))))
	mux.Handle("GET /me", protected(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, _ := auth.UserAs[*User](r.Context())
		io.WriteString(w, user.Email)
	})))
	mux.Handle("GET /admin", protected(rbac.RequireRole("admin")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))))
	mux.Handle("GET /posts/edit", protected(rbac.RequirePermission("posts.write")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))))
	mux.Handle("GET /verified", protected(auth.RequireVerified(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))))
	server := httptest.NewServer(mux)
	defer server.Close()
	newClient := func() *client {
		jar, _ := cookiejar.New(nil)
		return &client{t: t, base: server.URL, http: &http.Client{Jar: jar}}
	}

	c := newClient()
	if code, _ := c.do("GET", "/me", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous /me: %d", code)
	}
	creds := url.Values{"email": {"ada@example.com"}, "password": {"correct horse"}}
	if code, _ := c.do("POST", "/login", "", creds); code != http.StatusForbidden {
		t.Fatalf("login without CSRF token: %d", code)
	}
	_, token := c.do("GET", "/csrf", "", nil)
	before := c.sessionID()
	if code, _ := c.do("POST", "/login", "forged", creds); code != http.StatusForbidden {
		t.Fatalf("wrong CSRF token: %d", code)
	}
	if code, _ := c.do("POST", "/login", token, url.Values{"email": {"ada@example.com"}, "password": {"nope"}}); code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	if code, _ := c.do("POST", "/login", token, creds); code != http.StatusOK {
		t.Fatalf("login: %d", code)
	}
	if c.sessionID() == before || c.sessionID() == "" {
		t.Fatal("login must regenerate the session ID")
	}
	if code, body := c.do("GET", "/me", "", nil); code != http.StatusOK || body != "ada@example.com" {
		t.Fatalf("/me: %d %q", code, body)
	}
	if code, _ := c.do("POST", "/posts", token, url.Values{}); code != http.StatusForbidden {
		t.Fatal("login must rotate the CSRF token")
	}
	_, token = c.do("GET", "/csrf", "", nil)
	if code, body := c.do("POST", "/posts", "", url.Values{auth.CSRFField: {token}}); code != http.StatusOK || body != "created" {
		t.Fatalf("form token: %d %q", code, body)
	}
	if code, _ := c.do("GET", "/admin", "", nil); code != http.StatusOK {
		t.Fatalf("admin role: %d", code)
	}
	if code, _ := c.do("GET", "/posts/edit", "", nil); code != http.StatusOK {
		t.Fatalf("wildcard permission: %d", code)
	}
	if code, _ := c.do("GET", "/verified", "", nil); code != http.StatusForbidden {
		t.Fatalf("unverified email: %d", code)
	}

	// The old (pre-login) session ID must not be usable: session fixation.
	stolen := newClient()
	u, _ := url.Parse(server.URL)
	stolen.http.Jar.SetCookies(u, []*http.Cookie{{Name: "golar_session", Value: before}})
	if code, _ := stolen.do("GET", "/me", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("pre-login session ID still works: %d", code)
	}

	// A second user is authorized by role.
	eve := newClient()
	_, eveToken := eve.do("GET", "/csrf", "", nil)
	eve.do("POST", "/login", eveToken, url.Values{"email": {"eve@example.com"}, "password": {"eve password"}})
	if code, _ := eve.do("GET", "/admin", "", nil); code != http.StatusForbidden {
		t.Fatalf("member on admin route: %d", code)
	}
	if code, _ := eve.do("GET", "/posts/edit", "", nil); code != http.StatusForbidden {
		t.Fatalf("member lacks posts.write: %d", code)
	}

	// Changing the password ends existing sessions.
	hash, _ := auth.HashPassword("new password")
	if err := e.provider.UpdatePassword(ctx, e.ada, hash); err != nil {
		t.Fatal(err)
	}
	if code, _ := c.do("GET", "/me", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("session after password change: %d", code)
	}

	// Logout invalidates the session.
	_, eveToken = eve.do("GET", "/csrf", "", nil)
	if code, _ := eve.do("POST", "/logout", eveToken, url.Values{}); code != http.StatusOK {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := eve.do("GET", "/me", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("after logout: %d", code)
	}

	// Without the session middleware, guards fail closed.
	r := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	auth.Required(guard)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("must not run") })).ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("missing session middleware: %d", w.Code)
	}
	w = httptest.NewRecorder()
	auth.CSRF(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("must not run") })).ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code == http.StatusOK {
		t.Fatal("CSRF without session must fail closed")
	}
}

func TestBearerTokens(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for name, store := range map[string]auth.TokenStore{"memory": auth.NewMemoryTokenStore(), "sql": &auth.SQLTokenStore{DB: e.db}} {
		t.Run(name, func(t *testing.T) {
			guard := &auth.TokenGuard{Provider: e.provider, Store: store}
			handler := auth.Required(guard)(auth.RequireAbility("posts:read")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user := auth.User(r.Context())
				token, _ := auth.TokenFrom(r.Context())
				io.WriteString(w, user.AuthID()+":"+token.Name)
			})))
			call := func(header string) *httptest.ResponseRecorder {
				r := httptest.NewRequest("GET", "/api", nil)
				if header != "" {
					r.Header.Set("Authorization", header)
				}
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				return w
			}
			plain, token, err := guard.Issue(ctx, e.ada, "cli", []string{"posts:*"}, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(plain, "golar_") || len(plain) < 40 || token.ID == plain || strings.Contains(token.ID, plain) {
				t.Fatalf("token format: %q %+v", plain, token)
			}
			stored, err := store.Find(ctx, token.ID)
			if err != nil || stored.UserID != e.ada.AuthID() || stored.Name != "cli" || stored.ExpiresAt == nil || !stored.Can("posts:delete") {
				t.Fatalf("stored token: %+v %v", stored, err)
			}
			if w := call("Bearer " + plain); w.Code != http.StatusOK || w.Body.String() != e.ada.AuthID()+":cli" {
				t.Fatalf("valid token: %d %q", w.Code, w.Body)
			}
			if w := call("bearer " + plain); w.Code != http.StatusOK {
				t.Fatal("scheme is case-insensitive")
			}
			for _, bad := range []string{"", "Bearer", "Bearer " + plain + "x", "Basic " + plain, "Bearer golar_" + strings.Repeat("A", 43), "Bearer " + token.ID, "Bearer " + strings.Repeat("a", 5000)} {
				w := call(bad)
				if w.Code != http.StatusUnauthorized || !strings.HasPrefix(w.Header().Get("WWW-Authenticate"), "Bearer") {
					t.Fatalf("%.30q: %d %q", bad, w.Code, w.Header().Get("WWW-Authenticate"))
				}
			}
			readOnly, _, _ := guard.Issue(ctx, e.ada, "ro", []string{"profile:read"}, 0)
			if w := call("Bearer " + readOnly); w.Code != http.StatusForbidden {
				t.Fatalf("missing ability: %d", w.Code)
			}
			if err := guard.Revoke(ctx, plain); err != nil {
				t.Fatal(err)
			}
			if w := call("Bearer " + plain); w.Code != http.StatusUnauthorized {
				t.Fatalf("revoked token: %d", w.Code)
			}
			expired, _, _ := guard.Issue(ctx, e.ada, "short", []string{"*"}, time.Nanosecond)
			time.Sleep(1100 * time.Millisecond)
			if w := call("Bearer " + expired); w.Code != http.StatusUnauthorized {
				t.Fatalf("expired token: %d", w.Code)
			}
			if err := store.DeleteUser(ctx, e.ada.AuthID()); err != nil {
				t.Fatal(err)
			}
			if w := call("Bearer " + readOnly); w.Code != http.StatusUnauthorized {
				t.Fatalf("token after DeleteUser: %d", w.Code)
			}
		})
	}
	// Optional lets anonymous and invalid-token requests through without a user.
	guard := &auth.TokenGuard{Provider: e.provider, Store: auth.NewMemoryTokenStore()}
	var seen auth.Authenticatable = e.ada
	h := auth.Optional(guard)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = auth.User(r.Context()) }))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer golar_invalid")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if seen != nil {
		t.Fatal("invalid token must be anonymous")
	}
}

type Post struct{ UserID int64 }

func TestGatesAndPolicies(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	eve := e.user(t, "eve@example.com", "x", "member")
	rbac := auth.NewRBAC()
	rbac.Grant("member", "comments.create")
	rbac.Grant("editor", "posts.publish")
	rbac.Inherit("admin", "editor")
	rbac.Inherit("editor", "admin") // cycles are tolerated
	gate := auth.NewGate()
	gate.UseRBAC(rbac)
	gate.Define("view-dashboard", func(_ context.Context, u auth.Authenticatable, _ ...any) bool { return u.(*User).Roles == "admin" })
	auth.Policy(gate, "update", func(_ context.Context, u auth.Authenticatable, p *Post) bool {
		return u.AuthID() == strconv.FormatInt(p.UserID, 10)
	})

	own, other := &Post{UserID: eve.ID}, &Post{UserID: e.ada.ID}
	for _, c := range []struct {
		user    auth.Authenticatable
		ability string
		args    []any
		want    bool
	}{
		{eve, "update", []any{own}, true},
		{eve, "update", []any{other}, false},
		{eve, "update", []any{*own}, false}, // policy registered for *Post only
		{eve, "update", nil, false},
		{e.ada, "view-dashboard", nil, true},
		{eve, "view-dashboard", nil, false},
		{eve, "comments.create", nil, true},
		{eve, "posts.publish", nil, false},
		{e.ada, "posts.publish", nil, true}, // inherited
		{eve, "undefined", nil, false},
		{nil, "comments.create", nil, false},
		{(*User)(nil), "comments.create", nil, false},
	} {
		if got := gate.Allows(ctx, c.user, c.ability, c.args...); got != c.want {
			t.Errorf("%v %s %v = %v", c.user, c.ability, c.args, got)
		}
	}
	if err := gate.Authorize(ctx, eve, "update", other); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("authorize: %v", err)
	}
	if err := gate.Authorize(ctx, nil, "update", own); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Fatalf("authorize anonymous: %v", err)
	}
	gate.Before(func(_ context.Context, u auth.Authenticatable, _ string) (bool, bool) {
		return true, u.(*User).Roles == "admin"
	})
	if !gate.Allows(ctx, e.ada, "update", own) || gate.Allows(ctx, eve, "update", other) {
		t.Fatal("before hook")
	}
	if got := rbac.Permissions("admin"); strings.Join(got, ",") != "posts.publish" {
		t.Fatalf("permissions: %v", got)
	}
	rbac.Revoke("editor", "posts.publish")
	if gate.Allows(ctx, eve, "posts.publish") {
		t.Fatal("revoke")
	}
	h := gate.Can("view-dashboard")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for user, want := range map[auth.Authenticatable]int{nil: 401, eve: 403, e.ada: 200} {
		r := httptest.NewRequest("GET", "/", nil)
		if user != nil {
			r = r.WithContext(auth.WithUser(r.Context(), user))
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != want {
			t.Errorf("gate middleware for %v: %d, want %d", user, w.Code, want)
		}
	}
}

func TestPasswordReset(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	for name, store := range map[string]auth.ResetStore{"memory": auth.NewMemoryResetStore(), "sql": &auth.SQLResetStore{DB: e.db}} {
		t.Run(name, func(t *testing.T) {
			var sent string
			var resets int
			broker := &auth.PasswordBroker{
				Provider: e.provider, Store: store, Throttle: time.Hour, TTL: time.Hour,
				Send:    func(_ context.Context, _ auth.Authenticatable, token string) error { sent = token; return nil },
				OnReset: func(context.Context, auth.Authenticatable) error { resets++; return nil },
			}
			if err := broker.SendResetLink(ctx, "nobody@example.com"); err != nil || sent != "" {
				t.Fatalf("unknown users must be silent: %v %q", err, sent)
			}
			if err := broker.SendResetLink(ctx, "ada@example.com"); err != nil || len(sent) < 43 {
				t.Fatalf("send: %v %q", err, sent)
			}
			first := sent
			if err := broker.SendResetLink(ctx, "ada@example.com"); !errors.Is(err, auth.ErrThrottled) {
				t.Fatalf("throttle: %v", err)
			}
			if err := broker.Reset(ctx, "ada@example.com", first, "short"); !errors.Is(err, auth.ErrWeakPassword) {
				t.Fatalf("weak: %v", err)
			}
			if err := broker.Reset(ctx, "ada@example.com", first+"x", "brand new password"); !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("wrong token: %v", err)
			}
			if err := broker.Reset(ctx, "eve@example.com", first, "brand new password"); !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("token for another user: %v", err)
			}
			if err := broker.Reset(ctx, "ada@example.com", first, "brand new password"); err != nil || resets != 1 {
				t.Fatalf("reset: %v %d", err, resets)
			}
			if _, err := (&auth.Authenticator{Provider: e.provider}).Attempt(ctx, "ada@example.com", "brand new password"); err != nil {
				t.Fatalf("login with new password: %v", err)
			}
			if err := broker.Reset(ctx, "ada@example.com", first, "another password"); !errors.Is(err, auth.ErrInvalidToken) {
				t.Fatalf("tokens are single use: %v", err)
			}
			broker.Throttle, broker.TTL = time.Nanosecond, time.Nanosecond
			broker.SendResetLink(ctx, "ada@example.com")
			time.Sleep(10 * time.Millisecond)
			if err := broker.Reset(ctx, "ada@example.com", sent, "another password"); !errors.Is(err, auth.ErrExpiredToken) {
				t.Fatalf("expired: %v", err)
			}
		})
	}
}

func TestEmailVerification(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	var sent string
	v := &auth.EmailVerification{
		Secret: []byte(strings.Repeat("s", 32)), Provider: e.provider,
		Send: func(_ context.Context, _ auth.MustVerifyEmail, token string) error { sent = token; return nil },
	}
	if err := v.SendVerification(ctx, e.ada); err != nil || sent == "" {
		t.Fatal(err)
	}
	parts := strings.Split(sent, ".")
	tampered := []string{
		"", "a.b.c", sent + "x",
		strings.Join([]string{parts[0], "9999999999", parts[2], parts[3]}, "."), // extended expiry
		strings.Join([]string{"Mg", parts[1], parts[2], parts[3]}, "."),         // other user ID
	}
	for i, token := range tampered {
		if _, err := v.Verify(ctx, token); !errors.Is(err, auth.ErrInvalidToken) {
			t.Fatalf("tampered %d: %v", i, err)
		}
	}
	other := &auth.EmailVerification{Secret: []byte(strings.Repeat("o", 32)), Provider: e.provider}
	if _, err := other.Verify(ctx, sent); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("other secret: %v", err)
	}
	user, err := v.Verify(ctx, sent)
	if err != nil || user.AuthID() != e.ada.AuthID() {
		t.Fatalf("verify: %v", err)
	}
	reloaded, _ := orm.Find[User](ctx, e.db, e.ada.ID)
	if !reloaded.EmailVerified() {
		t.Fatal("verification must be stored")
	}
	if _, err := v.Verify(ctx, sent); err != nil {
		t.Fatalf("verifying twice is harmless: %v", err)
	}

	eve := e.user(t, "eve@example.com", "x", "")
	token, _ := v.Token(eve)
	eve.Email = "eve@new.example.com"
	if err := orm.Save(ctx, e.db, eve); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(ctx, token); !errors.Is(err, auth.ErrInvalidToken) {
		t.Fatalf("token must die when the email changes: %v", err)
	}
	short := &auth.EmailVerification{Secret: []byte("short"), Provider: e.provider, TTL: -time.Second}
	if _, err := short.Token(eve); err == nil {
		t.Fatal("short secrets must be rejected")
	}
	v.TTL = -time.Second
	expired, _ := v.Token(eve)
	if _, err := v.Verify(ctx, expired); !errors.Is(err, auth.ErrExpiredToken) {
		t.Fatalf("expired: %v", err)
	}
}
