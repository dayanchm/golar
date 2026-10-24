package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"
)

// ResetStore keeps one password reset token hash per user ID.
type ResetStore interface {
	// Put replaces any previous token for userID.
	Put(ctx context.Context, userID, tokenHash string, created time.Time) error
	// Get returns ErrInvalidToken when userID has no token.
	Get(ctx context.Context, userID string) (tokenHash string, created time.Time, err error)
	Delete(ctx context.Context, userID string) error
}

var (
	ErrThrottled    = errors.New("auth: please wait before retrying")
	ErrWeakPassword = errors.New("auth: password is too short")
)

// PasswordBroker issues single-use password reset tokens and resets passwords.
// Provider must implement PasswordUpdater.
type PasswordBroker struct {
	Provider UserProvider
	Store    ResetStore
	Hasher   Hasher
	// Send delivers token to the user, e.g. as a link by email. Required.
	Send func(ctx context.Context, user Authenticatable, token string) error
	// OnReset runs after a successful reset, e.g. to revoke API tokens.
	OnReset func(ctx context.Context, user Authenticatable) error
	// TTL defaults to 1 hour, Throttle (minimum time between links) to 1
	// minute and MinLength (in characters) to 8.
	TTL, Throttle time.Duration
	MinLength     int
}

func (b *PasswordBroker) ttl() time.Duration { return or(b.TTL, time.Hour) }

func or[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}

// SendResetLink creates a token and passes it to Send. Unknown identifiers
// return nil so callers cannot probe which accounts exist; show the same
// message in both cases. A second link within Throttle returns ErrThrottled.
func (b *PasswordBroker) SendResetLink(ctx context.Context, identifier string) error {
	if b.Send == nil {
		return errors.New("auth: PasswordBroker.Send is required")
	}
	user, err := b.Provider.RetrieveByIdentifier(ctx, identifier)
	if errors.Is(err, ErrUserNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if _, created, err := b.Store.Get(ctx, user.AuthID()); err == nil && time.Since(created) < or(b.Throttle, time.Minute) {
		return ErrThrottled
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	if err := b.Store.Put(ctx, user.AuthID(), tokenID(token), time.Now().UTC()); err != nil {
		return err
	}
	return b.Send(ctx, user, token)
}

// Reset sets a new password when token is the latest, unexpired token for the
// user identified by identifier. Tokens are single use.
func (b *PasswordBroker) Reset(ctx context.Context, identifier, token, password string) error {
	if utf8.RuneCountInString(password) < or(b.MinLength, 8) {
		return ErrWeakPassword
	}
	updater, ok := b.Provider.(PasswordUpdater)
	if !ok {
		return fmt.Errorf("auth: %T does not implement PasswordUpdater", b.Provider)
	}
	user, err := b.Provider.RetrieveByIdentifier(ctx, identifier)
	if errors.Is(err, ErrUserNotFound) {
		return ErrInvalidToken
	}
	if err != nil {
		return err
	}
	stored, created, err := b.Store.Get(ctx, user.AuthID())
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(tokenID(token)), []byte(stored)) != 1 {
		return ErrInvalidToken
	}
	if time.Since(created) > b.ttl() {
		_ = b.Store.Delete(ctx, user.AuthID())
		return ErrExpiredToken
	}
	hash, err := hasher(b.Hasher).Hash(password)
	if err != nil {
		return err
	}
	// Consume the token first so a concurrent request cannot reuse it.
	if err := b.Store.Delete(ctx, user.AuthID()); err != nil {
		return err
	}
	if err := updater.UpdatePassword(ctx, user, hash); err != nil {
		return err
	}
	if b.OnReset != nil {
		return b.OnReset(ctx, user)
	}
	return nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// MemoryResetStore keeps reset tokens in process memory.
type MemoryResetStore struct {
	mu     sync.Mutex
	tokens map[string]resetEntry
}

type resetEntry struct {
	hash    string
	created time.Time
}

func NewMemoryResetStore() *MemoryResetStore {
	return &MemoryResetStore{tokens: map[string]resetEntry{}}
}

func (s *MemoryResetStore) Put(_ context.Context, userID, hash string, created time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[userID] = resetEntry{hash, created}
	return nil
}

func (s *MemoryResetStore) Get(_ context.Context, userID string) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.tokens[userID]
	if !ok {
		return "", time.Time{}, ErrInvalidToken
	}
	return entry.hash, entry.created, nil
}

func (s *MemoryResetStore) Delete(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, userID)
	return nil
}
