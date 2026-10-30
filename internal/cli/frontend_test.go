package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dayanchm/golar/internal/project"
	"github.com/dayanchm/golar/internal/scaffold"
)

type call struct {
	dir  string
	env  []string
	args []string
}

// recorder fakes c.run. block makes long-running commands wait for their
// context, like real servers; fail makes a command fail immediately.
type recorder struct {
	mu    sync.Mutex
	calls []call
	block func(name string) bool
	fail  map[string]error
	// started is closed after the given number of calls.
	started chan struct{}
	want    int
}

func (r *recorder) run(ctx context.Context, dir string, env []string, _ io.Writer, args ...string) error {
	r.mu.Lock()
	r.calls = append(r.calls, call{dir, env, args})
	if r.started != nil && len(r.calls) == r.want {
		close(r.started)
	}
	r.mu.Unlock()
	if err := r.fail[args[0]]; err != nil {
		return err
	}
	if r.block != nil && r.block(args[0]) {
		<-ctx.Done()
	}
	return nil
}

func (r *recorder) find(name string) (call, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, c := range r.calls {
		if c.args[0] == name || strings.HasSuffix(c.args[0], string(filepath.Separator)+name) {
			return c, true
		}
	}
	return call{}, false
}

func named(names ...string) func(string) bool {
	return func(arg string) bool {
		for _, name := range names {
			if arg == name || strings.HasSuffix(arg, string(filepath.Separator)+name) {
				return true
			}
		}
		return false
	}
}

func hasEnv(env []string, entry string) bool { return slices.Contains(env, entry) }

func frontendProject(t *testing.T, c *CLI, installed bool) string {
	t.Helper()
	root, err := scaffold.Create(c.Dir, scaffold.Options{Name: "demo", Template: "fullstack", Frontend: "react"})
	if err != nil {
		t.Fatal(err)
	}
	if installed {
		if err := os.Mkdir(filepath.Join(root, "frontend", "node_modules"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestServeStartsVite(t *testing.T) {
	c, out := testCLI(t)
	root := frontendProject(t, c, true)
	rec := &recorder{block: named("npm")}
	c.run = rec.run
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	// The fake server returns at once, as if it exited; serve then stops Vite.
	go func() { done <- c.Run(ctx, []string{"serve", "--dir", root, "--addr", ":9000", "--vite-port", "5199"}) }()
	err := <-done
	cancel()
	if err == nil || !strings.Contains(err.Error(), "server exited") {
		t.Fatalf("serve = %v", err)
	}
	vite, ok := rec.find("npm")
	if !ok || vite.dir != filepath.Join(root, "frontend") || strings.Join(vite.args, " ") != "npm run dev -- --port 5199 --strictPort" {
		t.Fatalf("vite call = %+v", vite)
	}
	if !hasEnv(vite.env, "GOLAR_BACKEND_URL=http://localhost:9000") || !hasEnv(vite.env, "VITE_DEV_SERVER_URL=http://localhost:5199") {
		t.Errorf("vite env lacks URLs: %v", vite.env)
	}
	server, ok := rec.find("server")
	if !ok || !hasEnv(server.env, "VITE_DEV_SERVER_URL=http://localhost:5199") || hasEnv(server.env, "GOLAR_BACKEND_URL=http://localhost:9000") {
		t.Errorf("server call = %+v", server)
	}
	if !strings.Contains(out.String(), "with Vite on http://localhost:5199") {
		t.Errorf("output = %q", out.String())
	}
}

func TestServeStopsBothOnInterrupt(t *testing.T) {
	c, _ := testCLI(t)
	root := frontendProject(t, c, true)
	// go build, then Vite and the server, which both run until stopped.
	rec := &recorder{block: named("npm", "server"), started: make(chan struct{}), want: 3}
	c.run = rec.run
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, []string{"serve", "--dir", root}) }()
	<-rec.started
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Ctrl+C returned %v", err)
	}
}

func TestServeViteFailures(t *testing.T) {
	c, _ := testCLI(t)
	root := frontendProject(t, c, false)
	rec := &recorder{}
	c.run = rec.run
	if err := c.Run(context.Background(), []string{"serve", "--dir", root}); err == nil || !strings.Contains(err.Error(), "npm install") || !strings.Contains(err.Error(), "--no-vite") {
		t.Fatalf("missing node_modules = %v", err)
	}
	if err := c.Run(context.Background(), []string{"serve", "--dir", root, "--no-vite"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := rec.find("npm"); ok {
		t.Error("--no-vite started npm")
	}

	os.Mkdir(filepath.Join(root, "frontend", "node_modules"), 0755)
	broken := &recorder{block: named("server"), fail: map[string]error{"npm": errors.New("exit status 1")}}
	c.run = broken.run
	if err := c.Run(context.Background(), []string{"serve", "--dir", root}); err == nil || !strings.Contains(err.Error(), "Vite dev server: exit status 1") {
		t.Fatalf("failing Vite = %v", err)
	}
	if err := c.Run(context.Background(), []string{"serve", "--dir", root, "--vite-port", "0"}); err == nil {
		t.Fatal("port 0 accepted")
	}
}

func TestAPIProjectNeedsNoNode(t *testing.T) {
	c, _ := testCLI(t)
	root, err := scaffold.Create(c.Dir, scaffold.Options{Name: "api"})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	c.run = rec.run
	for _, args := range [][]string{{"serve", "--dir", root}, {"build", "--dir", root}} {
		if err := c.Run(context.Background(), args); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
	}
	if _, ok := rec.find("npm"); ok {
		t.Error("an API project ran npm")
	}
}

func TestBuild(t *testing.T) {
	c, out := testCLI(t)
	root := frontendProject(t, c, false)
	rec := &recorder{}
	c.run = rec.run
	if err := c.Run(context.Background(), []string{"build", "--dir", root}); err == nil || !strings.Contains(err.Error(), "npm install") {
		t.Fatalf("build without node_modules = %v", err)
	}
	os.Mkdir(filepath.Join(root, "frontend", "node_modules"), 0755)
	if err := c.Run(context.Background(), []string{"build", "--dir", root, "--output", "dist/app"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.calls) != 2 || strings.Join(rec.calls[0].args, " ") != "npm run build" || rec.calls[0].dir != filepath.Join(root, "frontend") {
		t.Fatalf("calls = %+v", rec.calls)
	}
	if got := strings.Join(rec.calls[1].args, " "); got != "go build -trimpath -o "+filepath.Join(root, "dist", "app")+" ./cmd/server" {
		t.Errorf("go build = %s", got)
	}
	if !strings.Contains(out.String(), "Built "+filepath.Join(root, "dist", "app")) {
		t.Errorf("output = %q", out.String())
	}
	rec.calls = nil
	if err := c.Run(context.Background(), []string{"build", "--dir", root, "--no-frontend"}); err != nil || len(rec.calls) != 1 || !strings.Contains(strings.Join(rec.calls[0].args, " "), filepath.Join(root, "bin", "server")) {
		t.Fatalf("--no-frontend = %v %+v", err, rec.calls)
	}
	rec.fail = map[string]error{"npm": errors.New("exit status 2")}
	if err := c.Run(context.Background(), []string{"build", "--dir", root}); err == nil || !strings.Contains(err.Error(), "build frontend") {
		t.Fatalf("failing npm = %v", err)
	}
	if err := c.Run(context.Background(), []string{"build", "extra"}); err == nil {
		t.Fatal("extra argument accepted")
	}
}

func TestMakeFrontend(t *testing.T) {
	c, out := testCLI(t)
	root, err := scaffold.Create(c.Dir, scaffold.Options{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(filepath.Join(root, "golar.yaml"))
	if err := c.Run(context.Background(), []string{"make:frontend", "angular", "--dir", root}); err == nil {
		t.Fatal("unknown framework accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "frontend")); !os.IsNotExist(err) {
		t.Fatal("failed command left a frontend directory")
	}
	if err := c.Run(context.Background(), []string{"make:frontend", "vue", "--dir", root}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"package.json", "vite.config.js", "index.html", "src/App.vue", "src/main.js"} {
		if _, err := os.Stat(filepath.Join(root, "frontend", filepath.FromSlash(name))); err != nil {
			t.Errorf("missing frontend/%s", name)
		}
	}
	config, err := project.Load(root)
	if err != nil || config.Frontend != "vue" || config.Template != "fullstack" {
		t.Fatalf("manifest = %+v, %v", config, err)
	}
	after, _ := os.Stat(filepath.Join(root, "golar.yaml"))
	if after.Mode() != before.Mode() {
		t.Errorf("golar.yaml mode %v, was %v", after.Mode(), before.Mode())
	}
	if !strings.Contains(out.String(), "template: fullstack") || !strings.Contains(out.String(), "SPA(vite.PathPrefix") {
		t.Errorf("output = %q", out.String())
	}
	if err := c.Run(context.Background(), []string{"make:frontend", "react", "--dir", root}); err == nil || !strings.Contains(err.Error(), "already has") {
		t.Fatalf("second frontend = %v", err)
	}

	web, err := scaffold.Create(c.Dir, scaffold.Options{Name: "site", Template: "web"})
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := c.Run(context.Background(), []string{"make:frontend", "react", "--dir", web}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `{{vite_react_refresh}}{{vite "src/main.jsx"}}`) || !strings.Contains(out.String(), "web.Assets.Files") {
		t.Errorf("web output = %q", out.String())
	}
	if config, _ := project.Load(web); config.Template != "web" {
		t.Errorf("web template changed to %s", config.Template)
	}

	taken, err := scaffold.Create(c.Dir, scaffold.Options{Name: "taken"})
	if err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(taken, "frontend"), 0755)
	if err := c.Run(context.Background(), []string{"make:frontend", "svelte", "--dir", taken}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing directory = %v", err)
	}
}

func TestBackendURL(t *testing.T) {
	for address, want := range map[string]string{":8080": "http://localhost:8080", "0.0.0.0:80": "http://localhost:80", "127.0.0.1:9000": "http://127.0.0.1:9000", "[::]:8080": "http://localhost:8080", "[::1]:8080": "http://[::1]:8080"} {
		if got, err := backendURL(address); err != nil || got != want {
			t.Errorf("backendURL(%q) = %q, %v", address, got, err)
		}
	}
	if _, err := backendURL("8080"); err == nil {
		t.Error("address without a port accepted")
	}
}
