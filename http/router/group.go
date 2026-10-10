package router

import (
	"net/http"
	"strings"
)

// Group registers routes under a shared path prefix.
type Group struct {
	router *Router
	prefix string
}

func joinPaths(prefix, path string) string {
	prefix = strings.Trim(prefix, "/")
	path = strings.Trim(path, "/")
	if prefix == "" {
		return "/" + path
	}
	if path == "" {
		return "/" + prefix
	}
	return "/" + prefix + "/" + path
}

func (r *Router) Group(prefix string) *Group {
	return &Group{router: r, prefix: joinPaths("", prefix)}
}

func (g *Group) Group(prefix string) *Group {
	return &Group{router: g.router, prefix: joinPaths(g.prefix, prefix)}
}

func (g *Group) Handle(method, path string, handler http.HandlerFunc) {
	g.router.Handle(method, joinPaths(g.prefix, path), handler)
}

func (g *Group) Get(path string, handler http.HandlerFunc) {
	g.Handle(http.MethodGet, path, handler)
}

func (g *Group) Post(path string, handler http.HandlerFunc) {
	g.Handle(http.MethodPost, path, handler)
}

func (g *Group) Put(path string, handler http.HandlerFunc) {
	g.Handle(http.MethodPut, path, handler)
}

func (g *Group) Patch(path string, handler http.HandlerFunc) {
	g.Handle(http.MethodPatch, path, handler)
}

func (g *Group) Delete(path string, handler http.HandlerFunc) {
	g.Handle(http.MethodDelete, path, handler)
}
