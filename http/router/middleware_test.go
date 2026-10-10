package router

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/dayanchm/golar/http/middleware"
)

func TestGlobalMiddleware(t *testing.T) {
	app := New()
	var order []string
	wrap := func(name string) middleware.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+" before")
				next.ServeHTTP(w, r)
				order = append(order, name+" after")
			})
		}
	}
	app.Use(wrap("A"))
	app.Use(wrap("B"))
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	for _, tt := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/users", 204}, {"POST", "/users", 405}, {"GET", "/unknown", 404},
	} {
		order = nil
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
		if rec.Code != tt.status {
			t.Errorf("status = %d, want %d", rec.Code, tt.status)
		}
		if want := []string{"A before", "B before", "B after", "A after"}; !reflect.DeepEqual(order, want) {
			t.Errorf("order = %v", order)
		}
	}
}
