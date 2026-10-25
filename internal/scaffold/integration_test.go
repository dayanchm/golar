package scaffold

import (
	"context"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// This checks actual compilation and HTTP tests, not just rendered source text.
func TestGeneratedProjects(t *testing.T) {
	if testing.Short() {
		t.Skip("generated project compilation")
	}
	_, file, _, _ := runtime.Caller(0)
	framework := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	for _, o := range []Options{
		{Template: "api", Database: "sqlite", Auth: "token"},
		{Template: "web", Database: "sqlite", Auth: "session"},
		{Template: "fullstack"},
	} {
		t.Run(o.Template+"/"+o.Database+"/"+o.Auth, func(t *testing.T) {
			o.Name, o.Module, o.FrameworkDir = "demo", "example.com/demo", framework
			target, err := Create(t.TempDir(), o)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			for _, args := range [][]string{{"mod", "tidy"}, {"test", "./..."}, {"run", "./cmd/server", "--routes"}} {
				cmd := exec.CommandContext(ctx, "go", args...)
				cmd.Dir = target
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("go %v: %v\n%s", args, err, output)
				}
			}
		})
	}
}
