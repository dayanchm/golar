package auth

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestLimiter(t *testing.T) {
	clock := time.Unix(0, 0)
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return clock }
	for i := range 3 {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("attempt %d rejected", i)
		}
	}
	if ok, retry := l.Allow("k"); ok || retry != time.Minute {
		t.Fatalf("4th attempt: %v %s", ok, retry)
	}
	if ok, _ := l.Allow("other"); !ok {
		t.Fatal("keys are independent")
	}
	clock = clock.Add(30 * time.Second)
	if blocked, retry := l.Blocked("k"); !blocked || retry != 30*time.Second {
		t.Fatalf("blocked: %v %s", blocked, retry)
	}
	clock = clock.Add(30 * time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Fatal("window must reset")
	}
	l.Hit("k")
	l.Hit("k")
	l.Clear("k")
	if blocked, _ := l.Blocked("k"); blocked {
		t.Fatal("clear")
	}
	for i := range 5000 { // prune keeps memory bounded
		l.Hit(string(rune(i)))
	}
	clock = clock.Add(time.Hour)
	for i := range 3000 { // growth past the threshold triggers a prune of the expired keys
		l.Hit("new" + string(rune(i)))
	}
	if len(l.windows) > 3000 {
		t.Fatalf("expired windows not pruned: %d", len(l.windows))
	}
}

func TestLimiterConcurrent(t *testing.T) {
	l := NewLimiter(50, time.Hour)
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 200 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ok, _ := l.Allow("k"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if allowed != 50 {
		t.Fatalf("allowed %d, want exactly 50", allowed)
	}
}

func TestRateLimitMiddleware(t *testing.T) {
	h := RateLimit(NewLimiter(1, time.Minute), nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	request := func(ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		r.RemoteAddr = ip + ":1234"
		r.Header.Set("X-Forwarded-For", "10.0.0.1") // must not be trusted
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request("1.1.1.1"); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	if w := request("1.1.1.1"); w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("%d %q", w.Code, w.Header().Get("Retry-After"))
	}
	if w := request("2.2.2.2"); w.Code != http.StatusOK {
		t.Fatal("limits are per client IP")
	}
}
