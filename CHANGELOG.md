# Changelog

## Unreleased

### Added

- **Validation rules:** `string`, `integer`, `boolean`, `alpha`, `alpha_num`,
  `alpha_dash`, `starts_with`, `ends_with` and `date`.
- **Collections:** structs in slices, arrays and maps are validated under keys
  such as `items.0.sku`; rules after `each` apply to every element; `Messages`
  and `Attributes` accept `*` for indexes.
- **Locales:** `AddLocale`, `WithLocale`, `MatchLocale` and `LocaleMiddleware`,
  with a built-in Turkish catalogue. English stays the default.
- **Form requests:** `c.Validate` calls `Authorize` (deny with
  `httpcontext.ErrForbidden`, rendered as 403) and `Prepare`;
  `validation.AfterValidator` adds cross-field checks.
- **API resources** (`http/resource`): `Object`, `Item`, `Collection`,
  `Paginated` with `meta` and `links`, `PageURL`, and the conditional helpers
  `When`, `WhenFunc`, `WhenNotNil` and `MergeWhen`.
- **Vite integration** (`http/vite`): `Tags`, `URL` and `ReactRefresh` resolve
  entries through Vite's manifest in production and the dev server in
  development; `FuncMap` exposes them to templates as `vite`, `vite_asset` and
  `vite_react_refresh`. `Files` serves the build with immutable caching for
  hashed assets, and `SPA` adds the `index.html` fallback. Builds can be
  embedded with `Config.FS`.
- **CLI:** `golar make:request <name>`. `golar serve` runs `npm run dev` alongside the server for projects
  with a frontend (`--no-vite`, `--vite-port`); `golar build` builds the
  frontend and the server; `golar make:frontend react|vue|svelte` adds a
  frontend to an existing project.
- **Docs:** a full-stack guide (`docs/frontend.md`).

### Changed

- Structs inside slices, arrays and maps are now validated. Requests whose
  element structs carry `validate` tags can fail validation where v0.5.1
  accepted them.
- `integer`, like `numeric`, makes size rules compare string values as numbers.
- Generated Vite configs enable `build.manifest` and `server.origin`.
- Generated Full Stack projects serve `frontend/dist` with `vite.SPA`. Web
  projects with a frontend now render their pages in Go and load the bundle
  with `{{vite ...}}` instead of replacing them with `frontend/dist/index.html`.

## v0.5.1 — 2026-10-28

### Fixed

- `golar new` from a CLI installed with `go install ...@v0.5.0` treated the
  module cache as a local Golar checkout and wrote a `replace` directive to
  it, so generated projects did not build on other machines. Installed CLIs
  now require the published version; `--framework-dir` still selects a
  checkout.
- Generated projects default to `github.com/dayanchm/golar v0.5.1`, and
  `golar version` reports `0.5.1`.

## v0.5.0 — 2026-10-28

The first tagged release. Install the CLI with
`go install github.com/dayanchm/golar/cmd/golar@v0.5.0`; generated projects
require `github.com/dayanchm/golar v0.5.0`.

### Added

- **Validation** (`validation`, #41): struct-tag rules (`required`, `email`,
  `url`, `uuid`, `regex`, `numeric`, `min`, `max`, `len`, `between`, `in`,
  `confirmed`, `unique`, `exists`), custom rules and messages, and nested
  structs. `c.Validate` binds and validates a request; invalid input becomes
  `422 {"errors": {...}}`.
- **Middleware** (`http/middleware`, #43): `CORS`, `RequestID`, `Timeout`,
  `BodyLimit`, `SecureHeaders` and `Compress`. `Logger` logs the request ID.
- **Request context** (`http/context`, #42):
  - `Bind` for JSON, URL-encoded and multipart bodies;
  - `Form`, `File`, `SaveFile`, `QueryDefault` and `QueryInt`;
  - `Cookie`, `SetCookie` with secure defaults, `SetRawCookie` and `ClearCookie`;
  - `HTML`, `Render`, `Blob`, `NoContent`, `Redirect` and `Download`;
  - `IP` and `IsTLS`, which trust forwarded headers only from configured proxies;
  - `WantsJSON` and `Context`.
- **SQL session store** (`http/session`, #44): `SQLStore` keeps sessions in a
  `sessions` table keyed by the SHA-256 of their ID, so they survive restarts
  and are shared between instances. `CreateTables`, `Prune` and `PruneEvery`
  manage the table.
- **Mail** (`mail`, #45): messages with header-injection checks, SMTP
  (STARTTLS by default), log and fake drivers, templates and `FromEnv`.
  `auth.PasswordResetMail` and `auth.VerificationMail` send the auth emails.
- **Authentication and authorization** (`auth`): Argon2id passwords, session
  and API token guards, CSRF, rate limiting, gates, policies, roles, password
  resets and email verification.
- **ORM and query builder** (`database/orm`, `database/query`): models, CRUD,
  relationships, eager loading, soft deletes, joins, aggregates, pagination and
  dialect-aware SQL for SQLite, PostgreSQL and MySQL.
- **Scaffolding:**
  - `golar new --auth token|session` generates user accounts;
  - projects with authentication get password reset and email verification routes;
  - generated projects register RequestID, Logger, Recovery, SecureHeaders,
    BodyLimit and Compress;
  - session projects store sessions in the database unless `SESSION_DRIVER=memory`.

### Changed

- The default error handler maps oversized bodies to 413, unsupported body
  types to 415, and undecodable bodies and invalid query parameters to 400.
- Request bodies read through `Context` are limited by default: 4 MiB, or
  32 MiB for multipart. Change the limits with `app.SetContextConfig`.
- Router: HEAD requests are served by GET routes, and `Allow` lists HEAD.
- `golar version` reports `0.5.0`.

### Fixed

- Password reset times are stored in whole seconds, so MySQL compares them
  correctly.
