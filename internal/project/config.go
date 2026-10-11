// Package project reads the small, flat golar.yaml project manifest.
package project

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Version  int    `json:"version"`
	Name     string `json:"name"`
	Module   string `json:"module"`
	Template string `json:"template"`
	Database string `json:"database"`
	Frontend string `json:"frontend"`
	Auth     string `json:"auth"`
	Docker   bool   `json:"docker"`
	Address  string `json:"address"`
}

// Marshal writes the supported flat YAML manifest with quoted string values.
func (c Config) Marshal() ([]byte, error) {
	quote := func(value string) string { body, _ := json.Marshal(value); return string(body) }
	return []byte(fmt.Sprintf("version: %d\nname: %s\nmodule: %s\ntemplate: %s\ndatabase: %s\nfrontend: %s\nauth: %s\ndocker: %t\naddress: %s\n",
		c.Version, quote(c.Name), quote(c.Module), quote(c.Template), quote(c.Database), quote(c.Frontend), quote(c.Auth), c.Docker, quote(c.Address))), nil
}

func Load(root string) (Config, error) {
	var c Config
	data, err := os.ReadFile(filepath.Join(root, "golar.yaml"))
	if err != nil {
		return c, fmt.Errorf("read golar.yaml (run in a Golar project): %w", err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(data)), "{") {
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&c); err != nil {
			return c, fmt.Errorf("invalid golar.yaml: %w", err)
		}
		if err = dec.Decode(new(any)); err != io.EOF {
			return c, fmt.Errorf("golar.yaml must contain exactly one document")
		}
	} else {
		values := make(map[string]json.RawMessage)
		for i, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(line, ":")
			if !ok {
				return c, fmt.Errorf("golar.yaml line %d: expected key: value", i+1)
			}
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if _, exists := values[key]; exists {
				return c, fmt.Errorf("duplicate config key %q", key)
			}
			if key == "version" || key == "docker" {
				values[key] = json.RawMessage(value)
			} else {
				if strings.HasPrefix(value, "\"") {
					decoded, e := strconv.Unquote(value)
					if e != nil {
						return c, e
					}
					value = decoded
				} else if strings.HasPrefix(value, "'") {
					if len(value) < 2 || !strings.HasSuffix(value, "'") {
						return c, fmt.Errorf("invalid quoted config value")
					}
					value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
				}
				encoded, _ := json.Marshal(value)
				values[key] = encoded
			}
		}
		data, err = json.Marshal(values)
		if err != nil {
			return c, err
		}
		dec := json.NewDecoder(strings.NewReader(string(data)))
		dec.DisallowUnknownFields()
		if err = dec.Decode(&c); err != nil {
			return c, err
		}
	}
	if c.Version != 1 {
		return c, fmt.Errorf("unsupported project config version %d", c.Version)
	}
	if c.Name == "" || c.Module == "" {
		return c, fmt.Errorf("project name and module are required")
	}
	if c.Address == "" {
		c.Address = ":8080"
	}
	return c, nil
}
