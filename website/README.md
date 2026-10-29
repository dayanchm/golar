# Website

The source of the Golar website. It renders the repository's `README.md`,
`CHANGELOG.md` and `docs/` into static HTML with a landing page, and the
[Website workflow](../.github/workflows/pages.yml) publishes it to GitHub Pages
on every push to `main`.

```sh
cd website
go run . -root .. -out dist   # build into website/dist
go test ./...                 # build and check every internal link
python3 -m http.server --directory dist
```

Add a guide by writing it under `docs/` and listing it in `sections` in
`main.go`. Links between markdown files become links between pages; other
repository links point to GitHub. This is a separate Go module, so its
dependencies do not reach applications that import Golar.
