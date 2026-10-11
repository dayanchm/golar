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
	for _, kind := range []string{"api", "web", "fullstack"} {
		t.Run(kind, func(t *testing.T) {
			target, err := Create(t.TempDir(), Options{Name: "demo", Module: "example.com/demo", Template: kind, Auth: "token", FrameworkDir: framework})
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
