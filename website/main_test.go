package main

import (
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	hrefPattern = regexp.MustCompile(`(?:href|src)="([^"]*)"`)
	idPattern   = regexp.MustCompile(`id="([^"]*)"`)
)

// TestBuild builds the site from this repository and checks that every page
// exists and every relative link points to an existing file and anchor.
func TestBuild(t *testing.T) {
	out := t.TempDir()
	if err := build("..", out, "/golar/"); err != nil {
		t.Fatal(err)
	}
	pages := map[string]string{}
	err := filepath.WalkDir(out, func(file string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(file, ".html") {
			return err
		}
		data, err := os.ReadFile(file)
		rel, _ := filepath.Rel(out, file)
		pages[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, section := range sections() {
		for _, page := range section.Pages {
			if _, ok := pages[page.Out]; !ok {
				t.Errorf("missing page %s for %s", page.Out, page.Source)
			}
		}
	}
	for _, want := range []string{"index.html", "404.html"} {
		if _, ok := pages[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	for page, html := range pages {
		for _, m := range hrefPattern.FindAllStringSubmatch(html, -1) {
			link := m[1]
			if link == "" || scheme.MatchString(link) {
				continue
			}
			target, fragment, _ := strings.Cut(link, "#")
			switch {
			case target == "":
				target = page
			case strings.HasPrefix(target, "/golar/"):
				target = strings.TrimPrefix(target, "/golar/")
			case strings.HasPrefix(target, "/"):
				t.Errorf("%s: absolute link %q outside the site", page, link)
				continue
			default:
				target = path.Join(path.Dir(page), target)
			}
			if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(target))); err != nil {
				t.Errorf("%s: broken link %q", page, link)
				continue
			}
			if fragment != "" && strings.HasSuffix(target, ".html") {
				found := false
				for _, id := range idPattern.FindAllStringSubmatch(pages[target], -1) {
					found = found || id[1] == fragment
				}
				if !found {
					t.Errorf("%s: missing anchor %q", page, link)
				}
			}
		}
	}
	if !strings.Contains(pages["docs/http/context.html"], `class="chroma"`) {
		t.Error("code blocks are not highlighted")
	}
	if !strings.Contains(pages["index.html"], "@v") {
		t.Error("home page does not show the released version")
	}
}

func TestLinkRewriting(t *testing.T) {
	s := &site{pages: map[string]*Page{}}
	for _, section := range sections() {
		for _, page := range section.Pages {
			s.pages[page.Source] = page
		}
	}
	auth := s.pages["docs/auth/README.md"]
	readme := s.pages["README.md"]
	for _, tt := range []struct {
		page *Page
		dest string
		want string
	}{
		{auth, "../mail.md", "../mail.html"},
		{auth, "#csrf", "#csrf"},
		{auth, "https://example.com", "https://example.com"},
		{readme, "docs/http/context.md#files", "http/context.html#files"},
		{readme, "docs/auth/README.md", "auth/index.html"},
		{readme, "CHANGELOG.md", "changelog.html"},
		{readme, ".github/workflows/ci.yml", repoURL + "/blob/main/.github/workflows/ci.yml"},
		{readme, "LICENSE", repoURL + "/blob/main/LICENSE"},
	} {
		if got := s.link(tt.page, tt.dest); got != tt.want {
			t.Errorf("link(%s, %q) = %q, want %q", tt.page.Source, tt.dest, got, tt.want)
		}
	}
}
