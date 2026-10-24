package auth

import (
	"context"
	"errors"
	"strings"
)

// Authenticator checks login credentials against a UserProvider.
type Authenticator struct {
	Provider UserProvider
	Hasher   Hasher   // defaults to DefaultHasher
	Limiter  *Limiter // optional; throttles failed attempts per identifier
}

// Attempt returns the user whose identifier and password match. Unknown users
// and wrong passwords both return ErrInvalidCredentials after a full hash
// verification, so responses do not reveal which accounts exist. With a
// Limiter, failures count against the lower-cased identifier and a blocked
// identifier returns a *RateLimitError. Outdated hashes are upgraded when the
// provider implements PasswordUpdater.
func (a *Authenticator) Attempt(ctx context.Context, identifier, password string) (Authenticatable, error) {
	key := "login:" + strings.ToLower(strings.TrimSpace(identifier))
	if a.Limiter != nil {
		if blocked, retry := a.Limiter.Blocked(key); blocked {
			return nil, &RateLimitError{RetryAfter: retry}
		}
	}
	h := hasher(a.Hasher)
	user, err := a.Provider.RetrieveByIdentifier(ctx, identifier)
	if err != nil && !errors.Is(err, ErrUserNotFound) {
		return nil, err
	}
	if user == nil {
		_, _ = h.Hash(password) // costs about as much as Verify, with the configured hasher
		return nil, a.fail(key)
	}
	if !h.Verify(user.AuthPassword(), password) {
		return nil, a.fail(key)
	}
	if a.Limiter != nil {
		a.Limiter.Clear(key)
	}
	if updater, ok := a.Provider.(PasswordUpdater); ok && h.NeedsRehash(user.AuthPassword()) {
		if hash, err := h.Hash(password); err == nil {
			_ = updater.UpdatePassword(ctx, user, hash) // best effort; login still succeeds
		}
	}
	return user, nil
}

func (a *Authenticator) fail(key string) error {
	if a.Limiter != nil {
		a.Limiter.Hit(key)
	}
	return ErrInvalidCredentials
}
