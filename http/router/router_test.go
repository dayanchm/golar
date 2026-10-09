package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMethodNotAllowed(t *testing.T) {
	app := New()

	app.Get("/users", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(
		http.MethodPost,
		"/users",
		nil,
	)

	rec := httptest.NewRecorder()

	app.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf(
			"expected 405, got %d",
			rec.Code,
		)
	}

	if rec.Header().Get("Allow") != "GET" {
		t.Fatalf(
			"expected Allow: GET, got %q",
			rec.Header().Get("Allow"),
		)
	}
}
