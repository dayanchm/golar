// Package scaffold renders and writes new projects without overwriting files.
package scaffold

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/template"

	"github.com/dayanchm/golar/internal/project"
)

//go:embed all:templates
var templates embed.FS

const FrameworkVersion = "v0.5.0"

type Options struct {
	Name, Module, Template, Database, Frontend, Auth string
	Docker                                           bool
	FrameworkDir, FrameworkVersion                   string
}

type data struct {
	Options
	Driver, DriverImport, DriverModule, DriverVersion, DSN string
	Config                                                 string
	// AppKey is a random secret written to .env for signing, such as email
	// verification links.
	AppKey string
}

// authFiles are rendered only when authentication is enabled.
var authFiles = map[string]bool{
	"app/models/user.go.tmpl":                                        true,
	"app/handlers/auth.go.tmpl":                                      true,
	"database/migrations/20260101_000000_create_users_table.go.tmpl": true,
	"tests/auth_test.go.tmpl":                                        true,
}

var namePattern = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_-]*$`)
var modulePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._~/-]*$`)
var versionPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:[-+][a-zA-Z0-9.-]+)?$`)

func Normalize(o Options) (Options, error) {
	if !namePattern.MatchString(o.Name) {
		return o, fmt.Errorf("project name must start with a letter and contain only letters, digits, _ or -")
	}
	if o.Module == "" {
		o.Module = o.Name
	}
	if !modulePattern.MatchString(o.Module) || strings.Contains(o.Module, "..") || strings.Contains(o.Module, "//") || strings.HasSuffix(o.Module, "/") {
		return o, fmt.Errorf("invalid Go module path %q", o.Module)
	}
	if o.Template == "" {
		o.Template = "api"
	}
	if o.Database == "" {
		o.Database = "none"
	}
	if o.Auth == "" {
		o.Auth = "none"
	}
	if o.Frontend == "" {
		if o.Template == "fullstack" {
			o.Frontend = "react"
		} else {
			o.Frontend = "none"
		}
	}
	for _, choice := range []struct {
		name, value string
		allowed     []string
	}{
		{"template", o.Template, []string{"api", "web", "fullstack"}}, {"database", o.Database, []string{"none", "sqlite", "postgres", "mysql"}}, {"frontend", o.Frontend, []string{"none", "react", "vue", "svelte"}}, {"auth", o.Auth, []string{"none", "token", "session"}},
	} {
		found := false
		for _, v := range choice.allowed {
			if v == choice.value {
				found = true
			}
		}
		if !found {
			return o, fmt.Errorf("invalid %s %q (choose %s)", choice.name, choice.value, strings.Join(choice.allowed, ", "))
		}
	}
	if o.Template == "api" && o.Frontend != "none" {
		return o, fmt.Errorf("API template requires frontend none")
	}
	if o.Template == "fullstack" && o.Frontend == "none" {
		return o, fmt.Errorf("Full Stack template requires a frontend")
	}
	if o.FrameworkVersion == "" {
		o.FrameworkVersion = FrameworkVersion
	}
	if !versionPattern.MatchString(o.FrameworkVersion) {
		return o, fmt.Errorf("invalid framework version %q", o.FrameworkVersion)
	}
	if o.FrameworkDir != "" {
		abs, err := filepath.Abs(o.FrameworkDir)
		if err != nil {
			return o, err
		}
		body, err := os.ReadFile(filepath.Join(abs, "go.mod"))
		if err != nil {
			return o, err
		}
		if !strings.Contains(string(body), "module github.com/dayanchm/golar\n") {
			return o, fmt.Errorf("framework-dir must point to the Golar module")
		}
		o.FrameworkDir = abs
	}
	return o, nil
}

func Render(options Options) (map[string][]byte, error) {
	o, err := Normalize(options)
	if err != nil {
		return nil, err
	}
	// Normalize also validates existing manifests, including legacy token
	// projects without a database, so this check only applies to new projects.
	if o.Auth != "none" && o.Database == "none" {
		return nil, fmt.Errorf("%s authentication stores users in a database (choose sqlite, postgres or mysql)", o.Auth)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	d := data{Options: o, AppKey: base64.RawURLEncoding.EncodeToString(key)}
	switch o.Database {
	case "sqlite":
		d.Driver = "sqlite"
		d.DriverImport = "modernc.org/sqlite"
		d.DriverModule = "modernc.org/sqlite"
		d.DriverVersion = "v1.60.1"
		d.DSN = "storage/app.db"
	case "postgres":
		d.Driver = "pgx"
		d.DriverImport = "github.com/jackc/pgx/v5/stdlib"
		d.DriverModule = "github.com/jackc/pgx/v5"
		d.DriverVersion = "v5.11.0"
		d.DSN = "postgres://golar:golar@localhost:5432/golar?sslmode=disable"
	case "mysql":
		d.Driver = "mysql"
		d.DriverImport = "github.com/go-sql-driver/mysql"
		d.DriverModule = "github.com/go-sql-driver/mysql"
		d.DriverVersion = "v1.10.1"
		d.DSN = "golar:golar@tcp(localhost:3306)/golar?parseTime=true"
	}
	manifest, err := (project.Config{Version: 1, Name: o.Name, Module: o.Module, Template: o.Template, Database: o.Database, Frontend: o.Frontend, Auth: o.Auth, Docker: o.Docker, Address: ":8080"}).Marshal()
	if err != nil {
		return nil, err
	}
	d.Config = string(manifest)
	files := map[string][]byte{}
	err = fs.WalkDir(templates, "templates", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative := strings.TrimPrefix(path, "templates/")
		if strings.HasPrefix(relative, "frontend/") {
			return nil
		}
		if strings.HasPrefix(relative, "docker/") {
			if !o.Docker {
				return nil
			}
			relative = strings.TrimPrefix(relative, "docker/")
		}
		if strings.HasPrefix(relative, "web/") && o.Template == "api" {
			return nil
		}
		if strings.HasPrefix(relative, "internal/database/") && o.Database == "none" {
			return nil
		}
		if o.Auth == "none" && (strings.HasPrefix(relative, "internal/auth/") || authFiles[relative]) {
			return nil
		}
		body, e := templates.ReadFile(path)
		if e != nil {
			return e
		}
		t, e := template.New(path).Option("missingkey=error").Funcs(template.FuncMap{"quote": strconv.Quote}).Parse(string(body))
		if e != nil {
			return e
		}
		var output bytes.Buffer
		if e = t.Execute(&output, d); e != nil {
			return e
		}
		relative = strings.TrimSuffix(relative, ".tmpl")
		result := output.Bytes()
		if strings.HasSuffix(relative, ".go") {
			result, e = format.Source(result)
			if e != nil {
				return fmt.Errorf("format %s: %w", relative, e)
			}
		}
		files[relative] = result
		return nil
	})
	if err != nil {
		return nil, err
	}
	if o.Frontend != "none" {
		if err = renderFrontend(files, d); err != nil {
			return nil, err
		}
	}
	return files, nil
}

// Create refuses existing destinations, including empty directories and symlinks.
// Files are staged first, then installed with exclusive directory creation.
func Create(parent string, options Options) (string, error) {
	files, err := Render(options)
	if err != nil {
		return "", err
	}
	parent, err = filepath.Abs(parent)
	if err != nil {
		return "", err
	}
	target := filepath.Join(parent, options.Name)
	if _, err = os.Lstat(target); err == nil {
		return "", fmt.Errorf("destination already exists: %s", target)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	stage, err := os.MkdirTemp(parent, ".golar-scaffold-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(stage, filepath.FromSlash(name))
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "", err
		}
		mode := os.FileMode(0644)
		if name == ".env" {
			mode = 0600
		}
		if err = os.WriteFile(path, files[name], mode); err != nil {
			return "", err
		}
	}
	if err = os.Mkdir(target, 0755); err != nil {
		return "", fmt.Errorf("create project: %w", err)
	}
	success := false
	defer func() {
		if !success {
			_ = os.RemoveAll(target)
		}
	}()
	entries, err := os.ReadDir(stage)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if err = os.Rename(filepath.Join(stage, entry.Name()), filepath.Join(target, entry.Name())); err != nil {
			return "", err
		}
	}
	success = true
	return target, nil
}
