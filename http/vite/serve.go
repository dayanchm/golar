package vite

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// Files serves the built frontend. Hashed files under assets/ are cached for
// a year; everything else must be revalidated. The manifest and directory
// listings are not served. Requests for missing files go to next, or get 404
// when next is nil.
func (a *Assets) Files(next http.Handler) http.Handler {
	files := http.StripPrefix(strings.TrimSuffix(a.config.Base, "/"), http.FileServerFS(a.fs))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, ok := a.file(r)
		if !ok {
			notFound(w, r, next)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// file maps a GET or HEAD request under Base to a regular built file.
func (a *Assets) file(r *http.Request) (string, bool) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return "", false
	}
	rest, ok := strings.CutPrefix(r.URL.Path, a.config.Base)
	if !ok {
		return "", false
	}
	name := path.Clean(rest)
	if name == "." || name == "index.html" || !fs.ValidPath(name) || name == ".vite" || strings.HasPrefix(name, ".vite/") {
		return "", false
	}
	info, err := fs.Stat(a.fs, name)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return name, true
}

func notFound(w http.ResponseWriter, r *http.Request, next http.Handler) {
	if next != nil {
		next.ServeHTTP(w, r)
		return
	}
	http.NotFound(w, r)
}

// SPA returns middleware for single-page apps: built files are served by
// Files, and other GET and HEAD requests for paths without a file extension
// get index.html so the client router can handle them. Requests that skip
// reports true for, such as /api, and every request while index.html is not
// built, go to next.
func (a *Assets) SPA(skip func(*http.Request) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if skip != nil && skip(r) || r.Method != http.MethodGet && r.Method != http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			if _, err := fs.Stat(a.fs, "index.html"); err != nil {
				next.ServeHTTP(w, r)
				return
			}
			if _, ok := a.file(r); ok {
				a.Files(nil).ServeHTTP(w, r)
				return
			}
			if !strings.HasPrefix(r.URL.Path, a.config.Base) || path.Ext(r.URL.Path) != "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, a.fs, "index.html")
		})
	}
}

// PathPrefix returns a skip function for SPA that matches the given path
// prefixes, such as "/api" (which matches /api and /api/..., not /apis).
func PathPrefix(prefixes ...string) func(*http.Request) bool {
	return func(r *http.Request) bool {
		for _, prefix := range prefixes {
			prefix = strings.TrimSuffix(prefix, "/")
			if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
				return true
			}
		}
		return false
	}
}
