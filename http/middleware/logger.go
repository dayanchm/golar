package middleware

import (
	"log"
	"net/http"
	"time"
)

// Logger logs method, path, status, response bytes and elapsed time, plus the
// request ID when RequestID runs before or after it.
// An optional logger can be supplied; otherwise log.Default is used.
func Logger(loggers ...*log.Logger) Middleware {
	logger := log.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &responseWriter{ResponseWriter: w}
			defer func() {
				status := rec.status
				if status == 0 {
					status = http.StatusOK
				}
				id := RequestIDFrom(r.Context())
				if id == "" {
					id = rec.Header().Get("X-Request-ID")
				}
				if id != "" {
					logger.Printf("method=%s path=%q status=%d bytes=%d duration=%s request_id=%s", r.Method, r.URL.Path, status, rec.bytes, time.Since(start), id)
					return
				}
				logger.Printf("method=%s path=%q status=%d bytes=%d duration=%s", r.Method, r.URL.Path, status, rec.bytes, time.Since(start))
			}()
			next.ServeHTTP(rec, r)
		})
	}
}
