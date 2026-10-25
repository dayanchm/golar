package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dayanchm/golar/internal/project"
)

func (c *CLI) doctor(ctx context.Context, root string, config project.Config, env []string) error {
	var failures []error
	check := func(label string, err error) {
		if err != nil {
			fmt.Fprintf(c.Out, "FAIL %s: %v\n", label, err)
			failures = append(failures, err)
		} else {
			fmt.Fprintf(c.Out, "OK   %s\n", label)
		}
	}
	for _, name := range []string{"go.mod", ".env", ".env.example", "golar.yaml", "cmd/server/main.go", "routes/routes.go", "tests"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err == nil && name != "tests" && !info.Mode().IsRegular() {
			err = fmt.Errorf("not a regular file")
		}
		check(name, err)
	}
	var output bytes.Buffer
	err := c.run(ctx, root, env, &output, "go", "version")
	if err == nil && !goVersionOK(output.String()) {
		err = fmt.Errorf("Go 1.27+ is required; found %s", strings.TrimSpace(output.String()))
	}
	check("Go toolchain", err)
	check("Go dependencies (run go mod tidy if needed)", c.run(ctx, root, env, c.Err, "go", "list", "-m", "all"))
	if config.Frontend != "none" {
		output.Reset()
		err = c.run(ctx, root, env, &output, "node", "--version")
		if err == nil && !nodeVersionOK(output.String()) {
			err = fmt.Errorf("Node 20.19+, 22.12+ or 24+ is required")
		}
		check("Node.js", err)
		check("npm", c.run(ctx, root, env, c.Err, "npm", "--version"))
		for _, name := range []string{"frontend/package.json", "frontend/node_modules"} {
			_, err = os.Stat(filepath.Join(root, name))
			check(name, err)
		}
		check("Frontend dependencies (run npm install if needed)", c.run(ctx, filepath.Join(root, "frontend"), env, c.Err, "npm", "ls", "--depth=0"))
	}
	if config.Docker {
		check("Docker", c.run(ctx, root, env, c.Err, "docker", "--version"))
		check("Docker daemon", c.run(ctx, root, env, c.Err, "docker", "info", "--format", "{{.ServerVersion}}"))
		check("Docker Compose", c.run(ctx, root, env, c.Err, "docker", "compose", "version"))
		check("Docker Compose configuration", c.run(ctx, root, env, c.Err, "docker", "compose", "config", "--quiet"))
		for _, name := range []string{"Dockerfile", "compose.yaml", "vendor"} {
			_, err = os.Stat(filepath.Join(root, name))
			check(name, err)
		}
	}
	// Projects generated before v0.4.0 use a shared AUTH_TOKEN and no database.
	if config.Auth == "token" && (config.Database == "none" || config.Database == "") {
		found := false
		for _, entry := range env {
			if token, ok := strings.CutPrefix(entry, "AUTH_TOKEN="); ok && token != "" {
				found = true
			}
		}
		var err error
		if !found {
			err = errors.New("set AUTH_TOKEN in .env or the environment")
		}
		check("Auth token configured", err)
	}
	if len(failures) > 0 {
		return fmt.Errorf("doctor found %d issue(s)", len(failures))
	}
	fmt.Fprintln(c.Out, "Project is ready.")
	return nil
}

var versionRE = regexp.MustCompile(`(?:go|v)(\d+)\.(\d+)`)

func versionParts(value string) (int, int, bool) {
	match := versionRE.FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	a, _ := strconv.Atoi(match[1])
	b, _ := strconv.Atoi(match[2])
	return a, b, true
}
func goVersionOK(value string) bool {
	major, minor, ok := versionParts(value)
	return ok && (major > 1 || major == 1 && minor >= 27)
}
func nodeVersionOK(value string) bool {
	major, minor, ok := versionParts(value)
	return ok && (major == 20 && minor >= 19 || major == 22 && minor >= 12 || major >= 24)
}
