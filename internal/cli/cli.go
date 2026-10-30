// Package cli implements the golar command line interface.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/dayanchm/golar/internal/project"
	"github.com/dayanchm/golar/internal/scaffold"
)

const Version = "0.5.1"

type CLI struct {
	In       io.Reader
	Out, Err io.Writer
	Dir      string
	run      func(context.Context, string, []string, io.Writer, ...string) error
}

func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	c := &CLI{In: in, Out: out, Err: errOut, Dir: dir}
	c.run = c.command
	return c.Run(ctx, args)
}
func (c *CLI) Run(ctx context.Context, args []string) error {
	if c.run == nil {
		c.run = c.command
	}
	if len(args) == 0 {
		c.help()
		return nil
	}
	switch args[0] {
	case "help", "--help", "-h":
		c.help()
		return nil
	case "version", "--version":
		fmt.Fprintln(c.Out, "golar "+Version)
		return nil
	case "make:migration":
		return c.makeMigration(args[1:])
	case "make:request":
		return c.makeRequest(args[1:])
	case "make:frontend":
		return c.makeFrontend(args[1:])
	case "build":
		return c.buildProject(ctx, args[1:])
	case "migrate", "migrate:rollback", "migrate:reset", "migrate:refresh", "migrate:status":
		return c.migrationCommand(ctx, args[0], args[1:])
	case "new":
		return c.newProject(args[1:])
	case "serve", "routes", "doctor":
		return c.projectCommand(ctx, args[0], args[1:])
	default:
		return fmt.Errorf("unknown command %q; run golar help", args[0])
	}
}
func (c *CLI) help() {
	fmt.Fprintln(c.Out, `Golar CLI
  golar new <name> [--template api|web|fullstack] [--database none|sqlite|postgres|mysql]
                   [--frontend none|react|vue|svelte] [--auth none|token|session] [--docker]
                   [--module path] [--interactive | --no-interactive] [--dir parent]
                   [--framework-dir path | --framework-version version]
  golar serve [--dir project] [--addr :8080] [--no-vite] [--vite-port 5173]
  golar routes [--dir project]
  golar doctor [--dir project]
  golar make:migration <name> [--dir project]
  golar make:request <name> [--dir project]
  golar make:frontend react|vue|svelte [--dir project]
  golar build [--dir project] [--output bin/server] [--no-frontend]
  golar migrate [--pretend] [--dir project]
  golar migrate:rollback | migrate:reset | migrate:refresh | migrate:status
  golar version`)
}

func (c *CLI) newProject(args []string) error {
	flags := flag.NewFlagSet("new", flag.ContinueOnError)
	flags.SetOutput(c.Err)
	o := scaffold.Options{}
	parent := flags.String("dir", c.Dir, "parent directory")
	flags.StringVar(&o.Module, "module", "", "Go module path")
	flags.StringVar(&o.Template, "template", "api", "api, web or fullstack")
	flags.StringVar(&o.Database, "database", "none", "none, sqlite, postgres or mysql")
	flags.StringVar(&o.Frontend, "frontend", "", "none, react, vue or svelte")
	flags.StringVar(&o.Auth, "auth", "none", "none, token or session (token and session need a database)")
	flags.BoolVar(&o.Docker, "docker", false, "include Docker files")
	flags.StringVar(&o.FrameworkDir, "framework-dir", "", "local Golar source directory")
	flags.StringVar(&o.FrameworkVersion, "framework-version", "", "published Golar version")
	interactive := flags.Bool("interactive", false, "ask installer questions")
	noInteractive := flags.Bool("no-interactive", false, "use flags and defaults")
	yes := flags.Bool("yes", false, "use flags and defaults")
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		o.Name = args[0]
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		if o.Name != "" || flags.NArg() > 1 {
			return fmt.Errorf("expected one project name")
		}
		o.Name = flags.Arg(0)
	}
	if *interactive && (*noInteractive || *yes) {
		return fmt.Errorf("interactive and non-interactive options conflict")
	}
	terminal := false
	if file, ok := c.In.(*os.File); ok {
		info, err := file.Stat()
		terminal = err == nil && info.Mode()&os.ModeCharDevice != 0
	}
	if *interactive || terminal && !(*noInteractive || *yes) {
		scanner := bufio.NewScanner(c.In)
		ask := func(label, defaultValue string, choices ...string) (string, error) {
			fmt.Fprintf(c.Out, "%s [%s]", label, defaultValue)
			if len(choices) > 0 {
				fmt.Fprintf(c.Out, " (%s)", strings.Join(choices, "/"))
			}
			fmt.Fprint(c.Out, ": ")
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					return "", err
				}
				return "", fmt.Errorf("installer input ended; use --no-interactive for defaults")
			}
			value := strings.TrimSpace(scanner.Text())
			if value == "" {
				value = defaultValue
			}
			if len(choices) > 0 {
				for _, choice := range choices {
					if value == choice {
						return value, nil
					}
				}
				return "", fmt.Errorf("invalid selection %q for %s", value, label)
			}
			return value, nil
		}
		var err error
		if o.Name == "" {
			o.Name, err = ask("Project name", "my-app")
			if err != nil {
				return err
			}
		}
		o.Template, err = ask("Template", o.Template, "api", "web", "fullstack")
		if err != nil {
			return err
		}
		o.Database, err = ask("Database", o.Database, "none", "sqlite", "postgres", "mysql")
		if err != nil {
			return err
		}
		frontend := o.Frontend
		if frontend == "" {
			frontend = "none"
			if o.Template == "fullstack" {
				frontend = "react"
			}
		}
		if o.Template != "api" {
			o.Frontend, err = ask("Frontend", frontend, "none", "react", "vue", "svelte")
			if err != nil {
				return err
			}
		} else {
			o.Frontend = "none"
		}
		if o.Database != "none" {
			o.Auth, err = ask("Authentication", o.Auth, "none", "token", "session")
			if err != nil {
				return err
			}
		} else {
			o.Auth = "none"
		}
		docker := "no"
		if o.Docker {
			docker = "yes"
		}
		docker, err = ask("Docker", docker, "yes", "no")
		if err != nil {
			return err
		}
		o.Docker = docker == "yes"
		module := o.Module
		if module == "" {
			module = o.Name
		}
		o.Module, err = ask("Go module", module)
		if err != nil {
			return err
		}
	}
	if o.Name == "" {
		return fmt.Errorf("usage: golar new <name> (or use --interactive)")
	}
	if o.FrameworkDir != "" && o.FrameworkVersion != "" {
		return fmt.Errorf("choose framework-dir or framework-version, not both")
	}
	if o.FrameworkDir == "" && o.FrameworkVersion == "" {
		o.FrameworkDir = localFramework()
	}
	if !filepath.IsAbs(*parent) {
		*parent = filepath.Join(c.Dir, *parent)
	}
	target, err := scaffold.Create(*parent, o)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.Out, "Created %s\nNext: cd %s && go mod tidy && golar serve\n", target, o.Name)
	if o.FrameworkDir != "" {
		fmt.Fprintf(c.Out, "Using local framework: %s\n", o.FrameworkDir)
	}
	return nil
}

func localFramework() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	return frameworkRoot(file)
}

// frameworkRoot returns the Golar checkout containing this source file. A
// CLI installed with `go install ...@version` was compiled from the read-only
// module cache (a "golar@v0.5.0" directory); that is not a checkout, so
// projects require the published version instead.
func frameworkRoot(file string) string {
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	if strings.Contains(filepath.Base(root), "@") {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err == nil && strings.HasPrefix(string(data), "module github.com/dayanchm/golar\n") {
		return root
	}
	return ""
}

func (c *CLI) projectCommand(ctx context.Context, name string, args []string) error {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(c.Err)
	root := flags.String("dir", c.Dir, "project directory")
	var addr *string
	var noVite *bool
	var vitePort *int
	if name == "serve" {
		addr = flags.String("addr", "", "listen address override")
		noVite = flags.Bool("no-vite", false, "do not start the Vite dev server")
		vitePort = flags.Int("vite-port", 5173, "Vite dev server port")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if !filepath.IsAbs(*root) {
		*root = filepath.Join(c.Dir, *root)
	}
	config, err := project.Load(*root)
	if err != nil {
		return err
	}
	if _, err = scaffold.Normalize(scaffold.Options{Name: config.Name, Module: config.Module, Template: config.Template, Database: config.Database, Frontend: config.Frontend, Auth: config.Auth}); err != nil {
		return fmt.Errorf("invalid project config: %w", err)
	}
	env, err := project.Env(*root, os.Environ())
	if err != nil {
		return err
	}
	if name == "doctor" {
		return c.doctor(ctx, *root, config, env)
	}
	tmp, err := os.MkdirTemp("", "golar-run-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	binary := filepath.Join(tmp, "server")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if err = c.run(ctx, *root, env, c.Err, "go", "build", "-o", binary, "./cmd/server"); err != nil {
		return fmt.Errorf("build application (try go mod tidy): %w", err)
	}
	if name == "routes" {
		return c.run(ctx, *root, env, c.Out, binary, "--routes")
	}
	address := config.Address
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, "APP_ADDR="); ok && value != "" {
			address = value
		}
	}
	if *addr != "" {
		address = *addr
	}
	if config.Frontend != "none" && !*noVite {
		return c.serveWithVite(ctx, *root, env, binary, address, *vitePort)
	}
	fmt.Fprintf(c.Out, "Serving %s on %s (Ctrl+C to stop)\n", config.Name, address)
	return c.run(ctx, *root, env, c.Out, binary, "--addr", address)
}

func (c *CLI) command(ctx context.Context, dir string, env []string, out io.Writer, args ...string) error {
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Dir = dir
	command.Env = env
	command.Stdin = c.In
	command.Stdout = out
	command.Stderr = c.Err
	command.Cancel = func() error {
		if runtime.GOOS == "windows" {
			return command.Process.Kill()
		}
		return command.Process.Signal(syscall.SIGTERM)
	}
	command.WaitDelay = 15 * time.Second
	err := command.Run()
	if ctx.Err() != nil && command.ProcessState != nil && command.ProcessState.Success() {
		return nil
	}
	return err
}
