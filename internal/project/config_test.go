package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigFormats(t *testing.T) {
	for _, body := range []string{`{"version":1,"name":"demo","module":"example.com/demo","template":"api"}`, "version: 1\nname: demo\nmodule: example.com/demo\ntemplate: api\ndocker: false\n"} {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "golar.yaml"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
		config, err := Load(root)
		if err != nil || config.Name != "demo" || config.Address != ":8080" {
			t.Fatalf("config=%+v err=%v", config, err)
		}
	}
}
func TestInvalidConfig(t *testing.T) {
	for _, body := range []string{`{"version":2,"name":"x","module":"x"}`, `{"version":1}`, `{"version":1,"name":"x","module":"x","unknown":true}`, "version: 1\nname: x\nname: y\nmodule: x", "invalid"} {
		root := t.TempDir()
		_ = os.WriteFile(filepath.Join(root, "golar.yaml"), []byte(body), 0644)
		if _, err := Load(root); err == nil {
			t.Errorf("accepted %s", body)
		}
	}
}
func TestEnv(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, ".env"), []byte("# comment\nAPP_ADDR=:9000\nTOKEN=\"hello world\"\nexport OTHER='quoted'\n"), 0600)
	env, err := Env(root, []string{"APP_ADDR=:8080"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	for _, want := range []string{"APP_ADDR=:8080", "TOKEN=hello world", "OTHER=quoted"} {
		if !strings.Contains(joined, want) {
			t.Fatal(joined)
		}
	}
}
func TestBadEnv(t *testing.T) {
	for _, body := range []string{"bad line", "BAD-KEY=x", `TOKEN="bad`, "TOKEN='bad"} {
		root := t.TempDir()
		_ = os.WriteFile(filepath.Join(root, ".env"), []byte(body), 0600)
		if _, err := Env(root, nil); err == nil {
			t.Errorf("accepted %q", body)
		}
	}
}
