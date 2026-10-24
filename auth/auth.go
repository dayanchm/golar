// Package auth provides authentication and authorization for Golar
// applications: password hashing, credential checks, session and bearer token
// guards, middleware, gates, policies, roles and permissions, password resets,
// email verification, rate limiting and CSRF protection.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// Authenticatable is implemented by user models.
type Authenticatable interface {
	// AuthID returns the user's stable identifier, such as the primary key.
	AuthID() string
	// AuthPassword returns the stored password hash.
	AuthPassword() string
}

// UserProvider loads users. Both methods return ErrUserNotFound when no user matches.
type UserProvider interface {
	RetrieveByID(ctx context.Context, id string) (Authenticatable, error)
	// RetrieveByIdentifier finds a user by login identifier, such as an email.
	RetrieveByIdentifier(ctx context.Context, identifier string) (Authenticatable, error)
}

// PasswordUpdater is implemented by providers that can store a new password hash.
type PasswordUpdater interface {
	UpdatePassword(ctx context.Context, user Authenticatable, hash string) error
}

// Guard authenticates a request. It returns (nil, nil) when the request
// carries no credentials and an error when credentials are invalid.
type Guard interface {
	Authenticate(r *http.Request) (Authenticatable, error)
}

var (
	ErrUserNotFound       = errors.New("auth: user not found")
	ErrInvalidCredentials = errors.New("auth: invalid credentials")
	ErrUnauthenticated    = errors.New("auth: unauthenticated")
	ErrForbidden          = errors.New("auth: forbidden")
	ErrInvalidToken       = errors.New("auth: invalid token")
	ErrExpiredToken       = errors.New("auth: token expired")
	ErrTooManyAttempts    = errors.New("auth: too many attempts")
)

// RateLimitError reports when another attempt is allowed. It matches ErrTooManyAttempts.
type RateLimitError struct{ RetryAfter time.Duration }

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("auth: too many attempts; retry after %s", e.RetryAfter.Round(time.Second))
}
func (e *RateLimitError) Is(target error) bool { return target == ErrTooManyAttempts }

type userKey struct{}

// WithUser returns a context carrying user.
func WithUser(ctx context.Context, user Authenticatable) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// User returns the authenticated user from ctx, or nil.
func User(ctx context.Context) Authenticatable {
	user, _ := ctx.Value(userKey{}).(Authenticatable)
	return user
}

// UserAs returns the authenticated user as T, e.g. UserAs[*models.User](ctx).
func UserAs[T Authenticatable](ctx context.Context) (T, bool) {
	user, ok := User(ctx).(T)
	return user, ok
}

// Check reports whether ctx carries an authenticated user.
func Check(ctx context.Context) bool { return User(ctx) != nil }
