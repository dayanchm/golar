package middleware

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestChain(t *testing.T) {
	var order []string
	wrap := func(name string) Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+" before")
				next.ServeHTTP(w, r)
				order = append(order, name+" after")
			})
		}
	}
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { order = append(order, "handler") })
	Chain(handler, wrap("A"), wrap("B")).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	want := []string{"A before", "B before", "handler", "B after", "A after"}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	order = nil
	Chain(handler).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
	if !reflect.DeepEqual(order, []string{"handler"}) {
		t.Fatal(order)
	}
}

func TestShortCircuit(t *testing.T) {
	block := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) })
	}
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("handler ran") }), block)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
}

func TestLogger(t *testing.T) {
	for _, tt := range []struct {
		name         string
		handler      http.HandlerFunc
		status, size string
	}{
		{"explicit", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(201)
			w.WriteHeader(500)
			_, _ = w.Write([]byte("abc"))
		}, "201", "3"},
		{"implicit", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("ok")) }, "200", "2"},
		{"empty", func(http.ResponseWriter, *http.Request) {}, "200", "0"},
		{"informational", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(103); w.WriteHeader(202) }, "202", "0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			rec := httptest.NewRecorder()
			Logger(log.New(&output, "", 0))(tt.handler).ServeHTTP(rec, httptest.NewRequest("POST", "/users?secret=value", nil))
			for _, want := range []string{"method=POST", `path="/users"`, "status=" + tt.status, "bytes=" + tt.size, "duration="} {
				if !strings.Contains(output.String(), want) {
					t.Errorf("log %q missing %q", output.String(), want)
				}
			}
			if strings.Contains(output.String(), "secret") {
				t.Error("query leaked into log")
			}
		})
	}
}

func TestRecovery(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output, "", 0)
	h := Chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("private detail") }), Logger(logger), Recovery(logger))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/users", nil))
	if rec.Code != 500 || rec.Body.String() != "Internal Server Error\n" {
		t.Fatalf("response = %d %q", rec.Code, rec.Body.String())
	}
	if !strings.Contains(output.String(), "private detail") || !strings.Contains(output.String(), "status=500") {
		t.Fatal(output.String())
	}
}

func TestRecoveryNormalResponse(t *testing.T) {
	h := Recovery()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 204 {
		t.Fatal(rec.Code)
	}
}

func TestRecoveryAbort(t *testing.T) {
	for _, mode := range []string{"committed", "flushed", "abort"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			h := Recovery(log.New(&output, "", 0))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "abort" {
					panic(http.ErrAbortHandler)
				}
				if mode == "flushed" {
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Fatal(err)
					}
				} else {
					_, _ = w.Write([]byte("partial"))
				}
				panic("late panic")
			}))
			defer func() {
				if got := recover(); got != http.ErrAbortHandler {
					t.Errorf("panic = %v, want ErrAbortHandler", got)
				}
			}()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
		})
	}
}
