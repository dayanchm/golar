package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// Token is a stored API token. The plaintext is only returned by Issue.
type Token struct {
	ID        string // SHA-256 of the plaintext, hex encoded; safe to store and log
	UserID    string
	Name      string
	Abilities []string
	ExpiresAt *time.Time
	CreatedAt time.Time
}

// Can reports whether the token grants ability. "*" grants everything and
// "posts:*" grants every "posts:" ability.
func (t Token) Can(ability string) bool {
	for _, granted := range t.Abilities {
		if granted == "*" || granted == ability || strings.HasSuffix(granted, ":*") && strings.HasPrefix(ability, strings.TrimSuffix(granted, "*")) {
			return true
		}
	}
	return false
}

// TokenStore persists tokens by ID. Find returns ErrInvalidToken when missing.
type TokenStore interface {
	Create(ctx context.Context, token Token) error
	Find(ctx context.Context, id string) (Token, error)
	Delete(ctx context.Context, id string) error
	DeleteUser(ctx context.Context, userID string) error
}

// tokenPrefix makes leaked tokens easy to recognize in logs and secret scanners.
const tokenPrefix = "golar_"

func tokenID(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// TokenGuard authenticates "Authorization: Bearer <token>" requests.
type TokenGuard struct {
	Provider UserProvider
	Store    TokenStore
}

// Issue creates a token for user. A zero ttl never expires. Only the returned
// plaintext can authenticate; the store keeps its SHA-256 hash.
func (g *TokenGuard) Issue(ctx context.Context, user Authenticatable, name string, abilities []string, ttl time.Duration) (string, Token, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", Token{}, err
	}
	plaintext := tokenPrefix + base64.RawURLEncoding.EncodeToString(secret)
	token := Token{ID: tokenID(plaintext), UserID: user.AuthID(), Name: name, Abilities: slices.Clone(abilities), CreatedAt: time.Now().UTC().Truncate(time.Second)}
	if ttl > 0 {
		expires := token.CreatedAt.Add(ttl)
		token.ExpiresAt = &expires
	}
	if err := g.Store.Create(ctx, token); err != nil {
		return "", Token{}, err
	}
	return plaintext, token, nil
}

// Revoke deletes the token with plaintext.
func (g *TokenGuard) Revoke(ctx context.Context, plaintext string) error {
	return g.Store.Delete(ctx, tokenID(plaintext))
}

type tokenKey struct{}

// TokenFrom returns the token that authenticated ctx's request.
func TokenFrom(ctx context.Context) (Token, bool) {
	token, ok := ctx.Value(tokenKey{}).(Token)
	return token, ok
}

func bearer(r *http.Request) string {
	scheme, value, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return ""
	}
	return strings.TrimSpace(value)
}

// Authenticate resolves the bearer token's user. It returns (nil, nil) without
// an Authorization header, and ErrInvalidToken or ErrExpiredToken otherwise.
func (g *TokenGuard) Authenticate(r *http.Request) (Authenticatable, error) {
	user, _, err := g.authenticate(r)
	return user, err
}

func (g *TokenGuard) authenticate(r *http.Request) (Authenticatable, Token, error) {
	plaintext := bearer(r)
	if plaintext == "" {
		return nil, Token{}, nil
	}
	if !strings.HasPrefix(plaintext, tokenPrefix) || len(plaintext) > 128 {
		return nil, Token{}, ErrInvalidToken
	}
	// Lookup by SHA-256 of a 256-bit secret: equality on the hash leaks nothing useful.
	token, err := g.Store.Find(r.Context(), tokenID(plaintext))
	if err != nil {
		return nil, Token{}, err
	}
	if token.ExpiresAt != nil && !time.Now().Before(*token.ExpiresAt) {
		return nil, Token{}, ErrExpiredToken
	}
	user, err := g.Provider.RetrieveByID(r.Context(), token.UserID)
	if errors.Is(err, ErrUserNotFound) {
		return nil, Token{}, ErrInvalidToken
	}
	return user, token, err
}

// MemoryTokenStore keeps tokens in process memory, for tests and development.
type MemoryTokenStore struct {
	mu     sync.Mutex
	tokens map[string]Token
}

func NewMemoryTokenStore() *MemoryTokenStore { return &MemoryTokenStore{tokens: map[string]Token{}} }

func (s *MemoryTokenStore) Create(_ context.Context, token Token) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[token.ID] = token
	return nil
}

func (s *MemoryTokenStore) Find(_ context.Context, id string) (Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, ok := s.tokens[id]
	if !ok {
		return Token{}, ErrInvalidToken
	}
	return token, nil
}

func (s *MemoryTokenStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, id)
	return nil
}

func (s *MemoryTokenStore) DeleteUser(_ context.Context, userID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, token := range s.tokens {
		if token.UserID == userID {
			delete(s.tokens, id)
		}
	}
	return nil
}
