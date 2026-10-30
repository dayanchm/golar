package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/dayanchm/golar/internal/project"
	"github.com/dayanchm/golar/internal/scaffold"
)

// backendURL turns a listen address such as ":8080" into a URL a local
// browser or proxy can reach.
func backendURL(address string) (string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", address, err)
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port), nil
}

// frontendReady reports a missing frontend/node_modules with the fix.
func frontendReady(root string) error {
	if _, err := os.Stat(filepath.Join(root, "frontend", "package.json")); err != nil {
		return fmt.Errorf("frontend/package.json not found: %w", err)
	}
	if _, err := os.Stat(filepath.Join(root, "frontend", "node_modules")); err != nil {
		return errors.New("frontend dependencies are not installed: run npm install in frontend")
	}
	return nil
}

// serveWithVite runs the Go server and the Vite dev server together. The Go
// server gets VITE_DEV_SERVER_URL so pages load assets from Vite; Vite gets
// GOLAR_BACKEND_URL for its /api proxy. When either process exits, the other
// is stopped; Ctrl+C stops both.
func (c *CLI) serveWithVite(ctx context.Context, root string, env []string, binary, address string, vitePort int) error {
	if vitePort < 1 || vitePort > 65535 {
		return fmt.Errorf("invalid --vite-port %d", vitePort)
	}
	if err := frontendReady(root); err != nil {
		return fmt.Errorf("%w (or pass --no-vite)", err)
	}
	backend, err := backendURL(address)
	if err != nil {
		return err
	}
	viteURL := "http://localhost:" + strconv.Itoa(vitePort)
	goEnv := append(append([]string(nil), env...), "VITE_DEV_SERVER_URL="+viteURL)
	viteEnv := append(append([]string(nil), goEnv...), "GOLAR_BACKEND_URL="+backend)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		name string
		err  error
	}
	results := make(chan result, 2)
	go func() {
		err := c.run(runCtx, filepath.Join(root, "frontend"), viteEnv, c.Out, "npm", "run", "dev", "--", "--port", strconv.Itoa(vitePort), "--strictPort")
		results <- result{"Vite dev server", err}
	}()
	go func() {
		results <- result{"server", c.run(runCtx, root, goEnv, c.Out, binary, "--addr", address)}
	}()
	fmt.Fprintf(c.Out, "Serving on %s with Vite on %s (Ctrl+C to stop)\n", address, viteURL)
	first := <-results
	cancel()
	<-results
	if ctx.Err() != nil {
		return nil // stopped by the user
	}
	if first.err != nil {
		return fmt.Errorf("%s: %w", first.name, first.err)
	}
	return fmt.Errorf("%s exited; stopped the other process", first.name)
}

// buildProject builds the frontend with npm run build, when the project has
// one, then the server binary.
func (c *CLI) buildProject(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(c.Err)
	root := flags.String("dir", c.Dir, "project directory")
	output := flags.String("output", "", "server binary path (default bin/server)")
	noFrontend := flags.Bool("no-frontend", false, "skip npm run build")
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
	env, err := project.Env(*root, os.Environ())
	if err != nil {
		return err
	}
	if config.Frontend != "none" && !*noFrontend {
		if err := frontendReady(*root); err != nil {
			return fmt.Errorf("%w (or pass --no-frontend)", err)
		}
		fmt.Fprintln(c.Out, "Building frontend (npm run build)")
		if err := c.run(ctx, filepath.Join(*root, "frontend"), env, c.Out, "npm", "run", "build"); err != nil {
			return fmt.Errorf("build frontend: %w", err)
		}
	}
	binary := *output
	if binary == "" {
		binary = filepath.Join("bin", "server")
		if runtime.GOOS == "windows" {
			binary += ".exe"
		}
	}
	if !filepath.IsAbs(binary) {
		binary = filepath.Join(*root, binary)
	}
	fmt.Fprintln(c.Out, "Building server (go build)")
	if err := c.run(ctx, *root, env, c.Err, "go", "build", "-trimpath", "-o", binary, "./cmd/server"); err != nil {
		return fmt.Errorf("build server (try go mod tidy): %w", err)
	}
	fmt.Fprintln(c.Out, "Built", binary)
	if config.Frontend != "none" {
		fmt.Fprintln(c.Out, "Run it from the project directory, or ship frontend/dist next to it.")
	}
	return nil
}

// makeFrontend adds a React, Vue or Svelte frontend to a project that has none.
func (c *CLI) makeFrontend(args []string) error {
	flags := flag.NewFlagSet("make:frontend", flag.ContinueOnError)
	flags.SetOutput(c.Err)
	root := flags.String("dir", c.Dir, "project directory")
	framework := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		framework, args = args[0], args[1:]
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() > 0 {
		if framework != "" || flags.NArg() != 1 {
			return fmt.Errorf("expected one framework: react, vue or svelte")
		}
		framework = flags.Arg(0)
	}
	if !filepath.IsAbs(*root) {
		*root = filepath.Join(c.Dir, *root)
	}
	config, err := project.Load(*root)
	if err != nil {
		return err
	}
	if config.Frontend != "none" {
		return fmt.Errorf("project already has a %s frontend", config.Frontend)
	}
	if _, err := os.Lstat(filepath.Join(*root, "frontend")); err == nil {
		return fmt.Errorf("frontend directory already exists; move it before adding a frontend")
	} else if !os.IsNotExist(err) {
		return err
	}
	updated := config
	updated.Frontend = framework
	if updated.Template == "api" {
		updated.Template = "fullstack"
	}
	files, err := scaffold.FrontendFiles(scaffold.Options{Name: updated.Name, Module: updated.Module, Template: updated.Template, Database: updated.Database, Frontend: updated.Frontend, Auth: updated.Auth})
	if err != nil {
		return err
	}
	manifest, err := updated.Marshal()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	written := []string{}
	cleanup := func() {
		for i := len(written) - 1; i >= 0; i-- {
			_ = os.Remove(written[i])
		}
		_ = os.RemoveAll(filepath.Join(*root, "frontend"))
	}
	for _, name := range names {
		path := filepath.Join(*root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			cleanup()
			return err
		}
		if err := writeNew(path, files[name]); err != nil {
			cleanup()
			return err
		}
		written = append(written, path)
	}
	if err := replaceFile(filepath.Join(*root, "golar.yaml"), manifest); err != nil {
		cleanup()
		return err
	}
	fmt.Fprintf(c.Out, "Added a %s frontend in %s and set frontend: %s", framework, filepath.Join(*root, "frontend"), framework)
	if updated.Template != config.Template {
		fmt.Fprintf(c.Out, " and template: %s", updated.Template)
	}
	fmt.Fprintln(c.Out, " in golar.yaml.")
	if updated.Template == "web" {
		entry, refresh := "src/main.js", ""
		if framework == "react" {
			entry, refresh = "src/main.jsx", "{{vite_react_refresh}}"
		}
		fmt.Fprintf(c.Out, `Load it in your pages. In web/render.go:

    var Assets = vite.New(vite.Config{}) // import "github.com/dayanchm/golar/http/vite"
    var Templates = template.Must(template.New("").Funcs(Assets.FuncMap()).ParseFS(files, "templates/*.html"))

in internal/bootstrap/app.go, after the default middleware:

    app.Use(web.Assets.Files)

and in the <head> of a page, with <div id="app"></div> in its body:

    %s{{vite %q}}

Then run npm install in frontend and golar serve.
`, refresh, entry)
		return nil
	}
	fmt.Fprintln(c.Out, `Serve the build from internal/bootstrap/app.go, after the default middleware:

    import "github.com/dayanchm/golar/http/vite"

    app.Use(vite.New(vite.Config{}).SPA(vite.PathPrefix("/api", "/health")))

Then run npm install in frontend and golar serve.`)
	return nil
}

func writeNew(path string, body []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644)
	if err != nil {
		return err
	}
	_, err = file.Write(body)
	return errors.Join(err, file.Close())
}

// replaceFile writes body to a temporary file and renames it over path,
// keeping path's permissions.
func replaceFile(path string, body []byte) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".golar-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(body)
	err = errors.Join(err, tmp.Chmod(info.Mode().Perm()))
	if err = errors.Join(err, tmp.Close()); err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}
