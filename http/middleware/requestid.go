package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"
)

// RequestIDConfig configures RequestID.
type RequestIDConfig struct {
	// Header defaults to X-Request-ID.
	Header string
	// Generate defaults to 16 random bytes in hex.
	Generate func() string
}

type requestIDKey struct{}

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// RequestID reuses a well-formed incoming request ID or generates one, sets it
// on the response and stores it in the request context. Logger includes it.
// Behind a proxy that does not set the header, clients choose their own IDs,
// so treat them as correlation hints, not as trusted values.
func RequestID(configs ...RequestIDConfig) Middleware {
	var config RequestIDConfig
	if len(configs) > 0 {
		config = configs[0]
	}
	if config.Header == "" {
		config.Header = "X-Request-ID"
	}
	if config.Generate == nil {
		config.Generate = randomHex
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get(config.Header)
			if !requestIDPattern.MatchString(id) {
				id = config.Generate()
			}
			w.Header().Set(config.Header, id)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
		})
	}
}

func randomHex() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// RequestIDFrom returns the ID stored by RequestID, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}
