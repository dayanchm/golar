package router

import "testing"

func TestMatch(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		path    string
		want    bool
	}{
		{"static", "/users", "/users", true},
		{"parameter", "/users/{id}", "/users/123", true},
		{"different path", "/users", "/posts", false},
		{"missing segment", "/users/{id}", "/users", false},
		{"nested", "/users/{id}/posts", "/users/1/posts", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := match(tt.pattern, tt.path)

			if got != tt.want {
				t.Errorf("match(%q, %q) = %v, want %v",
					tt.pattern, tt.path, got, tt.want)
			}
		})
	}
}
