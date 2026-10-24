# Golar — AI-native full-stack framework for Go

> A modern, modular, AI-native full-stack framework for Go, inspired by Laravel's developer experience.

## CLI and project scaffolding (v0.2.0)

Install the CLI from this checkout:

```sh
go install ./cmd/golar
```

Ensure `$(go env GOPATH)/bin` is on your PATH. Alternatively, run
`go run ./cmd/golar help` from this repository.

```sh
golar new my-app --interactive
# Or use flags without prompts:
golar new my-api --no-interactive --template api --database sqlite --auth token
cd my-api
go mod tidy
golar routes
golar doctor
golar serve --addr :8080
```

| Option | Choices | Default |
| --- | --- | --- |
| `--template` | `api`, `web`, `fullstack` | `api` |
| `--database` | `none`, `sqlite`, `postgres`, `mysql` | `none` |
| `--frontend` | `none`, `react`, `vue`, `svelte` | `react` for Full Stack; otherwise `none` |
| `--auth` | `none`, `token` | `none` |
| `--docker` | boolean | false |
| `--module` | Go module path | project name |
| `--dir` | parent directory for new; project directory for other commands | current directory |

API projects have no frontend; Full Stack requires one. Web defaults to embedded
server-rendered HTML and can also include a frontend. Frontends use
[Vite](https://vite.dev/guide/) with a development proxy to the backend and
production assets served by Go. In `frontend`, run `npm install` and
`npm run dev`; `npm run build` creates production assets. Node 24 is recommended.

The installer prompts on a terminal, or explicitly with `--interactive`.
`--no-interactive` / `--yes` uses defaults and flags, suitable for scripts.
Generation renders and validates all files first and rejects every existing
destination, including empty directories and symlinks. It creates `go.mod`,
`.env` (permissions 0600), `.env.example`, `golar.yaml`, handlers, models,
routes, bootstrap/config, migration/storage folders and runnable tests.
It does not automatically download dependencies or initialize Git.

`golar serve` builds a temporary executable, forwards cancellation for graceful
shutdown and removes the executable afterwards. It has no hot reload yet.
`golar routes` reads the application's actual registered routes without running
startup hooks. `golar doctor` checks project files, Go dependencies, tool versions,
frontend installation, auth configuration and Docker readiness where selected;
it exits nonzero when checks fail. Use `--dir` outside the project directory.

The flat `golar.yaml` manifest records scaffolding choices and the default address.
`.env` and process variables configure runtime values; process values take
precedence, and `serve --addr` takes precedence over both. Changing template,
database or frontend in the manifest does not regenerate an existing project.
The manifest supports flat scalars and JSON, not nested YAML or anchors.

Database starters use `database/sql`, ping on startup and close on shutdown.
SQLite creates its database file; PostgreSQL and MySQL require running services.
Set `DATABASE_DSN` in `.env`. No ORM or automatic migration runner is included.
Token auth protects `/api` using a random shared bearer token stored only in
`.env`; it does not provide user accounts, password login or JWT.

With Docker selected, run `go mod tidy && go mod vendor`, then
`docker compose up --build`. Docker builds use vendored Go modules, including
local framework dependencies. Compose includes a database service for PostgreSQL
or MySQL and persistent storage for SQLite. Database credentials in Compose are
development defaults.

During development the CLI detects its local Golar source tree and adds a
`replace` directive. Use `--framework-dir /path/to/golar` explicitly when needed.
For a published release use `--framework-version v0.2.0` instead. This version
must be tagged and published before remote-only installation works; generation
does not publish framework releases. Remove local replacements before distributing
a project outside your machine.

Scaffold tests exercise every option combination and compile/test generated API,
Web and Full Stack projects. `.github/workflows/scaffold.yml` additionally checks
database starters, all three frontend builds and Compose configuration in CI.

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

## Database and migrations (v0.3.0)

Reinstall the updated CLI from this checkout: `go install ./cmd/golar`.

```sh
golar new database-app --no-interactive --database sqlite
cd database-app
golar make:migration create_users_table
go mod tidy
golar migrate
golar migrate:status
golar migrate:rollback
```

Also available: migrate:reset, migrate:refresh and migrate --pretend. Generated
migrations register through Go init functions and implement Up/Down with Golar's
schema builder. Dry run prints all registered migrations without opening a database;
metadata-dependent operations return explicit offline errors.

The database package provides validated DB_* configuration, SQLite/PostgreSQL/MySQL
connections, pooling, named connections, context-aware raw SQL and panic-safe
transactions. Schema supports tables, columns, indexes, foreign keys, native alters
and SQLite rebuilds. Migration runners hold database locks, track batches and stop
on failure; MySQL partial DDL leaves a durable marker requiring manual repair.

See [configuration](docs/database/configuration.md),
[connections](docs/database/connections.md), [migrations](docs/database/migrations.md),
[schema builder](docs/database/schema-builder.md) and
[transactions](docs/database/transactions.md). Run the complete SQLite example with
`go run ./examples/database`; it uses and cleans up a temporary database.

New database-enabled scaffolds use the framework database package. Existing projects
can continue with DATABASE_DSN and their existing startup code; generated migrations
use the new package through the local framework replacement. The default scaffold
framework version is v0.3.0, which requires publishing a tag for remote-only installs.

Database CI runs real SQLite, PostgreSQL and MySQL migration/transaction/foreign-key
tests with race detection against isolated test databases.

### Query builder (v0.4.0 development)

`db.Table("users").Select("id", "name").Where("active", "=", true).WhereNull("deleted_at").OrderBy("id", "desc").Limit(20).Offset(40)`
builds an immutable, parameterized SELECT. Call `Compile()` (or `ToSQL()`) for SQL and bindings,
or `QueryContext(ctx)` to scan rows using the existing connection.
`db.Table("users").Insert(map[string]any{"name": "Dayanch"})` inserts one row with
sorted, bound columns. `Update` and `Delete` require a `Where` clause (or an explicit
`AllRows()`). Joins, `Count`/`Sum`/`Avg`/`Min`/`Max` and `Paginate(ctx, page, perPage)`
are also available. Transaction
handles support the same `Table` API. See the [query builder guide](docs/database/query-builder.md)
for supported operators, identifier rules and resource handling.

### ORM (v0.4.0 development)

```go
type User struct {
    orm.Model
    Name  string
    Posts []Post `orm:"hasMany"`
}

err := orm.Create(ctx, db, &user)
users, err := orm.Query[User](db).Where("active", "=", true).With("Posts").Get(ctx)
```

Models are plain structs. CRUD, `hasOne`/`hasMany`/`belongsTo`/`belongsToMany`
relationships, N+1-free eager loading and soft deletes are covered in the
[ORM guide](docs/database/orm.md).

### Authentication (v0.4.0 development)

```go
sessions := session.NewManager()
guard := &auth.SessionGuard{Provider: &auth.ORMProvider[User]{DB: db}}
app.Use(sessions.Middleware, auth.CSRF)

user, err := (&auth.Authenticator{Provider: provider}).Attempt(ctx, email, password)
err = guard.Login(r, user)
```

Argon2id password hashing, session and bearer-token guards, route middleware,
gates, policies, roles and permissions, password resets, email verification,
rate limiting and CSRF protection are described in the
[authentication guide](docs/auth/README.md).
