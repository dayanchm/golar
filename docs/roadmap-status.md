# Roadmap status (v0.6.0–v0.10.0)

Audit of the implementation plan against the repository at v0.5.1
(`f14049e`). Status values: **existing** (works and is tested), **partial**
(some of the item works; the gap is described) and **missing**. Items marked
**done** were completed after the audit and merged in the pull request or
issue noted.

Baseline at the audit: `go vet ./...` and `go test ./...` pass with no
pre-existing failures.

## Phase 1 — v0.6.0: Validation and API resources

| Item | Status | Notes |
| --- | --- | --- |
| Validation foundation | existing | `validation` package, `Validator`, cached plans, `*validation.Error` → 422 |
| Built-in rules | done (#57) | v0.5.1 had required, email, url, uuid, regex, numeric, min, max, len, between, in, confirmed, unique and exists; added string, integer, boolean, alpha, alpha_num, alpha_dash, starts_with, ends_with and date |
| Custom rules | existing | `Register`, `Validator.Register` |
| Nested JSON validation | done (#57) | nested structs existed; added slices, arrays and maps of structs (`items.0.name`) and `each` element rules |
| Database unique and exists | existing | values are bound; table and column names pass the query builder's identifier check |
| Localized messages | done (#57) | v0.5.1 had per-validator overrides only; added locales, a Turkish catalogue and Accept-Language middleware |
| Form request validation | done (#57) | `c.Validate` existed; added Authorize, Prepare and AfterValidate hooks |
| `make:request` command | done (#57) | |
| Validation tests and docs | done (#57) | unit tests for every new rule, collections, locales and hooks; `docs/validation.md` |
| API resource foundation | done (#57) | `http/resource` |
| Resource collections | done (#57) | |
| Pagination metadata | done (#57) | built on `query.Pagination`, which existed |
| Conditional resource fields | done (#57) | |

Known limitations:

- Locale catalogues are English and Turkish only.
- `AfterValidate` with a pointer receiver runs only when a pointer is
  validated (`c.Validate(&in)` always passes one).
- Resource fields set to `resource.Missing` inside slices are encoded as `{}`.

## Phase 2 — v0.7.0: Frontend and full stack

| Item | Status | Notes |
| --- | --- | --- |
| Frontend integration foundation | existing | `golar new --template web` or `fullstack` |
| Vite integration | done (#58) | v0.5.1 had the Vite config and `/api` proxy; added `http/vite` with manifest-based tags, dev-server tags and the React refresh preamble |
| React, Vue and Svelte templates | existing | `--frontend` react, vue or svelte; configs now enable `build.manifest` |
| Frontend asset management | done (#58) | `vite.Files` with immutable caching for hashed assets, `vite.SPA`, `Config.FS` for embedded builds; Web pages load the bundle with `{{vite ...}}` |
| Development server integration | done (#58) | `golar serve` runs Vite with Go and stops both together |
| Production asset builds | done (#58) | `golar build` |
| Frontend scaffolding commands | done (#58) | `golar make:frontend` react, vue or svelte |
| Frontend integration tests | done (#58) | unit tests with a manifest fixture and fake process runner; scaffold CI already builds each frontend with npm |
| Full-stack docs | done (#58) | `docs/frontend.md` |

Known limitations:

- `golar make:frontend` cannot edit `internal/bootstrap/app.go`; it prints
  the lines to add.
- `golar serve` reloads the frontend, not Go code.
- npm is the only package manager the CLI runs.
- Generated binaries read `frontend/dist` from disk; embedding is documented
  but not generated.

## Phase 3 — v0.8.0: Storage and cache

| Item | Status | Notes |
| --- | --- | --- |
| Filesystem abstraction | done (#59) | `storage.Disk` |
| Local storage driver | done (#59) | `storage.Local` through `os.Root` |
| AWS S3 driver | done (#59) | `storage.S3`; SigV4 checked against the S3 API reference examples |
| S3-compatible storage | done (#59) | path-style addressing, custom endpoints; CI runs the disk tests against Versity Gateway |
| File upload validation | done (#59) | `file`, `image`, `mimetypes`, `extensions`, `max_size`, `min_size`; `PutUpload` |
| Public and private file access | done (#59) | per-disk visibility |
| Temporary signed URLs | done (#59) | HMAC for local disks, presigned URLs for S3 |
| File streaming | done (#59) | streamed `Put` and `Get`, `Stream`, `Local.Handler` with Range |
| Storage integration tests | done (#59) | conformance suite for every disk, fake S3, Versity Gateway in `storage.yml`, an upload flow through a Golar app |
| Cache (all items) | done (#64) | `cache`: memory, file and database stores, a conformance suite on SQLite, PostgreSQL and MySQL, `internal/cache` in generated projects |

Known limitations:

- S3 uploads are single PUTs, so objects are limited to 5 GiB; there is no
  multipart upload yet.
- Visibility is per disk; S3 object ACLs are not managed.
- `Stream` has no Range support for S3; redirect to `TemporaryURL` instead.

## Phase 4 — v0.9.0: Queue, events and mail

| Item | Status | Notes |
| --- | --- | --- |
| Queue (all items) | missing | |
| Events (all items) | missing | |
| Mail foundation | existing | `mail` package (#45) |
| SMTP driver | existing | STARTTLS required by default, TLS, none |
| Email templates | existing | `mail.Template`, HTML escaped |
| Notifications | missing | |

## Phase 5 — v0.10.0: AI integration

Scope, settled in #68: give coding agents accurate, read-only context about a
project through MCP. Tools that change a project, and AI features inside
applications, are not in this phase.

| Item | Status | Notes |
| --- | --- | --- |
| `golar mcp` server | done (#68) | stdio MCP server, protocol versions 2024-11-05 to 2025-06-18 |
| Routes, schema and models | done (#68) | `list_routes`, `describe_schema` (SQLite, PostgreSQL, MySQL), `list_models`, `list_migrations`, `project_info` |
| Read-only tools and secrets | done (#68) | every tool is read-only; `.env` values and DSN parts are redacted from all output |
| Write tools | not planned | would need an explicit start-up flag and `destructiveHint` |

## Next steps

Phases 1 to 3 (#57, #58, #59 and #64) were released together as v0.8.0.

1. Phase 4: queue (#65), events (#66) and notifications (#67).
2. Phase 5: `golar mcp` (#68). The rest of the phase is not yet scoped.

The known limitations above are tracked as #69 (S3 multipart uploads), #70
(reloading Go code in `golar serve`), #71 (`make:frontend` editing bootstrap)
and #72 (embedding the frontend build).
