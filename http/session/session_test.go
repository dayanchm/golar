package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func serve(m *Manager, h http.HandlerFunc, cookie *http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	m.Middleware(h).ServeHTTP(w, r)
	return w
}

func cookieOf(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want 1 cookie, got %v", cookies)
	}
	return cookies[0]
}

func TestSessionLifecycle(t *testing.T) {
	store := NewMemoryStore()
	m := &Manager{Store: store}
	if w := serve(m, func(w http.ResponseWriter, r *http.Request) { FromContext(r.Context()).Get("x") }, nil); len(w.Result().Cookies()) != 0 {
		t.Fatal("reading an empty session must not create one")
	}
	w := serve(m, func(w http.ResponseWriter, r *http.Request) {
		FromContext(r.Context()).Put("user", "1")
		w.Write([]byte("ok"))
	}, nil)
	c := cookieOf(t, w)
	if c.Name != "golar_session" || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 7200 || len(c.Value) < 43 {
		t.Fatalf("cookie flags: %+v", c)
	}
	var got string
	serve(m, func(w http.ResponseWriter, r *http.Request) { got = FromContext(r.Context()).Get("user") }, c)
	if got != "1" {
		t.Fatalf("load: %q", got)
	}

	// Regenerate keeps data, changes the ID and deletes the old one.
	w = serve(m, func(w http.ResponseWriter, r *http.Request) {
		if err := FromContext(r.Context()).Regenerate(); err != nil {
			t.Fatal(err)
		}
	}, c)
	renewed := cookieOf(t, w)
	if renewed.Value == c.Value {
		t.Fatal("regenerate must change the ID")
	}
	if _, ok, _ := store.Load(context.Background(), c.Value); ok {
		t.Fatal("old session must be deleted")
	}
	serve(m, func(w http.ResponseWriter, r *http.Request) { got = FromContext(r.Context()).Get("user") }, renewed)
	if got != "1" {
		t.Fatal("regenerate must keep data")
	}

	// Invalidate clears data.
	w = serve(m, func(w http.ResponseWriter, r *http.Request) { FromContext(r.Context()).Invalidate() }, renewed)
	cleared := cookieOf(t, w)
	serve(m, func(w http.ResponseWriter, r *http.Request) { got = FromContext(r.Context()).Get("user") }, cleared)
	if got != "" || cleared.Value == renewed.Value {
		t.Fatal("invalidate must clear data and change the ID")
	}
}

func TestSessionFixation(t *testing.T) {
	m := &Manager{Store: NewMemoryStore()}
	attacker := &http.Cookie{Name: "golar_session", Value: "attacker-chosen-id"}
	w := serve(m, func(w http.ResponseWriter, r *http.Request) {
		s := FromContext(r.Context())
		if s.ID() == attacker.Value {
			t.Fatal("unknown session IDs must not be adopted")
		}
		s.Put("user", "1")
	}, attacker)
	if c := cookieOf(t, w); c.Value == attacker.Value {
		t.Fatal("cookie must carry a server-generated ID")
	}
}

func TestSessionExpiryAndOptions(t *testing.T) {
	store := NewMemoryStore()
	m := &Manager{Store: store, CookieName: "sid", Insecure: true, SameSite: http.SameSiteStrictMode, Lifetime: time.Minute, Domain: "example.com"}
	w := serve(m, func(w http.ResponseWriter, r *http.Request) { FromContext(r.Context()).Put("k", "v") }, nil)
	c := cookieOf(t, w)
	if c.Name != "sid" || c.Secure || c.SameSite != http.SameSiteStrictMode || c.MaxAge != 60 || c.Domain != "example.com" {
		t.Fatalf("options: %+v", c)
	}
	store.Save(context.Background(), c.Value, map[string]string{"k": "v"}, time.Now().Add(-time.Second))
	var got string
	serve(m, func(w http.ResponseWriter, r *http.Request) { got = FromContext(r.Context()).Get("k") }, c)
	if got != "" {
		t.Fatal("expired sessions must not load")
	}
	if w := serve(&Manager{}, func(w http.ResponseWriter, r *http.Request) {}, nil); w.Code != http.StatusInternalServerError {
		t.Fatal("missing store must fail closed")
	}
	if FromContext(context.Background()) != nil {
		t.Fatal("no session outside middleware")
	}
}
