package context

import (
	stdcontext "context"
	"crypto/tls"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTMLAndBlob(t *testing.T) {
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.HTML(http.StatusOK, "<h1>Hi</h1>"); err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get("Content-Type") != "text/html; charset=utf-8" || rec.Body.String() != "<h1>Hi</h1>" {
		t.Errorf("HTML wrote %q %q", rec.Header().Get("Content-Type"), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	c = New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.Blob(http.StatusCreated, "text/csv", []byte("a,b")); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusCreated || rec.Header().Get("Content-Type") != "text/csv" || rec.Body.String() != "a,b" {
		t.Errorf("Blob wrote %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestRender(t *testing.T) {
	templates := template.Must(template.New("page").Parse(`{{define "page"}}<p>{{.}}</p>{{end}}{{define "bad"}}{{.Missing}}{{end}}`))
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.Render(http.StatusOK, templates, "page", "<script>"); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != "<p>&lt;script&gt;</p>" || rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("Render wrote %q", rec.Body.String())
	}
	rec = httptest.NewRecorder()
	c = New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.Render(http.StatusOK, templates, "bad", "text"); err == nil {
		t.Fatal("expected a template error")
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Error("a failed Render wrote a response")
	}
}

func TestNoContentAndRedirect(t *testing.T) {
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodDelete, "/", nil))
	if err := c.NoContent(http.StatusNoContent); err != nil || rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("NoContent = %d %q %v", rec.Code, rec.Body.String(), err)
	}
	rec = httptest.NewRecorder()
	c = New(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if err := c.Redirect(http.StatusSeeOther, "/login"); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Errorf("Redirect = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = httptest.NewRecorder()
	c = New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.Redirect(http.StatusOK, "/login"); err == nil || rec.Header().Get("Location") != "" {
		t.Errorf("Redirect(200) error = %v, Location = %q", err, rec.Header().Get("Location"))
	}
}

func TestDownload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.csv")
	if err := os.WriteFile(path, []byte("id,name\n1,Ali\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	c := New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if err := c.Download(path, ""); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || rec.Body.String() != "id,name\n1,Ali\n" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename=report.csv` ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/csv") {
		t.Errorf("Download = %d %v %q", rec.Code, rec.Header(), rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Range", "bytes=0-1")
	c = New(rec, r)
	if err := c.Download(path, "rapor ğ.csv"); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusPartialContent || rec.Body.String() != "id" ||
		rec.Header().Get("Content-Disposition") != `attachment; filename*=utf-8''rapor%20%C4%9F.csv` {
		t.Errorf("ranged Download = %d %q %q", rec.Code, rec.Header().Get("Content-Disposition"), rec.Body.String())
	}

	for _, bad := range []string{dir, filepath.Join(dir, "missing.csv")} {
		rec = httptest.NewRecorder()
		c = New(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if err := c.Download(bad, ""); err == nil || rec.Body.Len() != 0 {
			t.Errorf("Download(%s) error = %v, body %q", bad, err, rec.Body.String())
		}
	}
}

func TestIP(t *testing.T) {
	proxies := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("::1/128")}
	for _, tt := range []struct {
		name, remote, forwarded string
		trusted                 []netip.Prefix
		want                    string
	}{
		{"no proxies ignores header", "203.0.113.9:1234", "198.51.100.1", nil, "203.0.113.9"},
		{"untrusted remote ignores header", "203.0.113.9:1234", "198.51.100.1", proxies, "203.0.113.9"},
		{"trusted proxy", "10.0.0.2:1234", "198.51.100.1", proxies, "198.51.100.1"},
		{"spoofed leftmost entry", "10.0.0.2:1234", "1.2.3.4, 198.51.100.1, 10.0.0.3", proxies, "198.51.100.1"},
		{"all trusted", "10.0.0.2:1234", "10.0.0.4, 10.0.0.3", proxies, "10.0.0.4"},
		{"invalid entry stops", "10.0.0.2:1234", "198.51.100.1, garbage", proxies, "10.0.0.2"},
		{"no header", "10.0.0.2:1234", "", proxies, "10.0.0.2"},
		{"ipv6", "[::1]:1234", "2001:db8::1", proxies, "2001:db8::1"},
		{"mapped ipv4", "[::ffff:10.0.0.2]:1234", "198.51.100.7", proxies, "198.51.100.7"},
		{"unparsable remote", "pipe", "", proxies, "pipe"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remote
			if tt.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if got := New(httptest.NewRecorder(), r, Config{TrustedProxies: tt.trusted}).IP(); got != tt.want {
				t.Errorf("IP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsTLS(t *testing.T) {
	proxies := Config{TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	if New(nil, r).IsTLS() {
		t.Error("untrusted X-Forwarded-Proto was believed")
	}
	r.RemoteAddr = "10.0.0.2:1234"
	if !New(nil, r, proxies).IsTLS() {
		t.Error("trusted X-Forwarded-Proto was ignored")
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = &tls.ConnectionState{}
	if !New(nil, r).IsTLS() {
		t.Error("TLS request not detected")
	}
}

func TestWantsJSON(t *testing.T) {
	for accept, want := range map[string]bool{
		"":                         false,
		"*/*":                      false,
		"application/json":         true,
		"application/problem+json": true,
		"text/html,application/xhtml+xml,*/*;q=0.8": false,
		"text/html;q=0.5, application/json":         true,
		"application/json;q=0.4, text/html":         false,
		"application/json;q=0":                      false,
		"application/json;q=bad":                    false,
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Accept", accept)
		if got := New(nil, r).WantsJSON(); got != want {
			t.Errorf("WantsJSON(%q) = %v, want %v", accept, got, want)
		}
	}
}

func TestCookies(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.AddCookie(&http.Cookie{Name: "theme", Value: "dark"})
	rec := httptest.NewRecorder()
	c := New(rec, r)
	if value, err := c.Cookie("theme"); value != "dark" || err != nil {
		t.Errorf("Cookie(theme) = %q, %v", value, err)
	}
	if _, err := c.Cookie("missing"); !errors.Is(err, http.ErrNoCookie) {
		t.Errorf("Cookie(missing) error = %v", err)
	}
	if err := c.SetCookie("theme", "light", time.Hour); err != nil {
		t.Fatal(err)
	}
	c.ClearCookie("old")
	if err := c.SetCookie("bad name", "x", 0); err == nil {
		t.Error("SetCookie accepted an invalid name")
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("cookies = %v", cookies)
	}
	set, cleared := cookies[0], cookies[1]
	if set.Value != "light" || set.Path != "/" || !set.HttpOnly || set.SameSite != http.SameSiteLaxMode || set.Secure || set.MaxAge != 3600 {
		t.Errorf("set cookie = %+v", set)
	}
	if cleared.Name != "old" || cleared.MaxAge != -1 {
		t.Errorf("cleared cookie = %+v", cleared)
	}

	r = httptest.NewRequest(http.MethodGet, "/", nil)
	r.TLS = &tls.ConnectionState{}
	rec = httptest.NewRecorder()
	if err := New(rec, r).SetCookie("session", "abc", 0); err != nil {
		t.Fatal(err)
	}
	if cookie := rec.Result().Cookies()[0]; !cookie.Secure || cookie.MaxAge != 0 || !cookie.Expires.IsZero() {
		t.Errorf("HTTPS session cookie = %+v", cookie)
	}
}

func TestContextPassthrough(t *testing.T) {
	type key struct{}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r = r.WithContext(stdcontext.WithValue(r.Context(), key{}, "value"))
	if got := New(nil, r).Context().Value(key{}); got != "value" {
		t.Errorf("Context().Value = %v", got)
	}
}
