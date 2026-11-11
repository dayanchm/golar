# Contributing to Golar

Thanks for helping. Bug reports, documentation fixes and pull requests are
all welcome. For a larger change, open an issue first so the design can be
agreed before you write the code.

Security problems are not reported here; see [SECURITY.md](SECURITY.md).

## Setup

Golar needs Go 1.27 or newer. Node 20.19+ or 22.12+ is only needed to try
generated projects with a frontend.

```sh
git clone https://github.com/dayanchm/golar
cd golar
go test ./...
```

Run the CLI from the checkout with `go run ./cmd/golar`. To try a generated
project against your local changes, point it at the checkout:

```sh
go run ./cmd/golar new demo --template api --database sqlite --framework-dir "$PWD"
```

## Checks

CI runs these on every pull request; run them before you push:

```sh
gofmt -l .               # must print nothing
go vet ./...
go test -race ./...
```

The website is a separate module:

```sh
cd website && go test ./... && go run . -root .. -out dist
```

The database packages run on SQLite by default. To test PostgreSQL or MySQL,
start a throwaway server and set `GOLAR_TEST_DRIVER` and `GOLAR_TEST_DSN`:

```sh
docker run -d --name golar-pg -p 55432:5432 -e POSTGRES_USER=golar \
  -e POSTGRES_PASSWORD=test-only -e POSTGRES_DB=golar_test postgres:17-alpine
GOLAR_TEST_DRIVER=postgres \
GOLAR_TEST_DSN='postgres://golar:test-only@localhost:55432/golar_test?sslmode=disable' \
  go test -race ./database/... ./auth/... ./validation/... ./http/session/... ./cache/...
```

For MySQL use `mysql:8.4` with
`GOLAR_TEST_DSN='golar:test-only@tcp(localhost:53306)/golar_test?parseTime=true'`.
The S3 tests run against any S3-compatible server when
`GOLAR_TEST_S3_ENDPOINT` and `GOLAR_TEST_S3_BUCKET` are set; see
`.github/workflows/storage.yml`.

## Pull requests

- Branch from `main` and keep one change per pull request.
- Write commit messages and PR titles as
  [Conventional Commits](https://www.conventionalcommits.org/):
  `feat(cache): ...`, `fix(cli): ...`, `docs: ...`, `test(...)`, `ci: ...`,
  `chore: ...`.
- Add tests for new behavior and for every bug you fix.
- Update the guide in `docs/` and the `Unreleased` section of `CHANGELOG.md`
  when users will notice the change. Call out anything that breaks existing
  code under `Changed`.
- Link the issue with `Closes #123`.
- Changes to generated projects go in `internal/scaffold/templates`; the
  scaffold tests and the Scaffold CI workflow build and test the results.

All checks must pass before a pull request is merged into `main`.

## License

By contributing, you agree that your contributions are released under the
[MIT License](LICENSE).
