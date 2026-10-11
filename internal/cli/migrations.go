package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/dayanchm/golar/internal/project"
	"go/format"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

func normalizeMigrationName(value string) (string, error) {
	var out strings.Builder
	for i, r := range value {
		if r > 127 {
			return "", fmt.Errorf("migration name must use ASCII letters and digits")
		}
		if unicode.IsUpper(r) {
			if i > 0 && out.Len() > 0 {
				out.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		if r == '-' || r == ' ' {
			r = '_'
		}
		out.WriteRune(r)
	}
	name := strings.Trim(out.String(), "_")
	valid := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	if !valid.MatchString(name) || len(name) > 100 {
		return "", fmt.Errorf("invalid migration name %q", value)
	}
	return name, nil
}
func migrationStruct(name string) string {
	parts := strings.Split(name, "_")
	var result strings.Builder
	for _, part := range parts {
		if part != "" {
			result.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return result.String()
}
func (c *CLI) makeMigration(args []string) error {
	flags := flag.NewFlagSet("make:migration", flag.ContinueOnError)
	flags.SetOutput(c.Err)
	root := flags.String("dir", c.Dir, "project directory")
	name := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name = args[0]
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		if name != "" || flags.NArg() != 1 {
			return fmt.Errorf("expected one migration name")
		}
		name = flags.Arg(0)
	}
	name, err := normalizeMigrationName(name)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(*root) {
		*root = filepath.Join(c.Dir, *root)
	}
	if _, err = project.Load(*root); err != nil {
		return err
	}
	dir := filepath.Join(*root, "database", "migrations")
	for _, path := range []string{filepath.Join(*root, "database"), dir} {
		if info, e := os.Lstat(path); e == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("migration: refusing symlink %s", path)
		} else if e != nil && !os.IsNotExist(e) {
			return e
		}
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	lockPath := filepath.Join(dir, ".golar-generator.lock")
	lock, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return fmt.Errorf("migration: generator lock unavailable (remove a stale lock only after confirming no generator is running): %w", err)
	}
	defer func() { _ = lock.Close(); _ = os.Remove(lockPath) }()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), "_"+name+".go") {
			return fmt.Errorf("migration: duplicate migration name %q", name)
		}
	}
	stamp := time.Now().UTC().Format("20060102_150405")
	migrationName := stamp + "_" + name
	structName := migrationStruct(name)
	up, down := "return nil", "return nil"
	if strings.HasPrefix(name, "create_") && strings.HasSuffix(name, "_table") {
		table := strings.TrimSuffix(strings.TrimPrefix(name, "create_"), "_table")
		if table != "" {
			up = fmt.Sprintf("return s.Create(%s,func(table *schema.Blueprint){table.ID();table.String(\"name\",255);table.String(\"email\",255).Unique();table.Timestamps()})", strconv.Quote(table))
			down = "return s.DropIfExists(" + strconv.Quote(table) + ")"
		}
	}
	source := fmt.Sprintf("package migrations\nimport (\"github.com/dayanchm/golar/database/migrations\";\"github.com/dayanchm/golar/database/schema\")\ntype %s struct{}\nfunc (%s)Up(s *schema.Schema)error{%s}\nfunc (%s)Down(s *schema.Schema)error{%s}\nfunc init(){migrations.MustRegister(%s,%s{})}\n", structName, structName, up, structName, down, strconv.Quote(migrationName), structName)
	body, err := format.Source([]byte(source))
	if err != nil {
		return err
	}
	path := filepath.Join(dir, migrationName+".go")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = file.Write(body)
	err = errors.Join(err, file.Close())
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	fmt.Fprintln(c.Out, "Created", path, "(UTC timestamp)")
	return nil
}

func (c *CLI) migrationCommand(ctx context.Context, command string, args []string) error {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(c.Err)
	root := flags.String("dir", c.Dir, "project directory")
	pretend := flags.Bool("pretend", false, "print schema SQL without opening a database (all registered migrations)")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected migration arguments")
	}
	if *pretend && command != "migrate" {
		return fmt.Errorf("--pretend is only supported by migrate")
	}
	if !filepath.IsAbs(*root) {
		*root = filepath.Join(c.Dir, *root)
	}
	config, err := project.Load(*root)
	if err != nil {
		return err
	}
	if config.Database == "none" || config.Database == "" {
		return fmt.Errorf("migration: configure database in golar.yaml first")
	}
	env, err := project.Env(*root, os.Environ())
	if err != nil {
		return err
	}
	hasDriver := false
	for _, value := range env {
		if strings.HasPrefix(value, "DB_DRIVER=") {
			hasDriver = true
		}
	}
	if !hasDriver {
		env = append(env, "DB_DRIVER="+config.Database)
	}
	dialect := config.Database
	for _, value := range env {
		if driver, ok := strings.CutPrefix(value, "DB_DRIVER="); ok && driver != "" {
			dialect = driver
		}
	}
	switch dialect {
	case "sqlite", "postgres", "mysql":
	default:
		return fmt.Errorf("migration: unsupported driver %q", dialect)
	}
	// The runner lives in the project, so its imports resolve project migrations.
	temporary, err := os.MkdirTemp(*root, ".golar-migrate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	source := fmt.Sprintf(`package main
import("context";"log";"os";"os/signal";"syscall";"fmt";
"github.com/dayanchm/golar/database";
"github.com/dayanchm/golar/database/migrations";
_ %s)
func main(){ctx,stop:=signal.NotifyContext(context.Background(),os.Interrupt,syscall.SIGTERM);defer stop()
if %t {if err:=migrations.Plan(migrations.DefaultRegistry.Entries(),%s,os.Stdout);err!=nil{log.Fatal(err)};return}
config,err:=database.ConfigFromEnv();if err!=nil{log.Fatal(err)};db,err:=database.Connect(config);if err!=nil{log.Fatal(err)};defer db.Close()
m:=migrations.New(db);m.Out=os.Stdout
switch %s {
case "migrate":err=m.Migrate(ctx)
case "migrate:rollback":err=m.Rollback(ctx)
case "migrate:reset":err=m.Reset(ctx)
case "migrate:refresh":err=m.Refresh(ctx)
case "migrate:status":var statuses []migrations.Status;statuses,err=m.Status(ctx);if err==nil{fmt.Println("Migration\tBatch\tStatus");for _,status:=range statuses{state:="Pending";if status.Ran{state="Ran"};fmt.Printf("%%s\t%%d\t%%s\n",status.Name,status.Batch,state)}}
};if err!=nil{log.Fatal(err)}}`, strconv.Quote(config.Module+"/database/migrations"), *pretend, strconv.Quote(dialect), strconv.Quote(command))
	body, err := format.Source([]byte(source))
	if err != nil {
		return err
	}
	path := filepath.Join(temporary, "main.go")
	if err = os.WriteFile(path, body, 0644); err != nil {
		return err
	}
	binary := filepath.Join(temporary, "runner")
	if err = c.run(ctx, *root, env, c.Err, "go", "build", "-o", binary, path); err != nil {
		return fmt.Errorf("migration command failed (run go mod tidy if dependencies are missing): %w", err)
	}
	return c.run(ctx, *root, env, c.Out, binary)
}
