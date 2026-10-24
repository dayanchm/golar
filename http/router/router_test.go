package router

import (
	"io"
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

	if rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf(
			"expected Allow: GET, HEAD, got %q",
			rec.Header().Get("Allow"),
		)
	}
}

func TestRouterMethods(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantAllow  string
		wantID     string
	}{
		{"registered GET", http.MethodGet, "/users", http.StatusOK, "", ""},
		{"registered POST after GET", http.MethodPost, "/users", http.StatusCreated, "", ""},
		{"all matching methods", http.MethodDelete, "/users", http.StatusMethodNotAllowed, "GET, HEAD, POST", ""},
		{"unknown path", http.MethodGet, "/unknown", http.StatusNotFound, "", ""},
		{"unknown path with unsupported method", http.MethodDelete, "/unknown", http.StatusNotFound, "", ""},
		{"parameterized route method mismatch", http.MethodPost, "/users/123", http.StatusMethodNotAllowed, "GET, HEAD", ""},
		{"HEAD falls back to GET", http.MethodHead, "/users", http.StatusOK, "", ""},
		{"HEAD on parameterized GET", http.MethodHead, "/users/7", http.StatusOK, "", "7"},
		{"HEAD on non-GET route", http.MethodHead, "/posts", http.StatusMethodNotAllowed, "PUT", ""},
		{"HEAD on unknown path", http.MethodHead, "/unknown", http.StatusNotFound, "", ""},
		{"parameterized route dispatch", http.MethodGet, "/users/123", http.StatusOK, "", "123"},
		{"missing parameter segment", http.MethodGet, "/users/123/posts", http.StatusNotFound, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := New()
			app.Get("/users", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			})
			app.Post("/users", func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusCreated)
			})
			app.Get("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-User-ID", r.PathValue("id"))
				w.WriteHeader(http.StatusOK)
			})
			app.Put("/posts", func(w http.ResponseWriter, r *http.Request) {
				t.Error("unrelated route handler should not run")
			})

			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
			if got := rec.Header().Get("Allow"); got != tt.wantAllow {
				t.Errorf("Allow = %q, want %q", got, tt.wantAllow)
			}
			if got := rec.Header().Get("X-User-ID"); got != tt.wantID {
				t.Errorf("user ID = %q, want %q", got, tt.wantID)
			}
		})
	}
}

func TestHeadPrefersExplicitRoute(t *testing.T) {
	app := New()
	app.Get("/files", func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Route", "get") })
	app.Handle(http.MethodHead, "/files", func(w http.ResponseWriter, r *http.Request) { w.Header().Set("X-Route", "head") })
	rec := httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/files", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("X-Route") != "head" {
		t.Fatalf("explicit HEAD route must win: %d %q", rec.Code, rec.Header().Get("X-Route"))
	}
	rec = httptest.NewRecorder()
	app.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/files", nil))
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("Allow must not repeat HEAD: %d %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestHeadOverRealServerHasNoBody(t *testing.T) {
	app := New()
	app.Get("/users", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total", "2")
		w.Write([]byte("ada,bob"))
	})
	server := httptest.NewServer(app)
	defer server.Close()
	res, err := http.Head(server.URL + "/users")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || res.Header.Get("X-Total") != "2" || len(body) != 0 {
		t.Fatalf("HEAD: %d %v %q", res.StatusCode, res.Header, body)
	}
}
