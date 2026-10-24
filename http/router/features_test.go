package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDuplicateRoutes(t *testing.T) {
	for _, path := range []string{"/users", "/users/", "users"} {
		t.Run(path, func(t *testing.T) {
			app := New()
			handler := func(http.ResponseWriter, *http.Request) {}
			app.Get("/users", handler)
			app.Post("/users", handler)
			app.Get("/posts", handler)
			defer func() {
				if recover() == nil {
					t.Fatal("expected duplicate registration to panic")
				}
			}()
			app.Get(path, handler)
		})
	}
}

func TestRoutePriority(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		app := New()
		paths := []string{"/users/{id}", "/users/new", "/users/{id}/posts/{post}", "/users/{id}/posts/new"}
		if reverse {
			for i, j := 0, len(paths)-1; i < j; i, j = i+1, j-1 {
				paths[i], paths[j] = paths[j], paths[i]
			}
		}
		for _, path := range paths {
			app.Get(path, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Route", path)
			})
		}
		for path, want := range map[string]string{
			"/users/new":          "/users/new",
			"/users/42":           "/users/{id}",
			"/users/42/posts/new": "/users/{id}/posts/new",
			"/users/42/posts/7":   "/users/{id}/posts/{post}",
		} {
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if got := rec.Header().Get("X-Route"); got != want {
				t.Errorf("reverse=%v path=%s: route=%q, want %q", reverse, path, got, want)
			}
		}
	}
}

func TestMethodHelpersAndGroups(t *testing.T) {
	for _, grouped := range []bool{false, true} {
		app := New()
		var get, post, put, patch, delete func(string, http.HandlerFunc)
		path := "/users/42"
		if grouped {
			group := app.Group("/api/").Group("/v1/")
			get, post, put, patch, delete = group.Get, group.Post, group.Put, group.Patch, group.Delete
			path = "/api/v1/users/42"
		} else {
			get, post, put, patch, delete = app.Get, app.Post, app.Put, app.Patch, app.Delete
		}
		for method, register := range map[string]func(string, http.HandlerFunc){
			http.MethodGet: get, http.MethodPost: post, http.MethodPut: put,
			http.MethodPatch: patch, http.MethodDelete: delete,
		} {
			register("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-Method", method)
				w.Header().Set("X-ID", r.PathValue("id"))
			})
		}
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
			if method == "OPTIONS" {
				if rec.Code != 405 || rec.Header().Get("Allow") != "DELETE, GET, HEAD, PATCH, POST, PUT" {
					t.Fatalf("grouped=%v: unexpected method rejection: %d %v", grouped, rec.Code, rec.Header())
				}
			} else if rec.Code != 200 || rec.Header().Get("X-Method") != method || rec.Header().Get("X-ID") != "42" {
				t.Fatalf("grouped=%v method=%s: unexpected response: %d %v", grouped, method, rec.Code, rec.Header())
			}
		}
	}
}

func TestGroupRootAndDuplicate(t *testing.T) {
	app := New()
	group := app.Group("/api/")
	group.Get("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest("GET", "/api", nil))
	if rec.Code != 204 {
		t.Fatalf("group root status = %d", rec.Code)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected duplicate across group and router to panic")
		}
	}()
	app.Get("/api", func(http.ResponseWriter, *http.Request) {})
}

func TestRoutesSnapshot(t *testing.T) {
	app := New()
	app.Get("/users", func(http.ResponseWriter, *http.Request) {})
	app.Group("/api").Post("/users", func(http.ResponseWriter, *http.Request) {})
	routes := app.Routes()
	if len(routes) != 2 {
		t.Fatal(routes)
	}
	found := false
	for _, route := range routes {
		if route.Method == "POST" && route.Path == "/api/users" {
			found = true
		}
	}
	if !found {
		t.Fatal(routes)
	}
	routes[0].Path = "changed"
	if app.Routes()[0].Path == "changed" {
		t.Fatal("snapshot mutated router")
	}
}
