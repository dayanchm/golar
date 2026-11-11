package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dayanchm/golar/internal/scaffold"
)

// fakeProcesses fakes c.run for serve: builds succeed unless failBuild is
// set, and servers run until stopped unless crash is set.
type fakeProcesses struct {
	mu        sync.Mutex
	failBuild bool
	crash     bool
	builds    []string // -o targets
	running   map[string]bool
	events    chan string
}

func (f *fakeProcesses) run(ctx context.Context, _ string, _ []string, _ io.Writer, args ...string) error {
	f.mu.Lock()
	if args[0] == "go" {
		f.builds = append(f.builds, args[3])
		fail := f.failBuild && len(f.builds) > 1
		f.mu.Unlock()
		if fail {
			f.events <- "build failed"
			return errors.New("syntax error")
		}
		f.events <- "built " + filepath.Base(args[3])
		return nil
	}
	name := filepath.Base(args[0])
	f.running[name] = true
	crash := f.crash
	f.mu.Unlock()
	f.events <- "started " + name
	if crash {
		f.mu.Lock()
		f.running[name] = false
		f.mu.Unlock()
		return errors.New("panic: boom")
	}
	<-ctx.Done()
	f.mu.Lock()
	f.running[name] = false
	f.mu.Unlock()
	return nil
}

// syncBuffer collects output that serve writes while the test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (f *fakeProcesses) isRunning(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[name]
}

func expect(t *testing.T, events chan string, want string) {
	t.Helper()
	select {
	case got := <-events:
		if got != want {
			t.Fatalf("event = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %q", want)
	}
}

// touch rewrites a file with a modification time the snapshot will see as new.
func touch(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Duration(len(content)) * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
}

func startServe(t *testing.T, args ...string) (*fakeProcesses, string, *CLI, func() error) {
	t.Helper()
	c, _ := testCLI(t)
	root, err := scaffold.Create(c.Dir, scaffold.Options{Name: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeProcesses{running: map[string]bool{}, events: make(chan string, 16)}
	c.run = f.run
	c.Out, c.Err = &syncBuffer{}, &syncBuffer{}
	c.reloadEvery = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, append([]string{"serve", "--dir", root}, args...)) }()
	return f, root, c, func() error { cancel(); return <-done }
}

func TestServeReloadsOnGoChanges(t *testing.T) {
	f, root, c, stop := startServe(t)
	expect(t, f.events, "built server")
	expect(t, f.events, "started server")

	touch(t, filepath.Join(root, "routes", "extra.go"), "package routes\n")
	expect(t, f.events, "built server-1")
	expect(t, f.events, "started server-1")
	if f.isRunning("server") {
		t.Error("the old server is still running")
	}

	// A compile error keeps the running server.
	f.mu.Lock()
	f.failBuild = true
	f.mu.Unlock()
	touch(t, filepath.Join(root, "routes", "extra.go"), "package routes\nfunc broken(\n")
	expect(t, f.events, "build failed")
	if !f.isRunning("server-1") {
		t.Error("a failed build stopped the server")
	}
	if !strings.Contains(c.Err.(*syncBuffer).String(), "previous server keeps running") {
		t.Errorf("stderr = %q", c.Err)
	}

	// Frontend and test files do not trigger a build.
	f.mu.Lock()
	f.failBuild = false
	f.mu.Unlock()
	os.MkdirAll(filepath.Join(root, "frontend", "src"), 0o755)
	touch(t, filepath.Join(root, "frontend", "src", "main.go"), "x")
	touch(t, filepath.Join(root, "routes", "extra_test.go"), "package routes\n")
	time.Sleep(100 * time.Millisecond)
	select {
	case event := <-f.events:
		t.Fatalf("unwatched change caused %q", event)
	default:
	}

	if err := stop(); err != nil {
		t.Fatalf("Ctrl+C returned %v", err)
	}
	if f.isRunning("server-1") {
		t.Error("server still running after Ctrl+C")
	}
}

func TestServeRestartsCrashedServerAfterChange(t *testing.T) {
	f, root, c, stop := startServe(t)
	f.mu.Lock()
	f.crash = true
	f.mu.Unlock()
	expect(t, f.events, "built server")
	expect(t, f.events, "started server")
	f.mu.Lock()
	f.crash = false
	f.mu.Unlock()
	touch(t, filepath.Join(root, ".env"), "APP_ADDR=:8080\n")
	expect(t, f.events, "built server-1")
	expect(t, f.events, "started server-1")
	if !strings.Contains(c.Err.(*syncBuffer).String(), "waiting for changes") {
		t.Errorf("stderr = %q", c.Err)
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
}

func TestServeNoReload(t *testing.T) {
	f, root, c, stop := startServe(t, "--no-reload")
	expect(t, f.events, "built server")
	expect(t, f.events, "started server")
	touch(t, filepath.Join(root, "routes", "extra.go"), "package routes\n")
	time.Sleep(100 * time.Millisecond)
	select {
	case event := <-f.events:
		t.Fatalf("--no-reload caused %q", event)
	default:
	}
	if err := stop(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(c.Out.(*syncBuffer).String(), "reloading") {
		t.Errorf("output = %q", c.Out)
	}
}
