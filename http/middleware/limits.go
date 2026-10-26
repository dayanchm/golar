package middleware

import (
	"context"
	"errors"
	"net/http"
	"time"
)

// Timeout gives each request a context that is canceled after d. Handlers and
// the database calls they make must honor r.Context(); Timeout does not stop
// a handler that ignores it. If the deadline passes before the handler writes
// a response, the client receives 503 Service Unavailable.
func Timeout(d time.Duration) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			rec := &responseWriter{ResponseWriter: w}
			next.ServeHTTP(rec, r.WithContext(ctx))
			if rec.status == 0 && errors.Is(ctx.Err(), context.DeadlineExceeded) {
				http.Error(rec, http.StatusText(http.StatusServiceUnavailable), http.StatusServiceUnavailable)
			}
		})
	}
}

// BodyLimit rejects request bodies larger than limit bytes. Requests that
// declare a larger Content-Length get 413 immediately; other bodies fail with
// *http.MaxBytesError once the limit is read, which the default Golar error
// handler also answers with 413.
func BodyLimit(limit int64) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > limit {
				http.Error(w, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}
