package router

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/dayanchm/golar/http/middleware"
	"github.com/dayanchm/golar/http/server"
)

type Route struct {
	Method  string
	Path    string
	Handler http.HandlerFunc
}

type Router struct {
	routes      []Route
	handler     http.Handler
	middlewares []middleware.Middleware
}

func New() *Router {
	return &Router{
		routes: make([]Route, 0),
	}
}

// Run serves requests until SIGINT or SIGTERM and waits for active requests.
func (r *Router) Run(addr string) error {
	return server.New(r, server.Config{Addr: addr}).Run(context.Background())
}

func (r *Router) Handle(method, path string, handler http.HandlerFunc) {
	for _, route := range r.routes {
		if route.Method == method && strings.Trim(route.Path, "/") == strings.Trim(path, "/") {
			panic(fmt.Sprintf("router: duplicate route %s %s", method, path))
		}
	}
	r.routes = append(r.routes, Route{
		Method:  method,
		Path:    path,
		Handler: handler,
	})
	// Compare segments from left to right so static segments win over parameters.
	sort.SliceStable(r.routes, func(i, j int) bool {
		a, b := splitPath(r.routes[i].Path), splitPath(r.routes[j].Path)
		for k := 0; k < len(a) && k < len(b); k++ {
			aDynamic := strings.HasPrefix(a[k], "{") && strings.HasSuffix(a[k], "}")
			bDynamic := strings.HasPrefix(b[k], "{") && strings.HasSuffix(b[k], "}")
			if aDynamic != bDynamic {
				return !aDynamic
			}
		}
		return len(a) < len(b)
	})
}

func (r *Router) Get(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodGet, path, handler)
}

func (r *Router) Post(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodPost, path, handler)
}

func (r *Router) Put(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodPut, path, handler)
}

func (r *Router) Patch(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodPatch, path, handler)
}

func (r *Router) Delete(path string, handler http.HandlerFunc) {
	r.Handle(http.MethodDelete, path, handler)
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	if r.handler != nil {
		r.handler.ServeHTTP(w, req)
		return
	}
	r.serveHTTP(w, req)
}

// Use adds global middleware. Register middleware before serving requests.
func (r *Router) Use(middlewares ...middleware.Middleware) {
	// Retain registration order across multiple Use calls.
	r.middlewares = append(r.middlewares, middlewares...)
	r.handler = middleware.Chain(http.HandlerFunc(r.serveHTTP), r.middlewares...)
}

func (r *Router) serveHTTP(w http.ResponseWriter, req *http.Request) {
	var allowedMethods []string
	for _, route := range r.routes {
		params, ok := match(route.Path, req.URL.Path)
		if !ok {
			continue
		}
		if route.Method != req.Method {
			allowedMethods = append(allowedMethods, route.Method)
			continue
		}
		for key, value := range params {
			req.SetPathValue(key, value)
		}
		route.Handler(w, req)
		return
	}

	if len(allowedMethods) > 0 {
		sort.Strings(allowedMethods)
		uniqueMethods := allowedMethods[:1]
		for _, method := range allowedMethods[1:] {
			if method != uniqueMethods[len(uniqueMethods)-1] {
				uniqueMethods = append(uniqueMethods, method)
			}
		}
		w.Header().Set("Allow", strings.Join(uniqueMethods, ", "))
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}

	http.NotFound(w, req)
}
