// Package core connects routing, request contexts, middleware and server lifecycle.
package core

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	httpcontext "github.com/dayanchm/golar/http/context"
	"github.com/dayanchm/golar/http/middleware"
	"github.com/dayanchm/golar/http/router"
	"github.com/dayanchm/golar/http/server"
)

type Handler func(*httpcontext.Context) error
type ErrorHandler func(*httpcontext.Context, error)
type Hook func(context.Context) error
type Config = server.Config

// Application is configured before serving. Run supports one lifecycle.
type Application struct {
	router       *router.Router
	config       Config
	errorHandler ErrorHandler
	startup      []Hook
	shutdown     []Hook
	started      atomic.Bool
}

func New(configs ...Config) *Application {
	var config Config
	if len(configs) > 0 {
		config = configs[0]
	}
	return &Application{router: router.New(), config: config, errorHandler: DefaultErrorHandler}
}

func (a *Application) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.router.ServeHTTP(w, r) }
func (a *Application) Use(middlewares ...middleware.Middleware)         { a.router.Use(middlewares...) }
func (a *Application) SetErrorHandler(handler ErrorHandler) {
	if handler == nil {
		handler = DefaultErrorHandler
	}
	a.errorHandler = handler
}
func (a *Application) OnStart(hook Hook)    { a.startup = append(a.startup, hook) }
func (a *Application) OnShutdown(hook Hook) { a.shutdown = append(a.shutdown, hook) }

func (a *Application) wrap(handler Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writer := &responseWriter{ResponseWriter: w}
		c := httpcontext.New(writer, r)
		if err := handler(c); err != nil {
			a.errorHandler(c, err)
		}
	}
}

func (a *Application) Handle(method, path string, handler Handler) {
	a.router.Handle(method, path, a.wrap(handler))
}
func (a *Application) Get(path string, handler Handler)   { a.Handle(http.MethodGet, path, handler) }
func (a *Application) Post(path string, handler Handler)  { a.Handle(http.MethodPost, path, handler) }
func (a *Application) Put(path string, handler Handler)   { a.Handle(http.MethodPut, path, handler) }
func (a *Application) Patch(path string, handler Handler) { a.Handle(http.MethodPatch, path, handler) }
func (a *Application) Delete(path string, handler Handler) {
	a.Handle(http.MethodDelete, path, handler)
}

// Adapt allows standard HTTP handlers to be registered with an Application.
func Adapt(handler http.Handler) Handler {
	return func(c *httpcontext.Context) error { handler.ServeHTTP(c.Writer, c.Request); return nil }
}

func (a *Application) Run(address ...string) error {
	return a.RunContext(context.Background(), address...)
}

// RunContext stops on cancellation, SIGINT or SIGTERM. Startup hooks run in
// registration order; shutdown hooks run in reverse order after requests drain.
func (a *Application) RunContext(ctx context.Context, address ...string) error {
	if !a.started.CompareAndSwap(false, true) {
		return errors.New("golar: application has already run")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if ctx.Err() != nil {
		return nil
	}
	config := a.config
	if len(address) > 0 {
		config.Addr = address[0]
	}
	if config.Addr == "" {
		config.Addr = ":8080"
	}
	listener, err := net.Listen("tcp", config.Addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	for _, hook := range a.startup {
		if err := hook(ctx); err != nil {
			return errors.Join(err, a.stop(config))
		}
	}
	err = server.New(a, config).Serve(ctx, listener)
	return errors.Join(err, a.stop(config))
}

func (a *Application) stop(config Config) error {
	timeout := config.ShutdownTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var err error
	for i := len(a.shutdown) - 1; i >= 0; i-- {
		err = errors.Join(err, a.shutdown[i](ctx))
	}
	return err
}
