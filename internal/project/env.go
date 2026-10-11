package project

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Env merges .env with the process environment. Process values take precedence.
func Env(root string, inherited []string) ([]string, error) {
	values := map[string]string{}
	file, err := os.Open(filepath.Join(root, ".env"))
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		keyPattern := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
		for i := 1; scanner.Scan(); i++ {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			key, value, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if !ok || !keyPattern.MatchString(key) {
				return nil, fmt.Errorf(".env line %d: expected KEY=value", i)
			}
			if strings.HasPrefix(value, "\"") {
				value, err = strconv.Unquote(value)
				if err != nil {
					return nil, fmt.Errorf(".env line %d: %w", i, err)
				}
			} else if strings.HasPrefix(value, "'") {
				if len(value) < 2 || !strings.HasSuffix(value, "'") {
					return nil, fmt.Errorf(".env line %d: invalid quoted value", i)
				}
				value = value[1 : len(value)-1]
			}
			values[key] = value
		}
		if err = scanner.Err(); err != nil {
			return nil, err
		}
	}
	for _, entry := range inherited {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result, nil
}
