package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Limiter allows Max attempts per key in a fixed Window. It is safe for
// concurrent use and keeps state in process memory.
type Limiter struct {
	Max    int
	Window time.Duration

	mu      sync.Mutex
	windows map[string]*window
	pruned  int
	now     func() time.Time
}

type window struct {
	count int
	reset time.Time
}

// NewLimiter allows max attempts per key within each window.
func NewLimiter(max int, per time.Duration) *Limiter {
	return &Limiter{Max: max, Window: per, windows: map[string]*window{}, now: time.Now}
}

func (l *Limiter) current(key string) *window {
	now := l.now()
	if len(l.windows) > 2*l.pruned+1024 { // prune expired windows as the map grows
		for k, w := range l.windows {
			if !now.Before(w.reset) {
				delete(l.windows, k)
			}
		}
		l.pruned = len(l.windows)
	}
	w := l.windows[key]
	if w == nil || !now.Before(w.reset) {
		w = &window{reset: now.Add(l.Window)}
		l.windows[key] = w
	}
	return w
}

// Allow records an attempt and reports whether it is within the limit. When it
// is not, retryAfter is the time until the window resets.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.current(key)
	if w.count >= l.Max {
		return false, w.reset.Sub(l.now())
	}
	w.count++
	return true, 0
}

// Hit records an attempt without checking the limit.
func (l *Limiter) Hit(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.current(key).count++
}

// Blocked reports whether key has used all attempts, and for how long.
func (l *Limiter) Blocked(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	w := l.current(key)
	if w.count >= l.Max {
		return true, w.reset.Sub(l.now())
	}
	return false, 0
}

// Clear forgets attempts for key, e.g. after a successful login.
func (l *Limiter) Clear(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.windows, key)
}

// ClientIP returns the request's remote address without the port. It ignores
// X-Forwarded-For, which clients can forge; behind a trusted proxy, pass a key
// function that reads the proxy's header instead.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimit rejects requests over the limit with 429 Too Many Requests and a
// Retry-After header. key defaults to ClientIP.
func RateLimit(l *Limiter, key func(*http.Request) string) func(http.Handler) http.Handler {
	if key == nil {
		key = ClientIP
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ok, retry := l.Allow(key(r)); !ok {
				w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds()+0.999)))
				http.Error(w, http.StatusText(http.StatusTooManyRequests), http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
