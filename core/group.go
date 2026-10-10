package core

import (
	"github.com/dayanchm/golar/http/router"
	"net/http"
)

type Group struct {
	app    *Application
	routes *router.Group
}

func (a *Application) Group(prefix string) *Group {
	return &Group{app: a, routes: a.router.Group(prefix)}
}
func (g *Group) Group(prefix string) *Group {
	return &Group{app: g.app, routes: g.routes.Group(prefix)}
}
func (g *Group) Handle(method, path string, handler Handler) {
	g.routes.Handle(method, path, g.app.wrap(handler))
}
func (g *Group) Get(path string, handler Handler)    { g.Handle(http.MethodGet, path, handler) }
func (g *Group) Post(path string, handler Handler)   { g.Handle(http.MethodPost, path, handler) }
func (g *Group) Put(path string, handler Handler)    { g.Handle(http.MethodPut, path, handler) }
func (g *Group) Patch(path string, handler Handler)  { g.Handle(http.MethodPatch, path, handler) }
func (g *Group) Delete(path string, handler Handler) { g.Handle(http.MethodDelete, path, handler) }
