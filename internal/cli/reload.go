package cli

import (
	"context"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// reloadInterval is how often golar serve looks for changed files.
const reloadInterval = 500 * time.Millisecond

// skippedDirs are not watched: the frontend reloads through Vite, and the
// others hold dependencies, build output or runtime data.
var skippedDirs = map[string]bool{"frontend": true, "node_modules": true, "vendor": true, "storage": true, "bin": true, "tmp": true, "dist": true, "testdata": true}

// watched reports whether a change to the file should rebuild the server:
// Go code, module files, templates that may be embedded, and configuration.
func watched(name string) bool {
	switch filepath.Ext(name) {
	case ".go", ".html", ".tmpl", ".gohtml", ".yaml", ".json", ".sql":
		return !strings.HasSuffix(name, "_test.go")
	}
	switch name {
	case "go.mod", "go.sum", ".env":
		return true
	}
	return false
}

// snapshot fingerprints the watched files under root by path, size and
// modification time, so any edit, addition or removal changes it.
func snapshot(root string) uint64 {
	h := fnv.New64a()
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != root && (strings.HasPrefix(name, ".") || skippedDirs[name]) {
				return filepath.SkipDir
			}
			return nil
		}
		if !watched(name) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", path, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return h.Sum64()
}

// serveReload runs the server and rebuilds it when watched files change.
// The new build replaces the running server only when it compiles, so a
// compile error leaves the last good server running. A server that crashes
// is restarted after the next change; one that exits cleanly ends serve.
func (c *CLI) serveReload(ctx context.Context, root string, env []string, binary, address string) error {
	interval := c.reloadEvery
	if interval == 0 {
		interval = reloadInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	last := snapshot(root)
	builds := 0

	var exited chan error // nil while no server runs
	stop := func() {}
	start := func() {
		serverCtx, cancel := context.WithCancel(ctx)
		stop = cancel
		exited = make(chan error, 1)
		go func(done chan error, binary string) {
			done <- c.run(serverCtx, root, env, c.Out, binary, "--addr", address)
		}(exited, binary)
	}
	start()
	defer func() { stop() }()
	for {
		select {
		case <-ctx.Done():
			if exited == nil {
				return nil
			}
			stop()
			return <-exited
		case err := <-exited:
			stop()
			exited = nil
			if err == nil {
				return nil
			}
			fmt.Fprintf(c.Err, "golar: server exited: %v; waiting for changes\n", err)
		case <-ticker.C:
			current := snapshot(root)
			if current == last {
				continue
			}
			last = current
			builds++
			next := filepath.Join(filepath.Dir(binary), "server-"+strconv.Itoa(builds))
			if runtime.GOOS == "windows" {
				next += ".exe"
			}
			fmt.Fprintln(c.Out, "golar: change detected, rebuilding")
			if err := c.run(ctx, root, env, c.Err, "go", "build", "-o", next, "./cmd/server"); err != nil {
				if ctx.Err() != nil {
					continue
				}
				if exited != nil {
					fmt.Fprintf(c.Err, "golar: build failed, the previous server keeps running: %v\n", err)
				} else {
					fmt.Fprintf(c.Err, "golar: build failed: %v\n", err)
				}
				continue
			}
			if exited != nil {
				stop()
				<-exited // a server stopped for a restart reports nothing
			}
			os.Remove(binary)
			binary = next
			start()
			fmt.Fprintf(c.Out, "golar: restarted on %s\n", address)
		}
	}
}
