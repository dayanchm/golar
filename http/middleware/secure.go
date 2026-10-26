package middleware

import (
	"net/http"
	"strconv"
	"time"
)

// SecureHeadersConfig lists response headers to set. Empty fields are omitted.
type SecureHeadersConfig struct {
	// HSTSMaxAge sets Strict-Transport-Security on HTTPS requests.
	HSTSMaxAge            time.Duration
	HSTSIncludeSubdomains bool
	HSTSPreload           bool
	// ContentTypeNosniff sets X-Content-Type-Options: nosniff.
	ContentTypeNosniff bool
	// FrameOptions sets X-Frame-Options, such as "DENY" or "SAMEORIGIN".
	FrameOptions   string
	ReferrerPolicy string
	// ContentSecurityPolicy is off by default because a useful policy depends
	// on the application's scripts, styles and API origins.
	ContentSecurityPolicy string
}

// DefaultSecureHeaders is used by SecureHeaders when no config is given.
var DefaultSecureHeaders = SecureHeadersConfig{
	HSTSMaxAge:            365 * 24 * time.Hour,
	HSTSIncludeSubdomains: true,
	ContentTypeNosniff:    true,
	FrameOptions:          "DENY",
	ReferrerPolicy:        "strict-origin-when-cross-origin",
}

// SecureHeaders sets security headers before the handler runs, so handlers
// can still override them. HSTS is sent over TLS or when X-Forwarded-Proto is
// https; browsers ignore it on plain HTTP, so a forged header does no harm.
func SecureHeaders(configs ...SecureHeadersConfig) Middleware {
	config := DefaultSecureHeaders
	if len(configs) > 0 {
		config = configs[0]
	}
	hsts := ""
	if config.HSTSMaxAge > 0 {
		hsts = "max-age=" + strconv.Itoa(int(config.HSTSMaxAge.Seconds()))
		if config.HSTSIncludeSubdomains {
			hsts += "; includeSubDomains"
		}
		if config.HSTSPreload {
			hsts += "; preload"
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			if hsts != "" && (r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https") {
				h.Set("Strict-Transport-Security", hsts)
			}
			if config.ContentTypeNosniff {
				h.Set("X-Content-Type-Options", "nosniff")
			}
			if config.FrameOptions != "" {
				h.Set("X-Frame-Options", config.FrameOptions)
			}
			if config.ReferrerPolicy != "" {
				h.Set("Referrer-Policy", config.ReferrerPolicy)
			}
			if config.ContentSecurityPolicy != "" {
				h.Set("Content-Security-Policy", config.ContentSecurityPolicy)
			}
			next.ServeHTTP(w, r)
		})
	}
}
