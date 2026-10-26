package middleware

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CORSConfig configures CORS. Origins are compared exactly, such as
// "https://app.example.com"; "*" allows any origin without credentials.
type CORSConfig struct {
	AllowOrigins []string
	// AllowOriginFunc, when set, is consulted for origins not in AllowOrigins.
	AllowOriginFunc func(origin string) bool
	// AllowMethods defaults to GET, HEAD, POST, PUT, PATCH and DELETE.
	AllowMethods []string
	// AllowHeaders defaults to Accept, Authorization, Content-Type,
	// X-CSRF-Token and X-Requested-With.
	AllowHeaders  []string
	ExposeHeaders []string
	// AllowCredentials lets browsers send cookies and HTTP authentication.
	// It cannot be combined with "*".
	AllowCredentials bool
	// MaxAge lets browsers cache preflight results; zero omits the header.
	MaxAge time.Duration
}

// CORS answers preflight requests and adds CORS headers for allowed origins.
// Requests from other origins proceed without CORS headers, so browsers block
// their responses. It panics if AllowCredentials is combined with "*".
func CORS(config CORSConfig) Middleware {
	wildcard := slices.Contains(config.AllowOrigins, "*")
	if wildcard && config.AllowCredentials {
		panic(`middleware: CORS cannot allow credentials for origin "*"`)
	}
	methods := config.AllowMethods
	if len(methods) == 0 {
		methods = []string{"GET", "HEAD", "POST", "PUT", "PATCH", "DELETE"}
	}
	headers := config.AllowHeaders
	if len(headers) == 0 {
		headers = []string{"Accept", "Authorization", "Content-Type", "X-CSRF-Token", "X-Requested-With"}
	}
	allowMethods, allowHeaders := strings.Join(methods, ", "), strings.Join(headers, ", ")
	expose := strings.Join(config.ExposeHeaders, ", ")
	maxAge := ""
	if config.MaxAge > 0 {
		maxAge = strconv.Itoa(int(config.MaxAge.Seconds()))
	}
	allowed := func(origin string) bool {
		return wildcard || slices.Contains(config.AllowOrigins, origin) ||
			config.AllowOriginFunc != nil && config.AllowOriginFunc(origin)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Add("Vary", "Origin")
			origin := r.Header.Get("Origin")
			preflight := r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
			if preflight {
				h.Add("Vary", "Access-Control-Request-Method")
				h.Add("Vary", "Access-Control-Request-Headers")
			}
			if origin == "" || !allowed(origin) {
				if preflight {
					w.WriteHeader(http.StatusNoContent)
					return
				}
				next.ServeHTTP(w, r)
				return
			}
			if wildcard {
				h.Set("Access-Control-Allow-Origin", "*")
			} else {
				h.Set("Access-Control-Allow-Origin", origin)
			}
			if config.AllowCredentials {
				h.Set("Access-Control-Allow-Credentials", "true")
			}
			if !preflight {
				if expose != "" {
					h.Set("Access-Control-Expose-Headers", expose)
				}
				next.ServeHTTP(w, r)
				return
			}
			h.Set("Access-Control-Allow-Methods", allowMethods)
			h.Set("Access-Control-Allow-Headers", allowHeaders)
			if maxAge != "" {
				h.Set("Access-Control-Max-Age", maxAge)
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
}
