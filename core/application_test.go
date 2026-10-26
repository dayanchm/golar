package core

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	httpcontext "github.com/dayanchm/golar/http/context"
	"github.com/dayanchm/golar/http/middleware"
	"github.com/dayanchm/golar/validation"
)

func TestApplicationRoutes(t *testing.T) {
	app := New()
	app.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-App", "Golar"); next.ServeHTTP(w, r) })
	})
	group := app.Group("/api").Group("/v1")
	for method, register := range map[string]func(string, Handler){"GET": group.Get, "POST": group.Post, "PUT": group.Put, "PATCH": group.Patch, "DELETE": group.Delete} {
		register("/users/{id}", func(c *httpcontext.Context) error { return c.String(200, method+":"+c.Param("id")+":"+c.Query("page")) })
	}
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(method, "/api/v1/users/42?page=2", nil))
		if rec.Code != 200 || rec.Body.String() != method+":42:2" || rec.Header().Get("X-App") != "Golar" {
			t.Fatalf("response = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
		}
	}
	for _, tt := range []struct {
		method, path string
		code         int
	}{{"OPTIONS", "/api/v1/users/42", 405}, {"GET", "/missing", 404}} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
		if rec.Code != tt.code || rec.Header().Get("X-App") != "Golar" {
			t.Fatalf("response = %d %v", rec.Code, rec.Header())
		}
		if tt.code == 405 && rec.Header().Get("Allow") != "DELETE, GET, HEAD, PATCH, POST, PUT" {
			t.Fatal(rec.Header())
		}
	}
}

func TestHandlerErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		code int
		body string
	}{
		{"unexpected", errors.New("database password secret"), 500, "Internal Server Error\n"},
		{"http error", &HTTPError{Code: 400, Message: "Invalid name"}, 400, "Invalid name\n"},
		{"wrapped", fmt.Errorf("wrapped: %w", &HTTPError{Code: 404}), 404, "Not Found\n"},
		{"invalid code", &HTTPError{Code: 200, Message: "secret"}, 500, "Internal Server Error\n"},
		{"validation", fmt.Errorf("wrapped: %w", &validation.Error{Errors: validation.Errors{"name": {"The name field is required."}}}), 422, `{"errors":{"name":["The name field is required."]}}`},
		{"bind", &httpcontext.BindError{Err: errors.New("unexpected EOF")}, 400, "Invalid request body\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			app := New()
			app.Get("/", func(*httpcontext.Context) error { return tt.err })
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
			if rec.Code != tt.code || rec.Body.String() != tt.body {
				t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestContextValidate(t *testing.T) {
	type request struct {
		Email string `json:"email" validate:"required,email"`
	}
	app := New()
	app.Post("/", func(c *httpcontext.Context) error {
		var in request
		if err := c.Validate(&in); err != nil {
			return err
		}
		return c.String(201, in.Email)
	})
	for _, tt := range []struct {
		body string
		code int
		want string
	}{
		{`{"email":"ada@example.com"}`, 201, "ada@example.com"},
		{`{"email":"ada"}`, 422, `{"errors":{"email":["The email field must be a valid email address."]}}`},
		{`{"email":`, 400, "Invalid request body\n"},
	} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest("POST", "/", strings.NewReader(tt.body)))
		if rec.Code != tt.code || rec.Body.String() != tt.want {
			t.Errorf("POST %s = %d %q, want %d %q", tt.body, rec.Code, rec.Body.String(), tt.code, tt.want)
		}
	}
}

func TestCustomErrorHandler(t *testing.T) {
	app := New()
	want := errors.New("bad request")
	app.SetErrorHandler(func(c *httpcontext.Context, err error) {
		if !errors.Is(err, want) {
			t.Error(err)
		}
		_ = c.JSON(422, map[string]string{"error": "invalid"})
	})
	app.Post("/", func(*httpcontext.Context) error { return want })
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("POST", "/", nil))
	if rec.Code != 422 || rec.Body.String() != `{"error":"invalid"}` {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	app.SetErrorHandler(nil)
}

func TestErrorAfterResponse(t *testing.T) {
	app := New()
	app.Get("/", func(c *httpcontext.Context) error { _ = c.String(202, "started"); return errors.New("late error") })
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 202 || rec.Body.String() != "started" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
}

func TestAdaptAndRecovery(t *testing.T) {
	app := New()
	app.Use(middleware.Recovery())
	app.Put("/adapt", Adapt(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })))
	app.Patch("/panic", func(*httpcontext.Context) error { panic("secret") })
	app.Delete("/delete", func(c *httpcontext.Context) error { return c.String(200, "deleted") })
	for _, tt := range []struct {
		method, path string
		code         int
	}{{"PUT", "/adapt", 204}, {"PATCH", "/panic", 500}, {"DELETE", "/delete", 200}} {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))
		if rec.Code != tt.code {
			t.Fatal(rec.Code)
		}
	}
}

func TestLifecycle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := New(Config{Addr: "127.0.0.1:0"})
	var order []string
	app.OnStart(func(context.Context) error { order = append(order, "start A"); return nil })
	app.OnStart(func(context.Context) error { order = append(order, "start B"); cancel(); return nil })
	app.OnShutdown(func(ctx context.Context) error {
		if ctx.Err() != nil {
			t.Error("shutdown inherited canceled context")
		}
		order = append(order, "stop A")
		return nil
	})
	app.OnShutdown(func(context.Context) error { order = append(order, "stop B"); return nil })
	if err := app.RunContext(ctx); err != nil {
		t.Fatal(err)
	}
	if want := []string{"start A", "start B", "stop B", "stop A"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v", order)
	}
	if err := app.RunContext(ctx); err == nil {
		t.Fatal("expected second run to fail")
	}
}

func TestLifecycleErrors(t *testing.T) {
	startErr, stopErr := errors.New("startup failed"), errors.New("cleanup failed")
	app := New()
	app.OnStart(func(context.Context) error { return startErr })
	app.OnStart(func(context.Context) error { t.Error("later startup ran"); return nil })
	app.OnShutdown(func(context.Context) error { return stopErr })
	err := app.Run("127.0.0.1:0")
	if !errors.Is(err, startErr) || !errors.Is(err, stopErr) {
		t.Fatal(err)
	}
}

func TestListenFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	app := New()
	app.OnStart(func(context.Context) error { t.Error("startup ran on bind failure"); return nil })
	if err := app.Run(listener.Addr().String()); err == nil {
		t.Fatal("expected listen error")
	}
}

func TestConcurrentRequests(t *testing.T) {
	app := New()
	app.Get("/users/{id}", func(c *httpcontext.Context) error { return c.String(200, c.Param("id")) })
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest("GET", "/users/42", nil))
			if rec.Body.String() != "42" {
				t.Error(rec.Body.String())
			}
		}()
	}
	wg.Wait()
}
