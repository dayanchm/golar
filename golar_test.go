package golar_test

import (
	"github.com/dayanchm/golar"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicAPI(t *testing.T) {
	app := golar.New()
	app.Get("/", func(c *golar.Context) error { return c.JSON(http.StatusOK, map[string]string{"framework": "Golar"}) })
	app.Post("/standard", golar.Adapt(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })))
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || rec.Body.String() != `{"framework":"Golar"}` {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
}
