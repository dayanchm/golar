// Package vite connects a Go server to a Vite frontend. In development it
// points pages at the Vite dev server for hot reloading; in production it
// reads Vite's build manifest to emit hashed asset URLs and serves the built
// files with long-lived caching.
//
//	assets := vite.New(vite.Config{})
//	templates := template.Must(template.New("").Funcs(assets.FuncMap()).ParseFS(files, "templates/*.html"))
//
//	<head>{{ vite "src/main.js" }}</head>
package vite

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"
)

// DevServerEnv names the environment variable that holds the Vite dev
// server's URL. golar serve sets it while it runs Vite.
const DevServerEnv = "VITE_DEV_SERVER_URL"

// Config configures Assets. Zero values use the defaults noted.
type Config struct {
	// Dir is the Vite build output on disk; it defaults to "frontend/dist".
	// It is ignored when FS is set.
	Dir string
	// FS holds the build output, for example an embed.FS sub-tree, so a
	// single binary carries its frontend.
	FS fs.FS
	// Base is the URL prefix of built files and must match Vite's base
	// option; it defaults to "/".
	Base string
	// DevURL is the Vite dev server, such as "http://localhost:5173". When it
	// is empty, VITE_DEV_SERVER_URL is read on each use; when that is empty
	// too, built assets are used.
	DevURL string
}

// Assets resolves entry points to tags and serves built files. It is safe
// for concurrent use.
type Assets struct {
	config Config
	fs     fs.FS

	mu       sync.Mutex
	manifest map[string]chunk
}

// chunk is one entry of Vite's .vite/manifest.json.
type chunk struct {
	File    string   `json:"file"`
	CSS     []string `json:"css"`
	Imports []string `json:"imports"`
	IsEntry bool     `json:"isEntry"`
}

// New returns Assets for config. It does no I/O; the manifest is read on
// first use, so the server can start before the frontend is built.
func New(config Config) *Assets {
	if config.Dir == "" {
		config.Dir = "frontend/dist"
	}
	if config.Base == "" {
		config.Base = "/"
	}
	if !strings.HasSuffix(config.Base, "/") {
		config.Base += "/"
	}
	fsys := config.FS
	if fsys == nil {
		fsys = os.DirFS(config.Dir)
	}
	return &Assets{config: config, fs: fsys}
}

// Dev reports whether pages use the Vite dev server.
func (a *Assets) Dev() bool { return a.devURL() != "" }

func (a *Assets) devURL() string {
	url := a.config.DevURL
	if url == "" {
		url = os.Getenv(DevServerEnv)
	}
	return strings.TrimRight(url, "/")
}

// ErrNoManifest means the frontend has not been built with manifest output.
var ErrNoManifest = errors.New("vite: .vite/manifest.json not found; run npm run build with build.manifest enabled")

func (a *Assets) load() (map[string]chunk, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.manifest != nil {
		return a.manifest, nil
	}
	data, err := fs.ReadFile(a.fs, ".vite/manifest.json")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNoManifest
	}
	if err != nil {
		return nil, err
	}
	var manifest map[string]chunk
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("vite: invalid manifest: %w", err)
	}
	a.manifest = manifest
	return manifest, nil
}

func entryKey(entry string) string { return strings.TrimPrefix(path.Clean("/"+entry), "/") }

func isCSS(name string) bool {
	switch path.Ext(name) {
	case ".css", ".scss", ".sass", ".less", ".styl", ".stylus", ".pcss", ".postcss":
		return true
	}
	return false
}

// URL returns the public URL of an entry or asset, such as "src/logo.svg":
// the dev server's in development, the hashed built file in production.
func (a *Assets) URL(entry string) (string, error) {
	key := entryKey(entry)
	if dev := a.devURL(); dev != "" {
		return dev + "/" + key, nil
	}
	manifest, err := a.load()
	if err != nil {
		return "", err
	}
	c, ok := manifest[key]
	if !ok {
		return "", fmt.Errorf("vite: %q is not in the manifest; add it to build.rollupOptions.input", key)
	}
	return a.config.Base + c.File, nil
}

// Tags returns the HTML for entries, such as "src/main.js": in development the
// Vite client and the sources from the dev server; in production stylesheet
// links, module preloads and module scripts for the hashed files.
func (a *Assets) Tags(entries ...string) (template.HTML, error) {
	var b strings.Builder
	if dev := a.devURL(); dev != "" {
		fmt.Fprintf(&b, `<script type="module" src="%s"></script>`, template.HTMLEscapeString(dev+"/@vite/client"))
		for _, entry := range entries {
			url := template.HTMLEscapeString(dev + "/" + entryKey(entry))
			if isCSS(entry) {
				fmt.Fprintf(&b, `<link rel="stylesheet" href="%s">`, url)
			} else {
				fmt.Fprintf(&b, `<script type="module" src="%s"></script>`, url)
			}
		}
		return template.HTML(b.String()), nil
	}
	manifest, err := a.load()
	if err != nil {
		return "", err
	}
	var styles, preloads, scripts []string
	seen := map[string]bool{}
	add := func(list *[]string, file string) {
		if !seen[file] {
			seen[file] = true
			*list = append(*list, file)
		}
	}
	var walk func(key string, entry bool)
	visited := map[string]bool{}
	walk = func(key string, entry bool) {
		if visited[key] {
			return
		}
		visited[key] = true
		c := manifest[key]
		for _, imported := range c.Imports {
			walk(imported, false)
		}
		for _, css := range c.CSS {
			add(&styles, css)
		}
		switch {
		case isCSS(c.File):
			add(&styles, c.File)
		case entry:
			add(&scripts, c.File)
		default:
			add(&preloads, c.File)
		}
	}
	for _, entry := range entries {
		key := entryKey(entry)
		if _, ok := manifest[key]; !ok {
			return "", fmt.Errorf("vite: %q is not in the manifest; add it to build.rollupOptions.input", key)
		}
		walk(key, true)
	}
	for _, file := range styles {
		fmt.Fprintf(&b, `<link rel="stylesheet" href="%s">`, template.HTMLEscapeString(a.config.Base+file))
	}
	for _, file := range preloads {
		fmt.Fprintf(&b, `<link rel="modulepreload" href="%s">`, template.HTMLEscapeString(a.config.Base+file))
	}
	for _, file := range scripts {
		fmt.Fprintf(&b, `<script type="module" src="%s"></script>`, template.HTMLEscapeString(a.config.Base+file))
	}
	return template.HTML(b.String()), nil
}

// ReactRefresh returns the preamble that @vitejs/plugin-react needs when the
// page is not served by Vite itself. Put it before Tags; it is empty in
// production.
func (a *Assets) ReactRefresh() template.HTML {
	dev := a.devURL()
	if dev == "" {
		return ""
	}
	// json.Marshal escapes <, > and &, so the URL cannot close the script.
	url, _ := json.Marshal(dev + "/@react-refresh")
	return template.HTML(`<script type="module">import RefreshRuntime from ` + string(url) + `;` +
		`RefreshRuntime.injectIntoGlobalHook(window);window.$RefreshReg$=()=>{};` +
		`window.$RefreshSig$=()=>(type)=>type;window.__vite_plugin_react_preamble_installed__=true;</script>`)
}

// FuncMap exposes vite (Tags), vite_asset (URL) and vite_react_refresh
// (ReactRefresh) to html/template. Add it with Funcs before parsing. Before
// the frontend is built, vite renders an HTML comment instead of failing, so
// pages still load; unknown entries remain errors.
func (a *Assets) FuncMap() template.FuncMap {
	return template.FuncMap{
		"vite": func(entries ...string) (template.HTML, error) {
			tags, err := a.Tags(entries...)
			if errors.Is(err, ErrNoManifest) {
				return "<!-- vite: frontend not built; run npm run build in frontend, or golar serve -->", nil
			}
			return tags, err
		},
		"vite_asset":         a.URL,
		"vite_react_refresh": a.ReactRefresh,
	}
}
