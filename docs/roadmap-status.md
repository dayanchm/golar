# Roadmap status (v0.6.0–v0.10.0)

Audit of the implementation plan against the repository at v0.5.1
(`f14049e`). Status values: **existing** (works and is tested), **partial**
(some of the item works; the gap is described) and **missing**. Items marked
**done** were completed after the audit, on the branch noted; they are not on
`main` until that branch is reviewed and merged.

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
- `unique` still cannot ignore the current record on updates; a custom rule is
  needed, as before.
- `AfterValidate` with a pointer receiver runs only when a pointer is
  validated (`c.Validate(&in)` always passes one).
- Resource fields set to `resource.Missing` inside slices are encoded as `{}`.

## Phase 2 — v0.7.0: Frontend and full stack

| Item | Status | Notes |
| --- | --- | --- |
| Frontend integration foundation | existing | `golar new --template web` or `fullstack` |
| Vite integration | done (`feat/v0.7`) | v0.5.1 had the Vite config and `/api` proxy; added `http/vite` with manifest-based tags, dev-server tags and the React refresh preamble |
| React, Vue and Svelte templates | existing | `--frontend` react, vue or svelte; configs now enable `build.manifest` |
| Frontend asset management | done (`feat/v0.7`) | `vite.Files` with immutable caching for hashed assets, `vite.SPA`, `Config.FS` for embedded builds; Web pages load the bundle with `{{vite ...}}` |
| Development server integration | done (`feat/v0.7`) | `golar serve` runs Vite with Go and stops both together |
| Production asset builds | done (`feat/v0.7`) | `golar build` |
| Frontend scaffolding commands | done (`feat/v0.7`) | `golar make:frontend` react, vue or svelte |
| Frontend integration tests | done (`feat/v0.7`) | unit tests with a manifest fixture and fake process runner; scaffold CI already builds each frontend with npm |
| Full-stack docs | done (`feat/v0.7`) | `docs/frontend.md` |

Known limitations:

- `golar make:frontend` cannot edit `internal/bootstrap/app.go`; it prints
  the lines to add.
- `golar serve` reloads the frontend, not Go code.
- npm is the only package manager the CLI runs.
- Generated binaries read `frontend/dist` from disk; embedding is documented
  but not generated.

## Phase 3 — v0.8.0: Storage and cache

All items **missing**: no filesystem, S3, upload-validation, signed-URL,
streaming or cache packages exist. `c.SaveFile` and `c.Download` cover single
local files only.

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

All items **missing**.

## Next steps

1. Review and merge `feat/v0.7` (Phase 2). Phase 1 was merged in #57.
2. Phase 3 (storage and cache) onwards in the plan's order.
