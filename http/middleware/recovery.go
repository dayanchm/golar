package middleware

import (
	"log"
	"net/http"
	"runtime/debug"
)

// Recovery catches panics, logs a stack trace and returns a generic 500.
// A panic after a response is committed aborts the connection via net/http.
func Recovery(loggers ...*log.Logger) Middleware {
	logger := log.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &responseWriter{ResponseWriter: w}
			defer func() {
				if value := recover(); value != nil {
					if value == http.ErrAbortHandler {
						panic(value)
					}
					logger.Printf("panic method=%s path=%q: %v\n%s", r.Method, r.URL.Path, value, debug.Stack())
					if rec.status != 0 {
						panic(http.ErrAbortHandler)
					}
					http.Error(rec, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(rec, r)
		})
	}
}
