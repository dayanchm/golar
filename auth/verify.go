package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// MustVerifyEmail is implemented by users who must confirm their email address.
type MustVerifyEmail interface {
	Authenticatable
	AuthEmail() string
	EmailVerified() bool
}

// EmailVerifiedMarker is implemented by providers that can record verification.
type EmailVerifiedMarker interface {
	MarkEmailVerified(ctx context.Context, user Authenticatable) error
}

// EmailVerification issues and checks stateless, HMAC-signed verification
// tokens. A token is bound to the user's ID and current email address, so it
// stops working if the address changes. Provider must implement EmailVerifiedMarker.
type EmailVerification struct {
	// Secret signs tokens. It must be at least 32 random bytes and kept private.
	Secret   []byte
	Provider UserProvider
	// Send delivers the token, e.g. as a link by email.
	Send func(ctx context.Context, user MustVerifyEmail, token string) error
	TTL  time.Duration // defaults to 24 hours
}

var errShortSecret = errors.New("auth: EmailVerification.Secret must be at least 32 bytes")

func (v *EmailVerification) sign(payload string) string {
	mac := hmac.New(sha256.New, v.Secret)
	mac.Write([]byte("golar-email-verification:" + payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func emailDigest(email string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(email))))
	return base64.RawURLEncoding.EncodeToString(sum[:16])
}

// Token returns a signed verification token for user.
func (v *EmailVerification) Token(user MustVerifyEmail) (string, error) {
	if len(v.Secret) < 32 {
		return "", errShortSecret
	}
	expires := time.Now().Add(or(v.TTL, 24*time.Hour)).Unix()
	payload := base64.RawURLEncoding.EncodeToString([]byte(user.AuthID())) + "." + strconv.FormatInt(expires, 10) + "." + emailDigest(user.AuthEmail())
	return payload + "." + v.sign(payload), nil
}

// SendVerification creates a token for user and passes it to Send.
func (v *EmailVerification) SendVerification(ctx context.Context, user MustVerifyEmail) error {
	if v.Send == nil {
		return errors.New("auth: EmailVerification.Send is required")
	}
	if user.EmailVerified() {
		return nil
	}
	token, err := v.Token(user)
	if err != nil {
		return err
	}
	return v.Send(ctx, user, token)
}

// Verify checks token and marks the user's email as verified. It returns the
// user, ErrInvalidToken for forged or mismatched tokens, or ErrExpiredToken.
func (v *EmailVerification) Verify(ctx context.Context, token string) (MustVerifyEmail, error) {
	if len(v.Secret) < 32 {
		return nil, errShortSecret
	}
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return nil, ErrInvalidToken
	}
	payload := strings.Join(parts[:3], ".")
	if !hmac.Equal([]byte(parts[3]), []byte(v.sign(payload))) {
		return nil, ErrInvalidToken
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, ErrInvalidToken
	}
	if time.Now().Unix() > expires {
		return nil, ErrExpiredToken
	}
	id, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalidToken
	}
	found, err := v.Provider.RetrieveByID(ctx, string(id))
	if errors.Is(err, ErrUserNotFound) {
		return nil, ErrInvalidToken
	}
	if err != nil {
		return nil, err
	}
	user, ok := found.(MustVerifyEmail)
	if !ok {
		return nil, fmt.Errorf("auth: %T does not implement MustVerifyEmail", found)
	}
	if !hmac.Equal([]byte(emailDigest(user.AuthEmail())), []byte(parts[2])) {
		return nil, ErrInvalidToken
	}
	if user.EmailVerified() {
		return user, nil
	}
	marker, ok := v.Provider.(EmailVerifiedMarker)
	if !ok {
		return nil, fmt.Errorf("auth: %T does not implement EmailVerifiedMarker", v.Provider)
	}
	return user, marker.MarkEmailVerified(ctx, user)
}

// RequireVerified rejects users whose email is unverified with 403 Forbidden.
// Users that do not implement MustVerifyEmail pass. Install it after Required.
func RequireVerified(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := User(r.Context())
		if user == nil {
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		if u, ok := user.(MustVerifyEmail); ok && !u.EmailVerified() {
			http.Error(w, "Email address is not verified", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}
