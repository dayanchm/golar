package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"

	"github.com/dayanchm/golar/http/session"
)

const csrfSessionKey = "auth.csrf"

// CSRFHeader and CSRFField carry the token on unsafe requests.
const (
	CSRFHeader = "X-CSRF-Token"
	CSRFField  = "_token"
)

// CSRFToken returns the session's CSRF token, creating it on first use. Embed
// it in forms as the "_token" field or send it in the X-CSRF-Token header.
func CSRFToken(r *http.Request) string {
	s := session.FromContext(r.Context())
	if s == nil {
		return ""
	}
	if token := s.Get(csrfSessionKey); token != "" {
		return token
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	s.Put(csrfSessionKey, token)
	return token
}

// CSRF rejects POST, PUT, PATCH, DELETE and other unsafe requests with 403
// Forbidden unless they carry the session's CSRF token. It must run inside
// session.Manager.Middleware. Requests without a session token are rejected,
// so a forged request can never pass by omitting both sides of the comparison.
// Bearer-token APIs do not need CSRF protection; mount them without it.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}
		s := session.FromContext(r.Context())
		if s == nil {
			http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			return
		}
		expected := s.Get(csrfSessionKey)
		got := r.Header.Get(CSRFHeader)
		if got == "" {
			// Bounded read of url-encoded or multipart bodies; never the query string.
			r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
			got = r.PostFormValue(CSRFField)
		}
		if expected == "" || subtle.ConstantTimeCompare([]byte(got), []byte(expected)) != 1 {
			http.Error(w, "CSRF token mismatch", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
