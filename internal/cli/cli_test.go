package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayanchm/golar/internal/scaffold"
)

func testCLI(t *testing.T) (*CLI, *bytes.Buffer) {
	t.Helper()
	out := new(bytes.Buffer)
	return &CLI{Dir: t.TempDir(), In: strings.NewReader(""), Out: out, Err: new(bytes.Buffer)}, out
}
func TestNewProject(t *testing.T) {
	c, out := testCLI(t)
	if err := c.Run(context.Background(), []string{"new", "demo", "--no-interactive", "--template", "web", "--module", "example.com/demo"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Created") {
		t.Fatal(out.String())
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "demo", "web", "render.go")); err != nil {
		t.Fatal(err)
	}
	if err := c.Run(context.Background(), []string{"new", "demo", "--no-interactive"}); err == nil {
		t.Fatal("overwrote project")
	}
}
func TestInteractive(t *testing.T) {
	c, _ := testCLI(t)
	c.In = strings.NewReader("demo\nfullstack\nsqlite\nvue\ntoken\nyes\nexample.com/demo\n")
	if err := c.Run(context.Background(), []string{"new", "--interactive"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"frontend/src/App.vue", "internal/auth/auth.go", "internal/database/database.go", "Dockerfile"} {
		if _, err := os.Stat(filepath.Join(c.Dir, "demo", name)); err != nil {
			t.Fatal(err)
		}
	}
}
func TestInstallerEOF(t *testing.T) {
	c, _ := testCLI(t)
	if err := c.Run(context.Background(), []string{"new", "demo", "--interactive"}); err == nil {
		t.Fatal("EOF silently accepted")
	}
	if _, err := os.Stat(filepath.Join(c.Dir, "demo")); !os.IsNotExist(err) {
		t.Fatal("partial project created")
	}
}
func TestInvalidCommands(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"new"}, {"new", "demo", "--template", "wrong"}, {"new", "demo", "--interactive", "--yes"}, {"new", "demo", "--framework-dir", "x", "--framework-version", "v0.2.0"}, {"serve", "unexpected"}, {"routes"}} {
		c, _ := testCLI(t)
		if err := c.Run(context.Background(), args); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
func TestHelp(t *testing.T) {
	for _, args := range [][]string{nil, {"help"}, {"--version"}, {"new", "--help"}, {"serve", "--help"}} {
		c, _ := testCLI(t)
		if err := c.Run(context.Background(), args); err != nil {
			t.Fatal(err)
		}
	}
}
func TestProjectCommands(t *testing.T) {
	for _, name := range []string{"serve", "routes"} {
		t.Run(name, func(t *testing.T) {
			c, out := testCLI(t)
			root, err := scaffold.Create(c.Dir, scaffold.Options{Name: "demo"})
			if err != nil {
				t.Fatal(err)
			}
			var calls [][]string
			c.run = func(ctx context.Context, dir string, env []string, writer io.Writer, args ...string) error {
				if dir != root {
					t.Errorf("dir=%s", dir)
				}
				calls = append(calls, append([]string(nil), args...))
				if args[len(args)-1] == "--routes" {
					fmt.Fprintln(writer, "GET\t/health")
				}
				return nil
			}
			args := []string{name, "--dir", "demo"}
			if name == "serve" {
				args = append(args, "--addr", ":9000")
			}
			if err = c.Run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if len(calls) != 2 || calls[0][0] != "go" || calls[0][1] != "build" {
				t.Fatal(calls)
			}
			if name == "serve" && calls[1][2] != ":9000" {
				t.Fatal(calls)
			}
			if name == "routes" && !strings.Contains(out.String(), "GET\t/health") {
				t.Fatal(out.String())
			}
		})
	}
}
func TestDoctor(t *testing.T) {
	c, out := testCLI(t)
	root, err := scaffold.Create(c.Dir, scaffold.Options{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	c.run = func(ctx context.Context, dir string, env []string, w io.Writer, args ...string) error {
		if args[0] == "go" && args[1] == "version" {
			fmt.Fprint(w, "go version go1.27.0 darwin/arm64")
		}
		return nil
	}
	if err = c.Run(context.Background(), []string{"doctor", "--dir", root}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Project is ready") {
		t.Fatal(out.String())
	}
	if err = os.Remove(filepath.Join(root, ".env")); err != nil {
		t.Fatal(err)
	}
	if err = c.Run(context.Background(), []string{"doctor", "--dir", root}); err == nil {
		t.Fatal("missing env accepted")
	}
}
func TestVersionRequirements(t *testing.T) {
	for value, want := range map[string]bool{"go version go1.27.0 darwin/arm64": true, "go version go1.26.3": false, "invalid": false} {
		if goVersionOK(value) != want {
			t.Fatal(value)
		}
	}
	for value, want := range map[string]bool{"v20.19.0": true, "v20.18.0": false, "v22.12.0": true, "v22.11.0": false, "v24.0.0": true, "v21.0.0": false, "invalid": false} {
		if nodeVersionOK(value) != want {
			t.Fatal(value)
		}
	}
}

func TestMigrationGenerator(t *testing.T) {
	c, out := testCLI(t)
	target, err := scaffold.Create(c.Dir, scaffold.Options{Name: "demo", Database: "sqlite"})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Run(context.Background(), []string{"make:migration", "create_users_table", "--dir", target}); err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(target, "database/migrations/*_create_users_table.go"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("paths=%v err=%v", paths, err)
	}
	body, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"type CreateUsersTable struct", "func (CreateUsersTable) Up", "MustRegister", "s.Create", "s.DropIfExists"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %s", want)
		}
	}
	if err = c.Run(context.Background(), []string{"make:migration", "create_users_table", "--dir", target}); err == nil {
		t.Fatal("duplicate migration generated")
	}
	if !strings.Contains(out.String(), "Created") {
		t.Fatal(out.String())
	}
}

func TestFrameworkRoot(t *testing.T) {
	gomod := []byte("module github.com/dayanchm/golar\n\ngo 1.27.0\n")
	checkout := filepath.Join(t.TempDir(), "golar")
	cached := filepath.Join(t.TempDir(), "pkg", "mod", "github.com", "dayanchm", "golar@v0.5.0")
	other := filepath.Join(t.TempDir(), "other")
	for dir, mod := range map[string][]byte{checkout: gomod, cached: gomod, other: []byte("module example.com/other\n")} {
		if err := os.MkdirAll(filepath.Join(dir, "internal", "cli"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), mod, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for dir, want := range map[string]string{checkout: checkout, cached: "", other: ""} {
		if got := frameworkRoot(filepath.Join(dir, "internal", "cli", "cli.go")); got != want {
			t.Errorf("frameworkRoot(%s) = %q, want %q", dir, got, want)
		}
	}
}
