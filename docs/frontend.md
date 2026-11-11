# Frontend and full stack

Golar projects can carry a React, Vue or Svelte frontend built with Vite. Go
serves the API and, in production, the built files; in development Vite serves
the frontend with hot reloading.

API projects never need Node. Everything below applies only to projects with a
frontend.

## Requirements

- Node.js 20.19+, 22.12+ or 24+ (24 recommended) and npm.
- `golar doctor` checks both and whether `frontend/node_modules` exists.

## Creating a project

```sh
golar new shop --template fullstack --frontend react   # single-page app
golar new blog --template web --frontend vue           # Go pages with a Vite bundle
cd shop/frontend && npm install
```

| Template | Who renders pages | How the frontend loads |
| --- | --- | --- |
| `fullstack` | the frontend (single-page app) | `frontend/index.html`; Go answers unknown paths with it |
| `web` | Go templates in `web/templates` | `{{vite "src/main.js"}}` in a Go template mounts the bundle into `<div id="app">` |

To add a frontend to an existing project:

```sh
golar make:frontend svelte
```

It writes `frontend/`, sets `frontend:` in `golar.yaml` (an `api` project
becomes `fullstack`) and wires the frontend in: `internal/bootstrap/app.go`
and, for `web` projects, `web/render.go` and `web/templates/index.html` are
replaced with what `golar new` generates for a project with that frontend. A
file is only replaced when it is unchanged since `golar new`; a file you
edited is left alone, and the command prints the lines to add to it. It
refuses projects that already have a frontend or a `frontend/` directory.

## Development

```sh
golar serve
```

`golar serve` builds and starts the Go server and runs `npm run dev` in
`frontend` at the same time:

| Variable | Set for | Value |
| --- | --- | --- |
| `VITE_DEV_SERVER_URL` | the Go server and Vite | `http://localhost:5173` |
| `GOLAR_BACKEND_URL` | Vite | the Go server, such as `http://localhost:8080` |

- For `fullstack` projects, open the Vite URL. Vite proxies `/api` and `/health`
  to Go.
- For `web` projects, open the Go server. Pages load scripts from Vite, so
  edits reload instantly.

When either process exits, the other is stopped; Ctrl+C stops both. Use
`--vite-port 5174` for another port and `--no-vite` to start only Go. Vite
listens with `--strictPort`, so a port in use is an error rather than a silent
switch to another port.

## Production builds

```sh
golar build                      # npm run build, then go build -trimpath -o bin/server
golar build --output /tmp/app    # another binary path
golar build --no-frontend        # skip npm
```

Vite writes `frontend/dist` with hashed file names and
`.vite/manifest.json`. `golar build` then compiles the server with
`-tags golar_embed`, which embeds `frontend/dist` through the generated
`frontend` package, so `bin/server` serves the frontend from any directory.
Other builds, such as `golar serve`, `go run` and a plain `go build`, leave
`frontend.FS` nil and read `frontend/dist` from the working directory, so
development needs no build. Built files under `assets/` are sent with
`Cache-Control: public, max-age=31536000, immutable`; everything else, including
`index.html`, with `Cache-Control: no-cache`. The manifest itself is not served.

Generated projects pass the embedded build to the integration:

```go
import "example.com/app/frontend"

assets := vite.New(vite.Config{FS: frontend.FS}) // nil FS reads frontend/dist
```

To build the binary yourself, run `npm run build` in `frontend` first, since
`go:embed` needs the files at compile time, then
`go build -tags golar_embed ./cmd/server`. The generated Dockerfile does the
same. Projects created before this have no `frontend/embed.go`; `golar build`
then builds without the tag and the binary reads `frontend/dist` from disk.

## The `vite` package

`github.com/dayanchm/golar/http/vite` connects Go to Vite:

```go
assets := vite.New(vite.Config{}) // Dir "frontend/dist", Base "/"

// Go templates (web projects)
templates := template.Must(template.New("").Funcs(assets.FuncMap()).ParseFS(files, "templates/*.html"))

// Middleware
app.Use(assets.Files)                                        // built files only
app.Use(assets.SPA(vite.PathPrefix("/api", "/health")))      // files plus index.html fallback
```

In templates:

```html
<head>
  {{vite_react_refresh}}            <!-- React only; empty in production -->
  {{vite "src/main.jsx"}}           <!-- several entries: {{vite "src/main.js" "src/admin.js"}} -->
</head>
<img src="{{vite_asset "src/logo.svg"}}">
```

| Mode | `vite "src/main.js"` renders |
| --- | --- |
| development (`VITE_DEV_SERVER_URL` set) | the Vite client and `<script type="module" src="http://localhost:5173/src/main.js">` |
| production | `<link rel="stylesheet">` for the entry's CSS and its imports' CSS, `<link rel="modulepreload">` for shared chunks, and `<script type="module">` for the hashed entry |

Entries are keys of the manifest, which Vite creates for the inputs in
`build.rollupOptions.input` (by default `index.html` and what it loads). An
unknown entry is an error. Before the first build, `vite` renders an HTML
comment instead of failing, so pages still load. `assets.Tags` and
`assets.URL` give the same results in Go code.

`Base` must match Vite's `base` option when the frontend is served from a sub
path such as `/static/`. `DevURL` overrides `VITE_DEV_SERVER_URL`.

`SPA` serves `index.html` for `GET` and `HEAD` requests to paths without a file
extension, so client-side routes such as `/dashboard/settings` work on reload.
Requests the skip function matches, other methods, missing files with an
extension and every request before `index.html` is built go to the next
handler.
