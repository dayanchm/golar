package middleware

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var ok = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok") })

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func TestCORS(t *testing.T) {
	h := CORS(CORSConfig{
		AllowOrigins:     []string{"https://app.example"},
		AllowOriginFunc:  func(origin string) bool { return strings.HasSuffix(origin, ".preview.example") },
		AllowCredentials: true,
		ExposeHeaders:    []string{"X-Request-ID"},
		MaxAge:           10 * time.Minute,
	})(ok)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://app.example")
	rec := serve(h, r)
	if rec.Body.String() != "ok" || rec.Header().Get("Access-Control-Allow-Origin") != "https://app.example" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" || rec.Header().Get("Access-Control-Expose-Headers") != "X-Request-ID" ||
		rec.Header().Get("Vary") != "Origin" {
		t.Fatalf("simple request headers = %v", rec.Header())
	}

	r = httptest.NewRequest("OPTIONS", "/", nil)
	r.Header.Set("Origin", "https://pr-1.preview.example")
	r.Header.Set("Access-Control-Request-Method", "PUT")
	rec = serve(h, r)
	if rec.Code != 204 || rec.Body.Len() != 0 || rec.Header().Get("Access-Control-Allow-Origin") != "https://pr-1.preview.example" ||
		rec.Header().Get("Access-Control-Allow-Methods") != "GET, HEAD, POST, PUT, PATCH, DELETE" ||
		!strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Authorization") || rec.Header().Get("Access-Control-Max-Age") != "600" {
		t.Fatalf("preflight = %d %v", rec.Code, rec.Header())
	}

	for _, method := range []string{"GET", "OPTIONS"} {
		r = httptest.NewRequest(method, "/", nil)
		r.Header.Set("Origin", "https://evil.example")
		r.Header.Set("Access-Control-Request-Method", "PUT")
		rec = serve(h, r)
		if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Header().Get("Access-Control-Allow-Credentials") != "" {
			t.Fatalf("%s from a disallowed origin got CORS headers: %v", method, rec.Header())
		}
	}
	if rec.Code != 204 {
		t.Fatalf("disallowed preflight should not reach the handler: %d", rec.Code)
	}

	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Origin", "https://any.example")
	if got := serve(CORS(CORSConfig{AllowOrigins: []string{"*"}})(ok), r).Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("wildcard origin = %q", got)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("credentials with * should panic")
		}
	}()
	CORS(CORSConfig{AllowOrigins: []string{"*"}, AllowCredentials: true})
}

func TestRequestID(t *testing.T) {
	var output bytes.Buffer
	var seen string
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = RequestIDFrom(r.Context()) }),
		Logger(log.New(&output, "", 0)), RequestID())

	rec := serve(h, httptest.NewRequest("GET", "/", nil))
	id := rec.Header().Get("X-Request-ID")
	if len(id) != 32 || seen != id || !strings.Contains(output.String(), "request_id="+id) {
		t.Fatalf("generated id = %q, context %q, log %q", id, seen, output.String())
	}

	for incoming, reuse := range map[string]bool{"abc-123": true, "bad id\n": false, strings.Repeat("a", 129): false} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("X-Request-ID", incoming)
		got := serve(h, r).Header().Get("X-Request-ID")
		if (got == incoming) != reuse {
			t.Errorf("incoming %q -> %q, reuse = %v", incoming, got, reuse)
		}
	}

	custom := RequestID(RequestIDConfig{Header: "X-Trace", Generate: func() string { return "fixed" }})(ok)
	if got := serve(custom, httptest.NewRequest("GET", "/", nil)).Header().Get("X-Trace"); got != "fixed" {
		t.Fatal(got)
	}
	if RequestIDFrom(context.Background()) != "" {
		t.Fatal("empty context should have no request ID")
	}
}

func TestTimeout(t *testing.T) {
	slow := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
			_, _ = io.WriteString(w, "late")
		}
	})
	rec := serve(Timeout(10*time.Millisecond)(slow), httptest.NewRequest("GET", "/", nil))
	if rec.Code != 503 || rec.Body.String() != "Service Unavailable\n" {
		t.Fatalf("timed out response = %d %q", rec.Code, rec.Body.String())
	}
	rec = serve(Timeout(time.Second)(ok), httptest.NewRequest("GET", "/", nil))
	if rec.Code != 200 || rec.Body.String() != "ok" {
		t.Fatalf("fast response = %d %q", rec.Code, rec.Body.String())
	}
	partial := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "started")
		<-r.Context().Done()
	})
	if rec = serve(Timeout(10*time.Millisecond)(partial), httptest.NewRequest("GET", "/", nil)); rec.Code != 200 || rec.Body.String() != "started" {
		t.Fatalf("committed response must not get a second body: %d %q", rec.Code, rec.Body.String())
	}
}

func TestBodyLimit(t *testing.T) {
	var readErr error
	h := BodyLimit(4)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
	}))
	if rec := serve(h, httptest.NewRequest("POST", "/", strings.NewReader("12345"))); rec.Code != 413 {
		t.Fatalf("declared length over limit = %d", rec.Code)
	}
	r := httptest.NewRequest("POST", "/", io.MultiReader(strings.NewReader("12345")))
	r.ContentLength = -1
	serve(h, r)
	var tooLarge *http.MaxBytesError
	if !errors.As(readErr, &tooLarge) {
		t.Fatalf("streamed body error = %v", readErr)
	}
	readErr = nil
	if serve(h, httptest.NewRequest("POST", "/", strings.NewReader("1234"))); readErr != nil {
		t.Fatal(readErr)
	}
	serve(h, httptest.NewRequest("GET", "/", nil))
	if readErr != nil {
		t.Fatal(readErr)
	}
}

func TestSecureHeaders(t *testing.T) {
	h := SecureHeaders()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "SAMEORIGIN") // handlers may override
	}))
	rec := serve(h, httptest.NewRequest("GET", "/", nil))
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Fatal("HSTS must not be sent over plain HTTP")
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("X-Frame-Options") != "SAMEORIGIN" ||
		rec.Header().Get("Referrer-Policy") != "strict-origin-when-cross-origin" || rec.Header().Get("Content-Security-Policy") != "" {
		t.Fatalf("headers = %v", rec.Header())
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.TLS = &tls.ConnectionState{}
	if got := serve(h, r).Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Fatalf("HSTS = %q", got)
	}
	custom := SecureHeaders(SecureHeadersConfig{HSTSMaxAge: time.Hour, HSTSPreload: true, ContentSecurityPolicy: "default-src 'self'"})(ok)
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	rec = serve(custom, r)
	if rec.Header().Get("Strict-Transport-Security") != "max-age=3600; preload" || rec.Header().Get("Content-Security-Policy") != "default-src 'self'" ||
		rec.Header().Get("X-Frame-Options") != "" {
		t.Fatalf("custom headers = %v", rec.Header())
	}
}

func gunzip(t *testing.T, body []byte) string {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestCompress(t *testing.T) {
	large := strings.Repeat("golar ", 400)
	write := func(contentType, body string, status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if contentType != "" {
				w.Header().Set("Content-Type", contentType)
			}
			w.Header().Set("Content-Length", "999")
			if status != 0 {
				w.WriteHeader(status)
			}
			for chunk := range strings.SplitSeq(body, " ") {
				_, _ = io.WriteString(w, chunk+" ")
			}
		})
	}
	request := func(encoding string) *http.Request {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("Accept-Encoding", encoding)
		return r
	}

	rec := serve(Compress()(write("application/json", large, 201)), request("br, gzip;q=0.8"))
	if rec.Code != 201 || rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Length") != "" ||
		rec.Header().Get("Vary") != "Accept-Encoding" || gunzip(t, rec.Body.Bytes()) != large+" " {
		t.Fatalf("compressed = %d %v", rec.Code, rec.Header())
	}
	if rec = serve(Compress()(write("", large, 0)), request("*")); rec.Header().Get("Content-Encoding") != "gzip" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("sniffed = %v", rec.Header())
	}

	for name, tt := range map[string]struct {
		handler        http.Handler
		encoding, want string
	}{
		"small":        {write("text/plain", "tiny", 0), "gzip", "tiny "},
		"not accepted": {write("text/plain", large, 0), "br", large + " "},
		"q=0":          {write("text/plain", large, 0), "gzip;q=0", large + " "},
		"image":        {write("image/png", large, 0), "gzip", large + " "},
		"event stream": {write("text/event-stream", large, 0), "gzip", large + " "},
		"partial":      {write("text/plain", large, 206), "gzip", large + " "},
		"already coded": {http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Encoding", "br")
			_, _ = io.WriteString(w, large)
		}), "gzip", large},
	} {
		rec := serve(Compress()(tt.handler), request(tt.encoding))
		if rec.Header().Get("Content-Encoding") == "gzip" || rec.Body.String() != tt.want {
			t.Errorf("%s: should pass through unchanged, got %v", name, rec.Header())
		}
	}

	head := httptest.NewRequest("HEAD", "/", nil)
	head.Header.Set("Accept-Encoding", "gzip")
	if serve(Compress()(write("text/plain", large, 0)), head).Header().Get("Content-Encoding") != "" {
		t.Error("HEAD should not be compressed")
	}
	if rec = serve(Compress(CompressConfig{MinSize: 2})(write("text/plain", "tiny", 204)), request("gzip")); rec.Code != 204 || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("204 = %d %v", rec.Code, rec.Header())
	}
	if rec = serve(Compress()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), request("gzip")); rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("empty = %d %q", rec.Code, rec.Body.String())
	}
}

func TestCompressFlushAndPanic(t *testing.T) {
	stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("a", 2000))
		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}
		_, _ = io.WriteString(w, "b")
	})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	rec := serve(Compress()(stream), r)
	if !rec.Flushed || gunzip(t, rec.Body.Bytes()) != strings.Repeat("a", 2000)+"b" {
		t.Fatal("flushed gzip stream is incomplete")
	}

	var output bytes.Buffer
	panicking := Chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "partial")
		panic("boom")
	}), Recovery(log.New(&output, "", 0)), Compress())
	if rec = serve(panicking, r); rec.Code != 500 || rec.Body.String() != "Internal Server Error\n" {
		t.Fatalf("panic under Compress = %d %q", rec.Code, rec.Body.String())
	}
	if _, err := gzip.NewWriterLevel(nil, 42); err == nil {
		t.Fatal("expected invalid level")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("invalid level should panic")
		}
	}()
	Compress(CompressConfig{Level: 42})
}
