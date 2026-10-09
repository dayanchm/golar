package router

import "strings"

func match(pattern, path string) (map[string]string, bool) {
	patternParts := splitPath(pattern)
	pathParts := splitPath(path)

	if len(patternParts) != len(pathParts) {
		return nil, false
	}

	params := make(map[string]string)

	for i, part := range patternParts {
		if strings.HasPrefix(part, "{") &&
			strings.HasSuffix(part, "}") {

			name := strings.TrimSuffix(
				strings.TrimPrefix(part, "{"),
				"}",
			)

			if name == "" || pathParts[i] == "" {
				return nil, false
			}

			params[name] = pathParts[i]
			continue
		}

		if part != pathParts[i] {
			return nil, false
		}
	}

	return params, true
}

func splitPath(path string) []string {
	path = strings.Trim(path, "/")

	if path == "" {
		return []string{}
	}

	return strings.Split(path, "/")
}
