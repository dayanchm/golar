// Command website builds the Golar website from the repository's markdown:
// a landing page plus one page per guide, written as static HTML.
//
//	go run . -root .. -out dist
package main

import (
	"bytes"
	"embed"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

const repoURL = "https://github.com/dayanchm/golar"

//go:embed templates/*.html assets/*
var files embed.FS

// Page is one generated document.
type Page struct {
	Source string // repository-relative markdown path
	Title  string
	Out    string // site-relative output path

	Body     template.HTML
	Headings []Heading
}

type Heading struct{ ID, Text string }

type Section struct {
	Title string
	Pages []*Page
}

// sections lists the documentation in sidebar order. Titles override the
// markdown's first heading where it reads poorly in navigation.
func sections() []Section {
	p := func(source, title string) *Page { return &Page{Source: source, Title: title, Out: outPath(source)} }
	return []Section{
		{"Getting started", []*Page{p("README.md", "Guide"), p("CHANGELOG.md", "Changelog")}},
		{"HTTP", []*Page{p("docs/http/context.md", "Request context"), p("docs/http/middleware.md", "Middleware"), p("docs/validation.md", "Validation"), p("docs/http/resources.md", "API resources")}},
		{"Database", []*Page{
			p("docs/database/configuration.md", "Configuration"), p("docs/database/connections.md", "Connections"),
			p("docs/database/migrations.md", "Migrations"), p("docs/database/schema-builder.md", "Schema builder"),
			p("docs/database/query-builder.md", "Query builder"), p("docs/database/orm.md", "ORM"),
			p("docs/database/transactions.md", "Transactions"),
		}},
		{"Security and mail", []*Page{p("docs/auth/README.md", "Authentication"), p("docs/mail.md", "Mail")}},
	}
}

// outPath maps a markdown path to its page: README.md becomes docs/index.html,
// docs/x/README.md docs/x/index.html and docs/x.md docs/x.html.
func outPath(source string) string {
	switch source {
	case "README.md":
		return "docs/index.html"
	case "CHANGELOG.md":
		return "docs/changelog.html"
	}
	if path.Base(source) == "README.md" {
		return path.Join(path.Dir(source), "index.html")
	}
	return strings.TrimSuffix(source, ".md") + ".html"
}

// rel returns a link from the page at from to the site path to.
func rel(from, to string) string {
	r, err := filepath.Rel(path.Dir(from), to)
	if err != nil {
		return to
	}
	return filepath.ToSlash(r)
}

type site struct {
	root, out string
	base      string // absolute URL path of the site, for pages served at any path
	version   string
	sections  []Section
	pages     map[string]*Page // by source path
	markdown  goldmark.Markdown
	templates *template.Template
}

func main() {
	root := flag.String("root", "..", "repository root")
	out := flag.String("out", "dist", "output directory")
	base := flag.String("base", "/golar/", "URL path the site is served from, used by 404.html")
	flag.Parse()
	if err := build(*root, *out, *base); err != nil {
		log.Fatal(err)
	}
}

func build(root, out, base string) error {
	s := &site{root: root, out: out, base: base, sections: sections(), pages: map[string]*Page{}}
	for _, section := range s.sections {
		for _, page := range section.Pages {
			s.pages[page.Source] = page
		}
	}
	s.markdown = goldmark.New(
		goldmark.WithExtensions(extension.GFM, highlighting.NewHighlighting(
			highlighting.WithFormatOptions(chromahtml.WithClasses(true)),
		)),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	changelog, err := os.ReadFile(filepath.Join(root, "CHANGELOG.md"))
	if err != nil {
		return err
	}
	if m := regexp.MustCompile(`(?m)^## (v\S+)`).FindSubmatch(changelog); m != nil {
		s.version = string(m[1])
	}
	s.templates, err = template.New("").ParseFS(files, "templates/*.html")
	if err != nil {
		return err
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	for _, page := range s.pages {
		if err := s.renderPage(page); err != nil {
			return fmt.Errorf("%s: %w", page.Source, err)
		}
	}
	if err := s.write("index.html", "home", s.homeData()); err != nil {
		return err
	}
	notFound := s.data("404.html", nil)
	notFound.Base = s.base // served for missing pages at any depth
	if err := s.write("404.html", "notfound", notFound); err != nil {
		return err
	}
	return s.writeAssets()
}

func (s *site) renderPage(page *Page) error {
	source, err := os.ReadFile(filepath.Join(s.root, page.Source))
	if err != nil {
		return err
	}
	if page.Source == "README.md" {
		source = readmeGuide(source)
	}
	body, headings, err := s.render(page, source)
	if err != nil {
		return err
	}
	page.Body, page.Headings = body, headings
	return s.write(page.Out, "doc", s.data(page.Out, page))
}

// readmeGuide drops the README's title, badges and feature list, which the
// home page already shows, keeping everything from installation on.
func readmeGuide(source []byte) []byte {
	if i := bytes.Index(source, []byte("\n## Requirements and installation")); i >= 0 {
		return append([]byte("# Guide\n"), source[i:]...)
	}
	return source
}

// render converts markdown to HTML, rewriting links between markdown files to
// site pages and other repository links to GitHub.
func (s *site) render(page *Page, source []byte) (template.HTML, []Heading, error) {
	doc := s.markdown.Parser().Parse(text.NewReader(source))
	var headings []Heading
	err := ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n := n.(type) {
		case *ast.Heading:
			if id, ok := n.AttributeString("id"); ok && n.Level == 2 {
				if id, ok := id.([]byte); ok {
					headings = append(headings, Heading{ID: string(id), Text: plainText(n, source)})
				}
			}
		case *ast.Link:
			n.Destination = []byte(s.link(page, string(n.Destination)))
		case *ast.Image:
			n.Destination = []byte(s.link(page, string(n.Destination)))
		}
		return ast.WalkContinue, nil
	})
	if err != nil {
		return "", nil, err
	}
	var buf bytes.Buffer
	if err := s.markdown.Renderer().Render(&buf, source, doc); err != nil {
		return "", nil, err
	}
	return template.HTML(buf.String()), headings, nil
}

func plainText(n ast.Node, source []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch c := c.(type) {
			case *ast.Text:
				b.Write(c.Segment.Value(source))
			case *ast.String:
				b.Write(c.Value)
			}
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

var scheme = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func (s *site) link(page *Page, dest string) string {
	if dest == "" || strings.HasPrefix(dest, "#") || scheme.MatchString(dest) {
		return dest
	}
	target, fragment, _ := strings.Cut(dest, "#")
	if fragment != "" {
		fragment = "#" + fragment
	}
	repoPath := path.Clean(path.Join(path.Dir(page.Source), target))
	if linked, ok := s.pages[repoPath]; ok {
		return rel(page.Out, linked.Out) + fragment
	}
	return repoURL + "/blob/main/" + repoPath + fragment
}

type pageData struct {
	Path     string // site-relative path of the page being written
	Base     string // when set, links are absolute from this URL path
	Version  string
	Repo     string
	Sections []Section
	Page     *Page
	Snippets map[string]template.HTML
}

// URL links to the site path to from the page being written.
func (d pageData) URL(to string) string {
	if d.Base != "" {
		return d.Base + to
	}
	return rel(d.Path, to)
}

func (s *site) data(out string, page *Page) pageData {
	return pageData{Path: out, Version: s.version, Repo: repoURL, Sections: s.sections, Page: page}
}

// homeData renders the landing page's code samples with the same highlighter
// as the guides.
func (s *site) homeData() pageData {
	d := s.data("index.html", nil)
	d.Snippets = map[string]template.HTML{}
	for name, code := range homeSnippets(s.version) {
		var buf bytes.Buffer
		if err := s.markdown.Convert([]byte(code), &buf); err != nil {
			log.Fatal(err)
		}
		d.Snippets[name] = template.HTML(buf.String())
	}
	return d
}

func homeSnippets(version string) map[string]string {
	return map[string]string{
		"install": "```sh\ngo install github.com/dayanchm/golar/cmd/golar@" + version + "\n" +
			"golar new blog --template fullstack --frontend react \\\n  --database sqlite --auth session\n" +
			"cd blog && go mod tidy && golar migrate && golar serve\n```\n",
		"code": "```go\n" + `type CreatePost struct {
    Title string ` + "`json:\"title\" validate:\"required,max=120\"`" + `
    Body  string ` + "`json:\"body\" validate:\"required\"`" + `
}

app := golar.New()
app.Use(middleware.RequestID(), middleware.Logger(), middleware.Recovery())

app.Post("/posts", func(c *golar.Context) error {
    var in CreatePost
    if err := c.Validate(&in); err != nil {
        return err // 422 with field errors
    }
    post := models.Post{Title: in.Title, Body: in.Body}
    if err := orm.Create(c.Context(), db, &post); err != nil {
        return err
    }
    return c.JSON(http.StatusCreated, post)
})
` + "```\n",
	}
}

func (s *site) write(out, name string, data pageData) error {
	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}
	target := filepath.Join(s.out, filepath.FromSlash(out))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, buf.Bytes(), 0o644)
}

// writeAssets copies the stylesheet and icon and generates the syntax
// highlighting theme for light and dark mode.
func (s *site) writeAssets() error {
	assets, err := fs.Sub(files, "assets")
	if err != nil {
		return err
	}
	if err := os.CopyFS(filepath.Join(s.out, "assets"), assets); err != nil {
		return err
	}
	// Each theme sits in its own media query so light token colours never
	// apply on the dark background.
	var css bytes.Buffer
	formatter := chromahtml.New(chromahtml.WithClasses(true))
	for _, theme := range []struct{ scheme, style string }{{"light", "github"}, {"dark", "github-dark"}} {
		fmt.Fprintf(&css, "@media (prefers-color-scheme: %s) {\n", theme.scheme)
		if err := formatter.WriteCSS(&css, styles.Get(theme.style)); err != nil {
			return err
		}
		css.WriteString("}\n")
	}
	return os.WriteFile(filepath.Join(s.out, "assets", "highlight.css"), css.Bytes(), 0o644)
}
