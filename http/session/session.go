// Package session provides cookie-identified server-side sessions.
package session

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"maps"
	"net/http"
	"sync"
	"time"
)

// Store persists session data by ID. Implementations must be safe for concurrent use.
type Store interface {
	// Load returns the data for id; ok is false when it is missing or expired.
	Load(ctx context.Context, id string) (data map[string]string, ok bool, err error)
	Save(ctx context.Context, id string, data map[string]string, expires time.Time) error
	Delete(ctx context.Context, id string) error
}

// Manager issues session cookies and loads sessions for each request.
type Manager struct {
	Store Store
	// Cookie defaults: name "golar_session", path "/", HttpOnly, Secure,
	// SameSite=Lax. Set Insecure only for local HTTP development.
	CookieName string
	Path       string
	Domain     string
	SameSite   http.SameSite
	Insecure   bool
	// Lifetime is the idle timeout; each request extends it. Defaults to 2 hours.
	Lifetime time.Duration
}

// NewManager returns a manager with secure defaults and an in-memory store.
func NewManager() *Manager { return &Manager{Store: NewMemoryStore()} }

// Session is one request's view of a session. It is safe for concurrent use
// within the request.
type Session struct {
	mu       sync.Mutex
	id       string
	previous string // ID to delete after Regenerate
	data     map[string]string
	existed  bool
	changed  bool
}

type contextKey struct{}

// FromContext returns the request's session, or nil outside Manager.Middleware.
func FromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(contextKey{}).(*Session)
	return s
}

// Get returns the value for key, or "".
func (s *Session) Get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.data[key]
}

// Put stores value under key.
func (s *Session) Put(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
	s.changed = true
}

// Delete removes key.
func (s *Session) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
	s.changed = true
}

// ID returns the current session ID.
func (s *Session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// Regenerate issues a new ID and keeps the data. Call it on login and
// privilege changes to prevent session fixation.
func (s *Session) Regenerate() error {
	id, err := newID()
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.previous == "" && s.existed {
		s.previous = s.id
	}
	s.id, s.changed = id, true
	return nil
}

// Invalidate clears all data and issues a new ID, e.g. on logout.
func (s *Session) Invalidate() error {
	s.mu.Lock()
	clear(s.data)
	s.mu.Unlock()
	return s.Regenerate()
}

func newID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (m *Manager) cookieName() string {
	if m.CookieName != "" {
		return m.CookieName
	}
	return "golar_session"
}

func (m *Manager) lifetime() time.Duration {
	if m.Lifetime > 0 {
		return m.Lifetime
	}
	return 2 * time.Hour
}

// Middleware loads the session before next and saves it afterwards. Sessions
// are only created, and cookies only sent, once data is stored.
func (m *Manager) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := m.load(r)
		if err != nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		sw := &writer{ResponseWriter: w, manager: m, session: s}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), contextKey{}, s)))
		sw.setCookie() // when the handler wrote nothing
		if err := m.save(r.Context(), s); err != nil && !sw.wrote {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		}
	})
}

func (m *Manager) load(r *http.Request) (*Session, error) {
	if m.Store == nil {
		return nil, errors.New("session: store is required")
	}
	if cookie, err := r.Cookie(m.cookieName()); err == nil && cookie.Value != "" {
		data, ok, err := m.Store.Load(r.Context(), cookie.Value)
		if err != nil {
			return nil, err
		}
		if ok {
			return &Session{id: cookie.Value, data: data, existed: true}, nil
		}
	}
	// Never adopt a client-chosen ID: unknown cookies get a fresh one.
	id, err := newID()
	if err != nil {
		return nil, err
	}
	return &Session{id: id, data: map[string]string{}}, nil
}

func (m *Manager) save(ctx context.Context, s *Session) error {
	s.mu.Lock()
	id, previous, data, active := s.id, s.previous, maps.Clone(s.data), s.existed || s.changed
	s.mu.Unlock()
	if previous != "" {
		if err := m.Store.Delete(ctx, previous); err != nil {
			return err
		}
	}
	if !active {
		return nil
	}
	if len(data) == 0 && !s.existed && previous == "" {
		return nil // nothing worth persisting
	}
	return m.Store.Save(ctx, id, data, time.Now().Add(m.lifetime()))
}

// writer adds the session cookie before the response headers are sent.
type writer struct {
	http.ResponseWriter
	manager *Manager
	session *Session
	wrote   bool
}

func (w *writer) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *writer) setCookie() {
	if w.wrote {
		return
	}
	w.wrote = true
	s := w.session
	s.mu.Lock()
	id, active := s.id, s.existed || s.changed && (len(s.data) > 0 || s.previous != "")
	s.mu.Unlock()
	if !active {
		return
	}
	m := w.manager
	sameSite := m.SameSite
	if sameSite == 0 {
		sameSite = http.SameSiteLaxMode
	}
	path := m.Path
	if path == "" {
		path = "/"
	}
	http.SetCookie(w.ResponseWriter, &http.Cookie{
		Name: m.cookieName(), Value: id, Path: path, Domain: m.Domain,
		MaxAge: int(m.lifetime().Seconds()), HttpOnly: true, Secure: !m.Insecure, SameSite: sameSite,
	})
}

func (w *writer) WriteHeader(code int) {
	w.setCookie()
	w.ResponseWriter.WriteHeader(code)
}

func (w *writer) Write(b []byte) (int, error) {
	w.setCookie()
	return w.ResponseWriter.Write(b)
}
