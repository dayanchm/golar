package auth

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"sync"
)

// Ability decides whether user may perform an action. user is never nil.
type Ability func(ctx context.Context, user Authenticatable, args ...any) bool

// Gate holds authorization rules. It is safe for concurrent use. Checks run in
// this order and the first decision wins:
//  1. Before hooks
//  2. the policy registered for the first argument's type
//  3. the ability defined with Define
//  4. the permission of the same name in the gate's RBAC
//
// Anything else is denied, and unauthenticated users are always denied.
type Gate struct {
	mu        sync.RWMutex
	abilities map[string]Ability
	policies  map[reflect.Type]map[string]func(context.Context, Authenticatable, any) bool
	before    []func(ctx context.Context, user Authenticatable, ability string) (allow, decided bool)
	rbac      *RBAC
}

func NewGate() *Gate {
	return &Gate{abilities: map[string]Ability{}, policies: map[reflect.Type]map[string]func(context.Context, Authenticatable, any) bool{}}
}

// Define registers ability.
func (g *Gate) Define(ability string, check Ability) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.abilities[ability] = check
}

// Before registers a hook that may decide any check first, e.g. letting admins
// through. Return decided=false to defer to the remaining rules.
func (g *Gate) Before(hook func(ctx context.Context, user Authenticatable, ability string) (allow, decided bool)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.before = append(g.before, hook)
}

// UseRBAC makes abilities fall back to permissions granted through r.
func (g *Gate) UseRBAC(r *RBAC) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.rbac = r
}

// Policy registers model-based rules for type M, used when M is the first
// argument of a check:
//
//	auth.Policy(gate, "update", func(ctx context.Context, u auth.Authenticatable, p *Post) bool {
//	    return u.AuthID() == strconv.FormatInt(p.UserID, 10)
//	})
//	gate.Allows(ctx, user, "update", post) // post is *Post
func Policy[M any](g *Gate, ability string, check func(ctx context.Context, user Authenticatable, model M) bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t := reflect.TypeFor[M]()
	if g.policies[t] == nil {
		g.policies[t] = map[string]func(context.Context, Authenticatable, any) bool{}
	}
	g.policies[t][ability] = func(ctx context.Context, user Authenticatable, model any) bool {
		m, ok := model.(M)
		return ok && check(ctx, user, m)
	}
}

// Allows reports whether user may perform ability.
func (g *Gate) Allows(ctx context.Context, user Authenticatable, ability string, args ...any) bool {
	if user == nil || reflect.ValueOf(user).Kind() == reflect.Pointer && reflect.ValueOf(user).IsNil() {
		return false
	}
	g.mu.RLock()
	before := g.before
	var policy func(context.Context, Authenticatable, any) bool
	if len(args) > 0 && args[0] != nil {
		policy = g.policies[reflect.TypeOf(args[0])][ability]
	}
	check, rbac := g.abilities[ability], g.rbac
	g.mu.RUnlock()
	for _, hook := range before {
		if allow, decided := hook(ctx, user, ability); decided {
			return allow
		}
	}
	switch {
	case policy != nil:
		return policy(ctx, user, args[0])
	case check != nil:
		return check(ctx, user, args...)
	case rbac != nil:
		return rbac.Can(user, ability)
	}
	return false
}

// Denies is the negation of Allows.
func (g *Gate) Denies(ctx context.Context, user Authenticatable, ability string, args ...any) bool {
	return !g.Allows(ctx, user, ability, args...)
}

// Authorize returns ErrUnauthenticated without a user, ErrForbidden when the
// check fails and nil otherwise.
func (g *Gate) Authorize(ctx context.Context, user Authenticatable, ability string, args ...any) error {
	if user == nil {
		return ErrUnauthenticated
	}
	if !g.Allows(ctx, user, ability, args...) {
		return fmt.Errorf("%w: %s", ErrForbidden, ability)
	}
	return nil
}

// Can rejects requests whose user (see Required) lacks ability, with 401 for
// anonymous requests and 403 otherwise.
func (g *Gate) Can(ability string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := User(r.Context())
			if user == nil {
				http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
				return
			}
			if !g.Allows(r.Context(), user, ability) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
