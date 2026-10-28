# Changelog

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
