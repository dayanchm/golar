package scaffold

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/dayanchm/golar/internal/project"
)

func TestTemplateMatrix(t *testing.T) {
	for _, kind := range []string{"api", "web", "fullstack"} {
		fronts := []string{"none"}
		if kind == "web" {
			fronts = []string{"none", "react", "vue", "svelte"}
		}
		if kind == "fullstack" {
			fronts = []string{"react", "vue", "svelte"}
		}
		for _, front := range fronts {
			for _, db := range []string{"none", "sqlite", "postgres", "mysql"} {
				for _, auth := range []string{"none", "token", "session"} {
					for _, docker := range []bool{false, true} {
						t.Run(kind+"/"+front+"/"+db+"/"+auth+"/"+map[bool]string{false: "plain", true: "docker"}[docker], func(t *testing.T) {
							files, err := Render(Options{Name: "demo", Module: "example.com/demo", Template: kind, Frontend: front, Database: db, Auth: auth, Docker: docker})
							if auth != "none" && db == "none" {
								if err == nil {
									t.Fatal("auth without a database accepted")
								}
								return
							}
							if err != nil {
								t.Fatal(err)
							}
							for _, path := range []string{"go.mod", ".env", ".env.example", "golar.yaml", "cmd/server/main.go", "routes/routes.go", "tests/application_test.go", "app/models/.gitkeep", "storage/.gitkeep", "database/migrations/.gitkeep"} {
								if _, ok := files[path]; !ok {
									t.Errorf("missing %s", path)
								}
							}
							for path, body := range files {
								if strings.HasSuffix(path, ".go") {
									if _, err := parser.ParseFile(token.NewFileSet(), path, body, parser.AllErrors); err != nil {
										t.Error(err)
									}
								}
							}
							if _, ok := files["Dockerfile"]; ok != docker {
								t.Error("Docker choice ignored")
							}
							if _, ok := files["frontend/package.json"]; ok != (front != "none") {
								t.Error("frontend choice ignored")
							}
							if _, ok := files["web/render.go"]; ok != (kind != "api") {
								t.Error("web choice ignored")
							}
							if _, ok := files["internal/database/database.go"]; ok != (db != "none") {
								t.Error("database choice ignored")
							}
							for _, path := range []string{"internal/auth/auth.go", "app/models/user.go", "app/handlers/auth.go", "database/migrations/20260101_000000_create_users_table.go", "tests/auth_test.go"} {
								if _, ok := files[path]; ok != (auth != "none") {
									t.Errorf("auth choice ignored for %s", path)
								}
							}
							if strings.Contains(string(files[".env"]), "AUTH_TOKEN") {
								t.Error("shared token still generated")
							}
							if strings.Contains(string(files[".env"]), "SESSION_SECURE_COOKIE") != (auth == "session") {
								t.Error("session cookie setting mismatch")
							}
							key := regexp.MustCompile(`(?m)^APP_KEY=(\S*)$`).FindStringSubmatch(string(files[".env"]))
							if (key != nil) != (auth != "none") || key != nil && len(key[1]) < 32 {
								t.Errorf("APP_KEY = %q", key)
							}
							if strings.Contains(string(files[".env.example"]), "APP_KEY=\n") != (auth != "none") {
								t.Error(".env.example must list APP_KEY without a value")
							}
							if strings.Contains(string(files[".env"]), "MAIL_DRIVER=log") != (auth != "none") {
								t.Error("mail settings mismatch")
							}
						})
					}
				}
			}
		}
	}
}

func TestCreateAndConflict(t *testing.T) {
	parent := t.TempDir()
	target, err := Create(parent, Options{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	config, err := project.Load(target)
	if err != nil {
		t.Fatal(err)
	}
	if config.Name != "demo" || config.Template != "api" {
		t.Fatal(config)
	}
	original, err := os.ReadFile(filepath.Join(target, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Create(parent, Options{Name: "demo", Template: "web"}); err == nil {
		t.Fatal("overwrote existing project")
	}
	current, _ := os.ReadFile(filepath.Join(target, "go.mod"))
	if string(original) != string(current) {
		t.Fatal("existing file changed")
	}
	info, err := os.Stat(filepath.Join(target, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("env mode = %v", info.Mode())
	}
	entries, _ := os.ReadDir(parent)
	if len(entries) != 1 {
		t.Fatal("staging directory leaked")
	}
}

func TestConflictingDestinations(t *testing.T) {
	for _, kind := range []string{"empty-dir", "file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			parent := t.TempDir()
			target := filepath.Join(parent, "demo")
			switch kind {
			case "empty-dir":
				if err := os.Mkdir(target, 0755); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(target, []byte("keep"), 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(filepath.Join(parent, "missing"), target); err != nil {
					t.Skip(err)
				}
			}
			if _, err := Create(parent, Options{Name: "demo"}); err == nil {
				t.Fatal("expected conflict")
			}
		})
	}
}

func TestInvalidOptions(t *testing.T) {
	for _, o := range []Options{{Name: "../escape"}, {Name: "."}, {Name: "bad name"}, {Name: "demo", Module: "x\nreplace foo => bad"}, {Name: "demo", Module: "../bad"}, {Name: "demo", Template: "invalid"}, {Name: "demo", Database: "invalid"}, {Name: "demo", Frontend: "invalid"}, {Name: "demo", Auth: "invalid"}, {Name: "demo", Auth: "token"}, {Name: "demo", Auth: "session"}, {Name: "demo", Template: "api", Frontend: "vue"}, {Name: "demo", Template: "fullstack", Frontend: "none"}, {Name: "demo", FrameworkVersion: "latest\n"}, {Name: "demo", FrameworkDir: t.TempDir()}} {
		if _, err := Render(o); err == nil {
			t.Errorf("accepted invalid options: %+v", o)
		}
	}
}
func TestFullStackDefaults(t *testing.T) {
	o, err := Normalize(Options{Name: "demo", Template: "fullstack"})
	if err != nil || o.Frontend != "react" {
		t.Fatalf("options=%+v err=%v", o, err)
	}
}

// Manifests of projects generated before v0.4.0 combine token auth with no database.
func TestLegacyTokenManifest(t *testing.T) {
	if _, err := Normalize(Options{Name: "demo", Auth: "token", Database: "none"}); err != nil {
		t.Fatal(err)
	}
}
