package vite

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

// manifest is shaped like the .vite/manifest.json that Vite 8 writes.
const manifest = `{
  "src/main.js": {"file": "assets/main-B1x.js", "name": "main", "src": "src/main.js", "isEntry": true,
    "imports": ["_vendor-C2y.js"], "css": ["assets/main-D3z.css"]},
  "src/admin.js": {"file": "assets/admin-E4a.js", "src": "src/admin.js", "isEntry": true,
    "imports": ["_vendor-C2y.js"]},
  "_vendor-C2y.js": {"file": "assets/vendor-C2y.js", "css": ["assets/vendor-F5b.css"]},
  "src/print.css": {"file": "assets/print-G6c.css", "src": "src/print.css", "isEntry": true},
  "src/logo.svg": {"file": "assets/logo-H7d.svg", "src": "src/logo.svg"}
}`

func dist() fstest.MapFS {
	return fstest.MapFS{
		".vite/manifest.json":  {Data: []byte(manifest)},
		"index.html":           {Data: []byte("<!doctype html><div id=app></div>")},
		"favicon.ico":          {Data: []byte("icon")},
		"assets/main-B1x.js":   {Data: []byte("console.log(1)")},
		"assets/vendor-C2y.js": {Data: []byte("vendor")},
	}
}

func TestProductionTags(t *testing.T) {
	t.Setenv(DevServerEnv, "")
	a := New(Config{FS: dist()})
	got, err := a.Tags("src/main.js", "/src/admin.js", "src/print.css")
	if err != nil {
		t.Fatal(err)
	}
	want := `<link rel="stylesheet" href="/assets/vendor-F5b.css">` +
		`<link rel="stylesheet" href="/assets/main-D3z.css">` +
		`<link rel="stylesheet" href="/assets/print-G6c.css">` +
		`<link rel="modulepreload" href="/assets/vendor-C2y.js">` +
		`<script type="module" src="/assets/main-B1x.js"></script>` +
		`<script type="module" src="/assets/admin-E4a.js"></script>`
	if string(got) != want {
		t.Errorf("Tags =\n%s\nwant\n%s", got, want)
	}
	if url, err := a.URL("src/logo.svg"); err != nil || url != "/assets/logo-H7d.svg" {
		t.Errorf("URL = %q, %v", url, err)
	}
	if _, err := a.Tags("src/missing.js"); err == nil || !strings.Contains(err.Error(), "rollupOptions.input") {
		t.Errorf("missing entry error = %v", err)
	}
	if a.ReactRefresh() != "" || a.Dev() {
		t.Error("production output contains dev-only markup")
	}

	based := New(Config{FS: dist(), Base: "/static"})
	if url, _ := based.URL("src/main.js"); url != "/static/assets/main-B1x.js" {
		t.Errorf("based URL = %q", url)
	}
}

func TestDevTags(t *testing.T) {
	t.Setenv(DevServerEnv, "http://localhost:5173/")
	a := New(Config{FS: fstest.MapFS{}}) // no manifest needed in development
	got, err := a.Tags("src/main.jsx", "src/style.css")
	if err != nil {
		t.Fatal(err)
	}
	want := `<script type="module" src="http://localhost:5173/@vite/client"></script>` +
		`<script type="module" src="http://localhost:5173/src/main.jsx"></script>` +
		`<link rel="stylesheet" href="http://localhost:5173/src/style.css">`
	if string(got) != want {
		t.Errorf("dev Tags = %s", got)
	}
	if url, _ := a.URL("/src/logo.svg"); url != "http://localhost:5173/src/logo.svg" {
		t.Errorf("dev URL = %q", url)
	}
	refresh := string(a.ReactRefresh())
	if !strings.Contains(refresh, `from "http://localhost:5173/@react-refresh"`) || !strings.Contains(refresh, "__vite_plugin_react_preamble_installed__") {
		t.Errorf("ReactRefresh = %s", refresh)
	}

	hostile := New(Config{DevURL: `http://x/"></script><script>alert(1)</script>`})
	if tags, _ := hostile.Tags("a.js"); strings.Contains(string(tags), "<script>alert") {
		t.Errorf("dev URL not escaped: %s", tags)
	}
	if refresh := string(hostile.ReactRefresh()); strings.Count(refresh, "</script>") != 1 {
		t.Errorf("react refresh URL not escaped: %s", refresh)
	}
	explicit := New(Config{DevURL: "http://127.0.0.1:3000"})
	t.Setenv(DevServerEnv, "")
	if !explicit.Dev() {
		t.Error("explicit DevURL ignored")
	}
}

func TestManifestErrors(t *testing.T) {
	t.Setenv(DevServerEnv, "")
	if _, err := New(Config{FS: fstest.MapFS{}}).Tags("src/main.js"); err != ErrNoManifest {
		t.Errorf("missing manifest = %v", err)
	}
	broken := fstest.MapFS{".vite/manifest.json": {Data: []byte("{")}}
	if _, err := New(Config{FS: broken}).URL("x"); err == nil {
		t.Error("broken manifest accepted")
	}
	// The manifest is read again after a failed load, so building later works.
	files := fstest.MapFS{}
	a := New(Config{FS: files})
	a.Tags("src/main.js")
	files[".vite/manifest.json"] = &fstest.MapFile{Data: []byte(manifest)}
	if _, err := a.Tags("src/main.js"); err != nil {
		t.Errorf("manifest built after start: %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { a.Tags("src/main.js") })
	}
	wg.Wait()
}

func TestTemplateFuncs(t *testing.T) {
	t.Setenv(DevServerEnv, "")
	a := New(Config{FS: dist()})
	page := template.Must(template.New("page").Funcs(a.FuncMap()).Parse(
		`<head>{{vite_react_refresh}}{{vite "src/main.js"}}</head><img src="{{vite_asset "src/logo.svg"}}">`))
	var b strings.Builder
	if err := page.Execute(&b, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `<script type="module" src="/assets/main-B1x.js"></script>`) || !strings.Contains(b.String(), `<img src="/assets/logo-H7d.svg">`) {
		t.Errorf("rendered %s", b.String())
	}
}

func TestTemplateBeforeBuild(t *testing.T) {
	t.Setenv(DevServerEnv, "")
	page := template.Must(template.New("page").Funcs(New(Config{FS: fstest.MapFS{}}).FuncMap()).Parse(`{{vite "src/main.js"}}`))
	var b strings.Builder
	if err := page.Execute(&b, nil); err != nil || !strings.Contains(b.String(), "frontend not built") {
		t.Errorf("unbuilt page = %q, %v", b.String(), err)
	}
}

func get(h http.Handler, method, target string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
	return rec
}

func TestFiles(t *testing.T) {
	a := New(Config{FS: dist()})
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := a.Files(next)
	if rec := get(h, "GET", "/assets/main-B1x.js"); rec.Code != 200 || rec.Body.String() != "console.log(1)" || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("asset = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec := get(h, "GET", "/favicon.ico"); rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("favicon = %d %v", rec.Code, rec.Header())
	}
	for _, target := range []string{"/.vite/manifest.json", "/assets/", "/", "/index.html", "/missing.js", "/../go.mod"} {
		if rec := get(h, "GET", target); rec.Code != http.StatusTeapot {
			t.Errorf("%s = %d, want next", target, rec.Code)
		}
	}
	if rec := get(h, "POST", "/assets/main-B1x.js"); rec.Code != http.StatusTeapot {
		t.Errorf("POST = %d", rec.Code)
	}
	if rec := get(a.Files(nil), "GET", "/missing.js"); rec.Code != 404 {
		t.Errorf("missing without next = %d", rec.Code)
	}
	based := New(Config{FS: dist(), Base: "/static/"}).Files(next)
	if rec := get(based, "GET", "/static/assets/main-B1x.js"); rec.Code != 200 {
		t.Errorf("based asset = %d", rec.Code)
	}
	if rec := get(based, "GET", "/assets/main-B1x.js"); rec.Code != http.StatusTeapot {
		t.Errorf("asset outside base = %d", rec.Code)
	}
}

func TestSPA(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := New(Config{FS: dist()}).SPA(PathPrefix("/api", "/health"))(next)
	for target, want := range map[string]int{
		"/": 200, "/dashboard/settings": 200, "/assets/main-B1x.js": 200,
		"/api": http.StatusTeapot, "/api/users": http.StatusTeapot, "/health": http.StatusTeapot,
		"/apis": 200, "/missing.png": http.StatusTeapot, "/.vite/manifest.json": http.StatusTeapot,
	} {
		rec := get(h, "GET", target)
		if rec.Code != want {
			t.Errorf("GET %s = %d, want %d", target, rec.Code, want)
		}
		if want == 200 && !strings.HasPrefix(target, "/assets/") && !strings.Contains(rec.Body.String(), "<div id=app>") {
			t.Errorf("GET %s did not serve index.html: %q", target, rec.Body.String())
		}
	}
	if rec := get(h, "POST", "/dashboard"); rec.Code != http.StatusTeapot {
		t.Errorf("POST = %d", rec.Code)
	}
	unbuilt := New(Config{FS: fstest.MapFS{}}).SPA(nil)(next)
	if rec := get(unbuilt, "GET", "/"); rec.Code != http.StatusTeapot {
		t.Errorf("unbuilt = %d", rec.Code)
	}
}
