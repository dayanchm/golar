package context

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParam(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users/42", nil)
	req.SetPathValue("id", "42")

	rec := httptest.NewRecorder()
	c := New(rec, req)

	if got := c.Param("id"); got != "42" {
		t.Errorf("Param(id) = %q, want %q", got, "42")
	}

	if got := c.Param("missing"); got != "" {
		t.Errorf("Param(missing) = %q, want empty string", got)
	}
}

func TestQuery(t *testing.T) {
	c := New(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/users?page=2&name=Ali+Veli", nil))
	for name, want := range map[string]string{"page": "2", "name": "Ali Veli", "missing": ""} {
		if got := c.Query(name); got != want {
			t.Errorf("Query(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.JSON(http.StatusCreated, map[string]string{"name": "Ali"}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
	if got := rec.Result().Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "Ali" {
		t.Errorf("body = %v, want name=Ali", body)
	}
}

func TestJSONEncodingError(t *testing.T) {
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.JSON(http.StatusCreated, make(chan int)); err == nil {
		t.Fatal("expected an encoding error")
	}
	// A failed encoding must leave the response available for an error handler.
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Fatal("encoding error wrote a response")
	}
	c.Status(http.StatusInternalServerError)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

func TestString(t *testing.T) {
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.String(http.StatusAccepted, "Merhaba dünya"); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rec.Code)
	}
	if got := rec.Result().Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := rec.Body.String(); got != "Merhaba dünya" {
		t.Errorf("body = %q", got)
	}
}

func TestBindJSON(t *testing.T) {
	for _, tt := range []struct {
		name      string
		body      string
		wantError bool
	}{
		{"valid", `{"name":"Ali"}`, false},
		{"malformed", `{"name":`, true},
		{"empty", "", true},
		{"wrong field type", `{"name":42}`, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := New(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(tt.body)))
			var user struct {
				Name string `json:"name"`
			}
			err := c.BindJSON(&user)
			if (err != nil) != tt.wantError {
				t.Fatalf("BindJSON error = %v, wantError = %v", err, tt.wantError)
			}
			if !tt.wantError && user.Name != "Ali" {
				t.Errorf("name = %q, want Ali", user.Name)
			}
		})
	}
}

func TestHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	c.Header("X-App", "old")
	c.Header("X-App", "Golar")
	c.Status(http.StatusOK)
	values := rec.Result().Header.Values("X-App")
	if len(values) != 1 || values[0] != "Golar" {
		t.Errorf("X-App = %v, want [Golar]", values)
	}
}

func TestStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	c.Status(http.StatusNoContent)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("response = %d %q, want 204 with no body", rec.Code, rec.Body.String())
	}
}

type failingWriter struct {
	header http.Header
	err    error
}

func (w *failingWriter) Header() http.Header       { return w.header }
func (w *failingWriter) WriteHeader(int)           {}
func (w *failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestResponseWriteErrors(t *testing.T) {
	for _, format := range []string{"JSON", "String"} {
		t.Run(format, func(t *testing.T) {
			want := io.ErrClosedPipe
			w := &failingWriter{header: make(http.Header), err: want}
			c := New(w, httptest.NewRequest(http.MethodGet, "/", nil))
			var err error
			if format == "JSON" {
				err = c.JSON(http.StatusOK, map[string]string{"name": "Ali"})
			} else {
				err = c.String(http.StatusOK, "hello")
			}
			if !errors.Is(err, want) {
				t.Errorf("error = %v, want %v", err, want)
			}
		})
	}
}
