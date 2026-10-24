package auth

import (
	"context"
	"errors"
	"net/http"
)

// authenticate runs guards in order and returns the first user found.
func authenticate(r *http.Request, guards []Guard) (*http.Request, Authenticatable, error) {
	for _, g := range guards {
		var user Authenticatable
		var err error
		ctx := r.Context()
		if tg, ok := g.(*TokenGuard); ok {
			var token Token
			if user, token, err = tg.authenticate(r); user != nil {
				ctx = context.WithValue(ctx, tokenKey{}, token)
			}
		} else {
			user, err = g.Authenticate(r)
		}
		if err != nil {
			return r, nil, err
		}
		if user != nil {
			return r.WithContext(WithUser(ctx, user)), user, nil
		}
	}
	return r, nil, nil
}

func unauthorized(w http.ResponseWriter, guards []Guard) {
	for _, g := range guards {
		if _, ok := g.(*TokenGuard); ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="api"`)
			break
		}
	}
	http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
}

// Required rejects requests that no guard authenticates with 401 Unauthorized
// and stores the user in the request context for handlers (see User).
func Required(guards ...Guard) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, user, err := authenticate(r, guards)
			if errors.Is(err, errNoSession) {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			if err != nil && !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrExpiredToken) {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			if user == nil {
				unauthorized(w, guards)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// Optional stores the user when a guard authenticates the request and lets
// every request through. Invalid tokens are treated as anonymous.
func Optional(guards ...Guard) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r, _, err := authenticate(r, guards)
			if err != nil && !errors.Is(err, ErrInvalidToken) && !errors.Is(err, ErrExpiredToken) {
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireAbility rejects token-authenticated requests whose token lacks any
// listed ability with 403 Forbidden. Session-authenticated requests pass.
// Install it after Required.
func RequireAbility(abilities ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if token, ok := TokenFrom(r.Context()); ok {
				for _, ability := range abilities {
					if !token.Can(ability) {
						http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
						return
					}
				}
			} else if !Check(r.Context()) {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
