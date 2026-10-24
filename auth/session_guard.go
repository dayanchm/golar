package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"

	"github.com/dayanchm/golar/http/session"
)

const (
	sessionUserKey     = "auth.user"
	sessionPasswordKey = "auth.password"
)

// SessionGuard authenticates users stored in the request's session. It must
// run inside session.Manager.Middleware.
type SessionGuard struct {
	Provider UserProvider
}

var errNoSession = errors.New("auth: session middleware is not installed")

// passwordFingerprint lets sessions end when the user's password changes.
func passwordFingerprint(user Authenticatable) string {
	sum := sha256.Sum256([]byte("golar-session:" + user.AuthPassword()))
	return hex.EncodeToString(sum[:16])
}

// Authenticate returns the session's user, or nil when nobody is logged in.
// Sessions created before a password change are logged out.
func (g *SessionGuard) Authenticate(r *http.Request) (Authenticatable, error) {
	s := session.FromContext(r.Context())
	if s == nil {
		return nil, errNoSession
	}
	id := s.Get(sessionUserKey)
	if id == "" {
		return nil, nil
	}
	user, err := g.Provider.RetrieveByID(r.Context(), id)
	if errors.Is(err, ErrUserNotFound) {
		return nil, s.Invalidate()
	}
	if err != nil {
		return nil, err
	}
	if subtle.ConstantTimeCompare([]byte(s.Get(sessionPasswordKey)), []byte(passwordFingerprint(user))) != 1 {
		return nil, s.Invalidate()
	}
	return user, nil
}

// Login stores user in the session after regenerating its ID (preventing
// session fixation) and rotating the CSRF token. Call it before writing the response.
func (g *SessionGuard) Login(r *http.Request, user Authenticatable) error {
	s := session.FromContext(r.Context())
	if s == nil {
		return errNoSession
	}
	if err := s.Regenerate(); err != nil {
		return err
	}
	s.Put(sessionUserKey, user.AuthID())
	s.Put(sessionPasswordKey, passwordFingerprint(user))
	s.Delete(csrfSessionKey)
	return nil
}

// Logout clears the session and issues a new ID.
func (g *SessionGuard) Logout(r *http.Request) error {
	s := session.FromContext(r.Context())
	if s == nil {
		return errNoSession
	}
	return s.Invalidate()
}
