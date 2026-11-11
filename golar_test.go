package golar_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dayanchm/golar"
	httpcontext "github.com/dayanchm/golar/http/context"
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

// The aliases are the inner packages' types, so either name works.
func TestContextAliases(t *testing.T) {
	app := golar.New()
	app.SetContextConfig(golar.ContextConfig{MaxBodySize: 4})
	app.Post("/", func(c *golar.Context) error {
		var in struct{ Name string }
		return c.Bind(&in)
	})
	app.Get("/n", func(c *golar.Context) error {
		_, err := c.QueryInt("n", 0)
		var input *golar.InputError
		if !errors.As(err, &input) {
			t.Errorf("QueryInt error = %T", err)
		}
		return golar.ErrForbidden
	})
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(`{"name":"too long"}`)))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("ContextConfig limit = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/n?n=x", nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("ErrForbidden = %d", rec.Code)
	}
	if !errors.Is(golar.ErrUnsupportedMediaType, httpcontext.ErrUnsupportedMediaType) {
		t.Error("ErrUnsupportedMediaType is not the context package's error")
	}
}
