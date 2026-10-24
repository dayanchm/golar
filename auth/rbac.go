package auth

import (
	"net/http"
	"slices"
	"strings"
	"sync"
)

// HasRoles is implemented by users that carry role names, for example loaded
// from a roles column or a belongsToMany relation.
type HasRoles interface {
	AuthRoles() []string
}

// RBAC maps roles to permissions. It is safe for concurrent use. Permissions
// may use wildcards: "*" grants everything and "posts.*" grants every
// "posts." permission.
type RBAC struct {
	mu       sync.RWMutex
	roles    map[string]map[string]bool
	inherits map[string][]string
}

func NewRBAC() *RBAC {
	return &RBAC{roles: map[string]map[string]bool{}, inherits: map[string][]string{}}
}

// Grant adds permissions to role, creating it if needed.
func (r *RBAC) Grant(role string, permissions ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.roles[role] == nil {
		r.roles[role] = map[string]bool{}
	}
	for _, p := range permissions {
		r.roles[role][p] = true
	}
}

// Revoke removes permissions from role.
func (r *RBAC) Revoke(role string, permissions ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, p := range permissions {
		delete(r.roles[role], p)
	}
}

// Inherit gives role every permission of parents, e.g. Inherit("admin", "editor").
func (r *RBAC) Inherit(role string, parents ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.roles[role] == nil {
		r.roles[role] = map[string]bool{}
	}
	r.inherits[role] = append(r.inherits[role], parents...)
}

// Permissions returns role's sorted permissions, including inherited ones.
func (r *RBAC) Permissions(role string) []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	set := map[string]bool{}
	r.collect(role, set, map[string]bool{})
	var out []string
	for p := range set {
		out = append(out, p)
	}
	slices.Sort(out)
	return out
}

func (r *RBAC) collect(role string, set, seen map[string]bool) {
	if seen[role] { // tolerate inheritance cycles
		return
	}
	seen[role] = true
	for p := range r.roles[role] {
		set[p] = true
	}
	for _, parent := range r.inherits[role] {
		r.collect(parent, set, seen)
	}
}

func roles(user Authenticatable) []string {
	if u, ok := user.(HasRoles); ok {
		return u.AuthRoles()
	}
	return nil
}

// HasRole reports whether user has any of the given roles.
func (r *RBAC) HasRole(user Authenticatable, wanted ...string) bool {
	for _, role := range roles(user) {
		if slices.Contains(wanted, role) {
			return true
		}
	}
	return false
}

// Can reports whether any of user's roles grants permission.
func (r *RBAC) Can(user Authenticatable, permission string) bool {
	if user == nil {
		return false
	}
	for _, role := range roles(user) {
		for _, granted := range r.Permissions(role) {
			if granted == "*" || granted == permission ||
				strings.HasSuffix(granted, ".*") && strings.HasPrefix(permission, strings.TrimSuffix(granted, "*")) {
				return true
			}
		}
	}
	return false
}

func (r *RBAC) require(allowed func(Authenticatable) bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			user := User(req.Context())
			if user == nil {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			if !allowed(user) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, req)
		})
	}
}

// RequireRole allows users with any of roles. Install it after Required.
func (r *RBAC) RequireRole(roles ...string) func(http.Handler) http.Handler {
	return r.require(func(u Authenticatable) bool { return r.HasRole(u, roles...) })
}

// RequirePermission allows users holding every listed permission. Install it after Required.
func (r *RBAC) RequirePermission(permissions ...string) func(http.Handler) http.Handler {
	return r.require(func(u Authenticatable) bool {
		for _, p := range permissions {
			if !r.Can(u, p) {
				return false
			}
		}
		return true
	})
}
