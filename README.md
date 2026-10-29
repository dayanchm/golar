# Golar — full-stack framework for Go

> A modern, modular full-stack framework for Go, inspired by Laravel's developer experience.

[![CI](https://github.com/dayanchm/golar/actions/workflows/ci.yml/badge.svg)](https://github.com/dayanchm/golar/actions/workflows/ci.yml)
[![Database CI](https://github.com/dayanchm/golar/actions/workflows/database.yml/badge.svg)](https://github.com/dayanchm/golar/actions/workflows/database.yml)
[![Scaffold CI](https://github.com/dayanchm/golar/actions/workflows/scaffold.yml/badge.svg)](https://github.com/dayanchm/golar/actions/workflows/scaffold.yml)

**Website and documentation: [dayanchm.github.io/golar](https://dayanchm.github.io/golar/)**

Golar gives Go applications the batteries Laravel developers expect, built on
the standard library: `net/http` handlers, `database/sql` connections and
`func(http.Handler) http.Handler` middleware all work as usual.

## Features

- **CLI and scaffolding:** `golar new` generates API, Web or Full Stack projects
  with an optional React, Vue or Svelte frontend, database, authentication and
  Docker setup.
- **HTTP:** a router with groups and route priority, error-returning handlers
  with a request context for JSON, forms, uploads, cookies and downloads, a
  graceful server, and middleware for logging, recovery, request IDs, CORS,
  security headers, body limits, timeouts and gzip.
- **Validation:** struct-tag rules with JSON `422` responses.
- **Database:** SQLite, PostgreSQL and MySQL connections, migrations, a schema
  builder, transactions, a query builder and a generics-based ORM with
  relationships and eager loading.
- **Authentication:** Argon2id passwords, session and API token guards, CSRF,
  rate limiting, gates, policies, roles, password resets and email verification.
- **Mail:** SMTP with STARTTLS, a log driver for development, a fake with test
  assertions, templates and ready-made reset and verification emails.

See the [roadmap](#roadmap) for what is coming next.

## Requirements and installation

Go 1.27 or newer. Install the CLI:

```sh
go install github.com/dayanchm/golar/cmd/golar@v0.5.1
```

Make sure `$(go env GOPATH)/bin` is on your `PATH`. From a checkout of this
repository, `go install ./cmd/golar` installs the development version.
Applications that only use the framework can add it with
`go get github.com/dayanchm/golar@v0.5.1`. Changes are listed in
[CHANGELOG.md](CHANGELOG.md).

## Quick start

Create a project:

```sh
golar new my-api --no-interactive --template api --database sqlite --auth token
cd my-api
go mod tidy
golar migrate
golar serve --addr :8080
```

Or use the framework directly:

```go
package main

import (
    "log"
    "net/http"

    "github.com/dayanchm/golar"
    "github.com/dayanchm/golar/http/middleware"
)

type CreateUser struct {
    Name  string `json:"name" validate:"required,max=100"`
    Email string `json:"email" validate:"required,email"`
}

func main() {
    app := golar.New()
    app.Use(middleware.RequestID(), middleware.Logger(), middleware.Recovery(), middleware.SecureHeaders())

    app.Get("/users/{id}", func(c *golar.Context) error {
        return c.JSON(http.StatusOK, map[string]string{"id": c.Param("id")})
    })
    app.Post("/users", func(c *golar.Context) error {
        var in CreateUser
        if err := c.Validate(&in); err != nil {
            return err // 400 for bad JSON, 422 with field errors
        }
        return c.JSON(http.StatusCreated, in)
    })

    if err := app.Run(":8080"); err != nil {
        log.Fatal(err)
    }
}
```

`go run ./examples/basic-api` runs a similar example with `/`, `/health`,
`GET /api/users/{id}` and `POST /api/users`.

## CLI

```sh
golar new my-app --interactive   # prompts for every option
golar routes                     # lists the application's registered routes
golar doctor                     # checks files, dependencies, tools and configuration
golar serve --addr :8080         # builds and runs the app with graceful shutdown
golar make:migration create_posts_table
golar migrate                    # also migrate:status, :rollback, :reset, :refresh, --pretend
```

| `golar new` option | Choices | Default |
| --- | --- | --- |
| `--template` | `api`, `web`, `fullstack` | `api` |
| `--database` | `none`, `sqlite`, `postgres`, `mysql` | `none` |
| `--frontend` | `none`, `react`, `vue`, `svelte` | `react` for Full Stack; otherwise `none` |
| `--auth` | `none`, `token`, `session` (both need a database) | `none` |
| `--docker` | boolean | false |
| `--module` | Go module path | project name |
| `--dir` | parent directory for `new`; project directory for other commands | current directory |

`golar new` prompts on a terminal; `--no-interactive` (or `--yes`) uses defaults
and flags for scripts. It validates every file before writing and refuses
existing destinations, including empty directories and symlinks. It does not
download dependencies or initialize Git. `golar serve` has no hot reload yet.

### Generated projects

Projects contain `go.mod`, `.env` (mode 0600), `.env.example`, `golar.yaml`,
handlers, models, routes, bootstrap and config code, migration and storage
folders, and runnable tests. The default middleware stack adds request IDs,
logging, panic recovery, security headers, a 10 MiB body limit and gzip.

- **Frontends** use [Vite](https://vite.dev/guide/). `npm run dev` in
  `frontend` proxies `/api` and `/health` to Go; `npm run build` produces assets
  that Go serves in production. Node 24 is recommended.
- **Databases:** SQLite creates its file; PostgreSQL and MySQL need a running
  server. Configure `DATABASE_DSN` in `.env` and run `golar migrate`.
- **Authentication:** `--auth token` and `--auth session` generate a `User`
  model, a users migration and rate-limited `/api/auth/register`, `login`,
  `logout` and `me` routes. Token projects issue personal access tokens for
  `Authorization: Bearer`; session projects use a cookie with CSRF protection.
  Projects generated before v0.4.0 keep their shared `AUTH_TOKEN`.
- **Docker:** run `go mod tidy && go mod vendor`, then `docker compose up --build`.
  Compose adds a PostgreSQL or MySQL service, or persistent storage for SQLite.
  Its credentials are development defaults.

`golar.yaml` records the scaffolding choices and default address. `.env` and
process variables configure runtime values; process values win, and
`serve --addr` wins over both. Editing the manifest does not regenerate a
project. It supports flat scalars and JSON, not nested YAML.

### Framework source

Inside a Golar checkout the CLI detects the source tree and adds a `replace`
directive; pass `--framework-dir /path/to/golar` to choose it explicitly.
Elsewhere, projects require the tagged release given by `--framework-version`
(default `v0.5.1`). Remove local replacements before distributing a project.

## Applications and errors

`golar.New(golar.Config{...})` creates an application. `app.Run()` listens on the
configured address (default `:8080`), `app.Run(":9000")` overrides it and
`app.RunContext(ctx)` stops when `ctx` is canceled. Configure routes, middleware,
hooks and error handling before serving; an application runs once.

`app.OnStart(func(context.Context) error)` hooks run in order after the address
is bound and before serving. `app.OnShutdown` hooks run in reverse order after
requests drain, also when a startup hook fails, with a fresh context bounded by
the shutdown timeout. Server and hook errors are returned from `Run` and work
with `errors.Is`.

Handlers receive `*golar.Context` and return an error:

| Returned error | Response |
| --- | --- |
| `&golar.HTTPError{Code: 404, Message: "Post not found"}` | that status and message |
| `*validation.Error`, for example from `c.Validate` | `422 {"errors": {"field": ["message"]}}` |
| `*http.MaxBytesError`, from the body limits or `BodyLimit` | `413` |
| `httpcontext.ErrUnsupportedMediaType` from `c.Bind` | `415` |
| a body `c.Bind` cannot decode, or a missing upload | `400 Invalid request body` |
| `*httpcontext.InputError`, for example from `c.QueryInt` | `400 Invalid query parameter "page"` |
| anything else | logged, generic `500` |

Once a response has started, later errors are only logged. Replace the handler
with `app.SetErrorHandler(func(c *golar.Context, err error) { ... })`; nil
restores the default. Mount standard handlers with `golar.Adapt(handler)`.

## Validation

```go
type RegisterRequest struct {
    Name     string `json:"name" validate:"required,max=100"`
    Email    string `json:"email" validate:"required,email,unique=users.email"`
    Password string `json:"password" validate:"required,min=8,confirmed"`
}
```

Rules include `required`, `email`, `url`, `uuid`, `numeric`, `string`,
`integer`, `boolean`, `alpha`, `alpha_num`, `alpha_dash`, `starts_with`,
`ends_with`, `date`, `min`, `max`, `len`, `between`, `in`, `regex`,
`confirmed`, `unique` and `exists`. Slices, arrays and maps are validated
element by element (`items.0.sku`, `each` rules), messages can be localized
(Turkish is built in), and request structs can add `Authorize`, `Prepare` and
`AfterValidate` hooks; `golar make:request` generates one. See the
[validation guide](docs/validation.md).

## API resources

```go
func UserResource(ctx context.Context, u models.User) resource.Object {
    return resource.Object{"id": u.ID, "name": u.Name, "email": resource.When(isAdmin(ctx), u.Email)}
}

users, page, err := orm.Query[models.User](db).Paginate(ctx, pageNumber, 15)
return c.JSON(http.StatusOK, resource.Paginated(ctx, users, page, UserResource, resource.PageURL(c.Request, "page")))
```

Resources name every field they send, so private fields stay private.
`Item`, `Collection` and `Paginated` wrap them in `{"data": ...}` with
pagination `meta` and `links`. See the [resources guide](docs/http/resources.md).

## Request context

```go
app.Post("/avatar", func(c *golar.Context) error {
    page, err := c.QueryInt("page", 1)
    if err != nil {
        return err // 400 Invalid query parameter "page"
    }
    file, err := c.File("avatar")
    if err != nil {
        return err // 400 when missing, 413 when too large
    }
    if err := c.SaveFile(file, filepath.Join("storage", "avatars", rand.Text())); err != nil {
        return err
    }
    if err := c.SetCookie("last_page", strconv.Itoa(page), 24*time.Hour); err != nil {
        return err
    }
    return c.Redirect(http.StatusSeeOther, "/profile")
})
```

`c.Bind` and `c.Validate` read JSON, URL-encoded or multipart bodies. Bodies are
limited to 4 MiB, or 32 MiB for multipart, unless changed with
`app.SetContextConfig`. Responses include `JSON`, `String`, `HTML`, `Render`,
`Blob`, `NoContent`, `Redirect` and `Download`; `c.IP()` honours
`X-Forwarded-For` only from trusted proxies. See the
[request context guide](docs/http/context.md).

## Router

```go
app.Get("/users/{id}", showUser)
app.Get("/users/new", newUser) // static segments win over parameters

api := app.Group("/api").Group("/v1")
api.Post("/users", createUser)
api.Put("/users/{id}", replaceUser)
api.Patch("/users/{id}", updateUser)
api.Delete("/users/{id}", deleteUser)
```

Read parameters with `c.Param("id")`, or `r.PathValue("id")` when using
`github.com/dayanchm/golar/http/router` directly with standard handlers.

- Static segments take priority over parameters from left to right, regardless
  of registration order.
- Registering the same method and path twice panics during setup.
- Unknown paths return 404. Known paths with another method return 405 with a
  sorted `Allow` header.
- HEAD is served by the GET route unless a HEAD route exists. OPTIONS is not
  registered implicitly; `middleware.CORS` answers preflight requests.

## Middleware

```go
app.Use(
    middleware.RequestID(),
    middleware.Logger(),
    middleware.Recovery(),
    middleware.SecureHeaders(),
    middleware.CORS(middleware.CORSConfig{AllowOrigins: []string{"https://app.example.com"}}),
    middleware.BodyLimit(10 << 20),
    middleware.Timeout(30 * time.Second),
    middleware.Compress(),
)
```

Middleware runs in registration order, with the first outermost, and wraps 404
and 405 responses too. Every middleware is opt-in and configurable; see the
[middleware guide](docs/http/middleware.md). Custom middleware has the standard
`func(http.Handler) http.Handler` shape. Use `http.NewResponseController(w)` to
flush or hijack through the wrappers.

## HTTP server

`Run` handles SIGINT and SIGTERM, stops accepting connections and waits up to 10
seconds for active requests. For custom timeouts, use `http/server`:

```go
srv := server.New(app, server.Config{
    Addr:              ":8080",
    ReadTimeout:       15 * time.Second,
    ReadHeaderTimeout: 5 * time.Second,
    WriteTimeout:      30 * time.Second,
    IdleTimeout:       60 * time.Second,
    ShutdownTimeout:   10 * time.Second,
})
err := srv.Run(ctx)
```

These are the defaults. A shutdown that times out force-closes connections and
returns an error. Hijacked connections such as WebSockets need their own shutdown
handling.

## Database

```go
config, err := database.ConfigFromEnv() // DB_DRIVER, DATABASE_DSN or DB_HOST, DB_DATABASE, ...
db, err := database.Connect(config)

rows, err := db.Table("users").
    Select("id", "name").
    Where("active", "=", true).
    OrderBy("id", "desc").
    Limit(20).
    QueryContext(ctx)
```

- **Connections:** validated `DB_*` configuration, pooling, named connections,
  context-aware raw SQL and panic-safe transactions.
- **Migrations and schema:** Go migrations with `Up`/`Down`, batches, locking and
  `--pretend`; tables, columns, indexes, foreign keys, native alters and SQLite
  rebuilds.
- **Query builder:** immutable, parameterized SELECT, INSERT, UPDATE and DELETE
  with joins, `WhereIn`, aggregates and pagination. `Update` and `Delete` require
  a `Where` clause or an explicit `AllRows()`.
- **ORM:** plain struct models, CRUD, `hasOne`, `hasMany`, `belongsTo` and
  `belongsToMany`, N+1-free eager loading and soft deletes.

```go
type User struct {
    orm.Model
    Name  string
    Posts []Post `orm:"hasMany"`
}

err := orm.Create(ctx, db, &user)
users, err := orm.Query[User](db).Where("active", "=", true).With("Posts").Get(ctx)
```

Guides: [configuration](docs/database/configuration.md),
[connections](docs/database/connections.md),
[migrations](docs/database/migrations.md),
[schema builder](docs/database/schema-builder.md),
[transactions](docs/database/transactions.md),
[query builder](docs/database/query-builder.md) and [ORM](docs/database/orm.md).
`go run ./examples/database` runs a complete SQLite example.

## Authentication

```go
sessions := &session.Manager{Store: &session.SQLStore{DB: db}} // or session.NewManager() in memory
guard := &auth.SessionGuard{Provider: &auth.ORMProvider[User]{DB: db}}
app.Use(sessions.Middleware, auth.CSRF)

user, err := (&auth.Authenticator{Provider: provider}).Attempt(ctx, email, password)
err = guard.Login(r, user)
```

Sessions can live in memory or in a SQL table that survives restarts and is
shared between instances, with session IDs stored as SHA-256 hashes.
Argon2id password hashing, session and bearer-token guards, route protection,
gates, policies, roles and permissions, password resets, email verification,
rate limiting and CSRF protection are described in the
[authentication guide](docs/auth/README.md).

## Mail

```go
mailer, err := mail.FromEnv() // MAIL_DRIVER=smtp|log, MAIL_HOST, MAIL_FROM_ADDRESS, ...
err = mailer.Send(ctx, mail.Message{To: []string{"ada@example.com"}, Subject: "Hi", Text: "Hello"})

broker.Send = auth.PasswordResetMail{Mailer: mailer, URL: "https://app.example.com/reset-password"}.Send

fake := mail.NewFake() // in tests
fake.AssertSent(t, "ada@example.com")
```

Generated projects with authentication send password reset and verification
emails from `.env` settings alone; they log mail until `MAIL_DRIVER=smtp` is
set. See the [mail guide](docs/mail.md).

## Development

```sh
go vet ./...
go test -race ./...
go build ./...
```

CI checks formatting, vet, tests with race detection, coverage and builds
([ci.yml](.github/workflows/ci.yml)); runs the database, auth, validation
and session store tests against real SQLite, PostgreSQL and MySQL ([database.yml](.github/workflows/database.yml));
and generates, migrates and tests projects for every template, frontend and
auth mode ([scaffold.yml](.github/workflows/scaffold.yml)).

## Roadmap

Planned work is tracked in [GitHub milestones](https://github.com/dayanchm/golar/milestones).
With the mailer, the planned v0.5.0 features are in place. AI-native tooling,
such as a `golar mcp` command that exposes routes, schema and models to coding
agents, is planned but not yet available.
