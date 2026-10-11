package cli

import (
	"context"
	"github.com/dayanchm/golar/internal/scaffold"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestMigrationCLIIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("generated migration command compilation")
	}
	c, _ := testCLI(t)
	target, err := scaffold.Create(c.Dir, scaffold.Options{Name: "demo", Module: "example.com/demo", Database: "sqlite", FrameworkDir: localFramework()})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.Run(context.Background(), []string{"make:migration", "create_users_table", "--dir", target}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "mod", "tidy")
	command.Dir = target
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("tidy: %v %s", err, output)
	}
	t.Setenv("DATABASE_DSN", filepath.Join(target, "storage", "migration.db"))
	for _, command := range []string{"migrate", "migrate:status", "migrate:rollback", "migrate", "migrate:refresh", "migrate:reset"} {
		if err = c.Run(ctx, []string{command, "--dir", target}); err != nil {
			t.Fatalf("%s: %v", command, err)
		}
	}
	t.Setenv("DATABASE_DSN", filepath.Join(target, "storage", "pretend.db"))
	if err = c.Run(ctx, []string{"migrate", "--pretend", "--dir", target}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(target, "storage", "pretend.db")); !os.IsNotExist(err) {
		t.Fatal("pretend created database")
	}
}
