package scaffold

import (
	"encoding/json"
	"regexp"
	"testing"
)

// Every version in npm/package.json is exact and used by a frontend, so a
// Dependabot update reaches generated projects.
func TestNPMVersions(t *testing.T) {
	used := map[string]bool{}
	for _, frontend := range []string{"react", "vue", "svelte"} {
		files := map[string][]byte{}
		if err := renderFrontend(files, data{Name: "demo", Template: "fullstack", Frontend: frontend}); err != nil {
			t.Fatal(err)
		}
		var pkg struct{ Dependencies, DevDependencies map[string]string }
		if err := json.Unmarshal(files["frontend/package.json"], &pkg); err != nil {
			t.Fatal(err)
		}
		for _, m := range []map[string]string{pkg.Dependencies, pkg.DevDependencies} {
			for name, version := range m {
				if version != npmVersions[name] {
					t.Errorf("%s: %s = %q, want %q", frontend, name, version, npmVersions[name])
				}
				used[name] = true
			}
		}
	}
	exact := regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	for name, version := range npmVersions {
		if !used[name] {
			t.Errorf("npm/package.json: %s is not used by any frontend", name)
		}
		if !exact.MatchString(version) {
			t.Errorf("npm/package.json: %s = %q, want an exact version", name, version)
		}
	}
}
