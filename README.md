# Golar — AI-native full-stack framework for Go

> A modern, modular, AI-native full-stack framework for Go, inspired by Laravel's developer experience.

## Quick start

Requires Go 1.27 or newer.

```go
package main

import (
    "log"
    "net/http"

    "github.com/dayanchm/golar"
    "github.com/dayanchm/golar/http/middleware"
)

func main() {
    app := golar.New()
    app.Use(middleware.Logger(), middleware.Recovery())
    app.Get("/users/{id}", func(c *golar.Context) error {
        return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
    })
    if err := app.Run(":8080"); err != nil {
        log.Fatal(err)
    }
}
```

Run the included example with `go run ./examples/basic-api`. It provides `/`,
`/health`, `GET /api/users/{id}` and `POST /api/users` with a JSON `name` field.
The POST example echoes the user; it does not persist data.

## Application lifecycle and errors

`golar.New(golar.Config{...})` accepts the server configuration described below.
`app.Run()` uses the configured address (default `:8080`); `app.Run(":9000")`
overrides it. `app.RunContext(ctx)` also supports programmatic cancellation.
Configure routes, middleware, hooks and error handling before serving requests.
An application can run once.

Register `app.OnStart(func(context.Context) error)` hooks to initialize resources.
They run in registration order after the address is bound, before serving.
`app.OnShutdown(func(context.Context) error)` hooks run in reverse order after
requests drain, including when a startup hook fails. Cleanup hooks must safely
handle partially initialized resources. Shutdown hooks receive a fresh context
with the configured shutdown timeout and must honor cancellation; arbitrary
Go functions cannot be forcibly stopped. Server and hook errors are returned
from Run, preserving joined errors for `errors.Is`.

Handlers receive `*golar.Context` and return an error. Unexpected errors are
logged and produce a generic 500 response. Return
`&golar.HTTPError{Code: 400, Message: "Invalid input", Err: err}` for a deliberate
client error; Message is public. Once a response starts, the default error handler
only logs later errors. Panics are handled by the optional Recovery middleware.

Customize returned-error handling with
`app.SetErrorHandler(func(c *golar.Context, err error) { ... })`.
Passing nil restores the default. Custom handlers are responsible for avoiding
a second response after output has started. Register standard HTTP handlers with
`app.Get("/legacy", golar.Adapt(existingHandler))`.
Application route groups expose the same five method helpers as the router.

## Quality checks

```sh
go vet ./...
go test ./...
go test -race ./...
go test -cover ./...
go build ./...
```

GitHub Actions runs formatting, vet, tests, race detection, coverage and build
on pushes and pull requests. The workflow is in `.github/workflows/ci.yml`.

## Router

```go
app := router.New()
app.Get("/users/{id}", showUser)
app.Get("/users/new", newUser)

api := app.Group("/api").Group("/v1")
api.Get("/users", listUsers)
api.Post("/users", createUser)
api.Put("/users/{id}", replaceUser)
api.Patch("/users/{id}", updateUser)
api.Delete("/users/{id}", deleteUser)
```

Import `github.com/dayanchm/golar/http/router`. Handlers use the standard
`func(http.ResponseWriter, *http.Request)` signature. Read dynamic parameters
with `r.PathValue("id")`.

Static segments take priority over dynamic parameters, from left to right,
regardless of registration order. Routes with equal specificity retain their
registration order. Priority applies among routes supporting the request method.

Registering the same method and path twice panics during application setup.
Leading and trailing slashes are ignored when checking duplicates; different
methods on the same path are allowed. Parameter names are compared literally.
Register routes before serving requests.

Unknown paths return 404. Matching paths without a registered request method
return 405 with a sorted, deduplicated `Allow` header containing the registered
methods of all matching routes. HEAD and OPTIONS are not registered implicitly.

## Middleware

```go
// Import github.com/dayanchm/golar/http/middleware.
app := router.New()
app.Use(middleware.Logger(), middleware.Recovery())
app.Get("/", handler)
```

Register middleware before serving requests. Middleware runs in registration
order; the first wraps all subsequent middleware and the router, including
404 and 405 responses. Logger records method, path, status, bytes and duration.
Put Logger before Recovery to log recovered panics with status 500.
Both accept an optional standard `*log.Logger`.

Recovery logs panic details and a stack trace, returning a generic 500 response.
If a response has already started, it aborts the connection instead of appending
an error body. Use `http.NewResponseController(w)` for streaming flushes or
other optional writer operations through the middleware wrappers.

Custom middleware uses `func(http.Handler) http.Handler`. It may call the next
handler or stop the chain, for example to reject an unauthorized request.

## HTTP server

```go
app := router.New()
app.Get("/", handler)
if err := app.Run(":8080"); err != nil {
    log.Fatal(err)
}
```

`Run` handles SIGINT (Ctrl+C) and SIGTERM. It stops accepting connections and
waits up to 10 seconds for active requests to finish. A normal shutdown returns
nil; a shutdown timeout force-closes connections and returns an error.

For custom timeouts and cancellation, import `context`, `time` and
`github.com/dayanchm/golar/http/server`:

```go
srv := server.New(app, server.Config{
    Addr: ":8080",
    ReadTimeout: 15 * time.Second,
    ReadHeaderTimeout: 5 * time.Second,
    WriteTimeout: 30 * time.Second,
    IdleTimeout: 60 * time.Second,
    ShutdownTimeout: 10 * time.Second,
})
if err := srv.Run(context.Background()); err != nil {
    log.Fatal(err)
}
```

These are the default timeouts. Cancel the supplied context to initiate
shutdown programmatically. A server instance supports one serving lifecycle.
Graceful shutdown waits for HTTP requests; hijacked connections such as
WebSockets need their own shutdown handling.
