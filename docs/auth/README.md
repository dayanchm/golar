# Authentication and authorization

The `auth` package and `http/session` cover login, sessions, API tokens,
authorization, password resets, email verification, rate limiting and CSRF.
All middleware has the standard `func(http.Handler) http.Handler` shape, so it
works with `app.Use`, `http.ServeMux` and any router.

## Users

User models implement `auth.Authenticatable`:

```go
type User struct {
    orm.Model
    Email           string
    Password        string     // hash, never plaintext
    EmailVerifiedAt *time.Time
}

func (u *User) AuthID() string       { return strconv.FormatInt(u.ID, 10) }
func (u *User) AuthPassword() string { return u.Password }
```

`auth.ORMProvider[User]{DB: db}` loads users by ID and by the `email` column
(configurable with `IdentifierColumn`, `PasswordColumn`, `VerifiedColumn`).
Any type implementing `auth.UserProvider` can replace it.

`auth.User(ctx)` returns the authenticated user, and `auth.UserAs[*User](ctx)`
returns it typed.

## Passwords

```go
hash, err := auth.HashPassword("correct horse")
ok := auth.VerifyPassword(hash, "correct horse")
```

New hashes use Argon2id in PHC format with OWASP's minimum parameters (19 MiB,
2 iterations, 1 thread). Tune them with `auth.Passwords{Argon2id: auth.Argon2id{…}}`
and assign it to `auth.DefaultHasher`. bcrypt hashes (`$2a$`, `$2b$`, `$2y$`, e.g.
from Laravel) still verify and are upgraded to Argon2id on the next login.
Malformed hashes, or hashes with parameters outside sane bounds, never verify.
Passwords over 4096 bytes are rejected; bcrypt rejects passwords over 72 bytes
rather than truncating them.

## Login

```go
login := &auth.Authenticator{
    Provider: provider,
    Limiter:  auth.NewLimiter(5, time.Minute), // per identifier
}
user, err := login.Attempt(ctx, email, password)
switch {
case errors.Is(err, auth.ErrTooManyAttempts): // 429; errors.As gives *auth.RateLimitError.RetryAfter
case errors.Is(err, auth.ErrInvalidCredentials): // 401
}
```

Unknown emails and wrong passwords return the same error after the same amount
of hashing work, so responses do not reveal which accounts exist.

## Sessions

```go
sessions := session.NewManager() // in-memory store, secure cookie defaults
guard := &auth.SessionGuard{Provider: provider}

app.Use(sessions.Middleware, auth.CSRF)

// in the login handler:
err = guard.Login(r, user)  // regenerates the session ID, rotates the CSRF token
// in the logout handler:
err = guard.Logout(r)
```

Session cookies are `HttpOnly`, `Secure` and `SameSite=Lax` by default. Set
`Insecure: true` only for local HTTP. Unknown session IDs from clients are never
adopted, which prevents session fixation. Changing a user's password ends their
existing sessions. Change the session before writing the response body.

`session.MemoryStore` loses sessions on restart and is not shared between
instances. Implement `session.Store` for Redis or a database in multi-instance
deployments.

## API tokens

```go
tokens := &auth.TokenGuard{Provider: provider, Store: &auth.SQLTokenStore{DB: db}}
plaintext, token, err := tokens.Issue(ctx, user, "cli", []string{"posts:*"}, 30*24*time.Hour)
// show plaintext once; only its SHA-256 hash is stored
err = tokens.Revoke(ctx, plaintext)
```

Clients send `Authorization: Bearer golar_…`. Abilities support `*` and
`resource:*` wildcards. `auth.TokenFrom(ctx)` returns the token of the current request.

Create the token and password-reset tables in a migration with
`auth.CreateTables(schema)`.

## Protecting routes

```go
auth.Required(guard, tokens)    // 401 unless a guard authenticates; tries guards in order
auth.Optional(guard, tokens)    // sets the user when present
auth.RequireAbility("posts:write") // token abilities; after Required
auth.RequireVerified            // 403 for unverified emails; after Required
```

## Gates and policies

```go
gate := auth.NewGate()
gate.Define("view-dashboard", func(ctx context.Context, u auth.Authenticatable, _ ...any) bool {
    return u.(*User).IsAdmin
})
auth.Policy(gate, "update", func(ctx context.Context, u auth.Authenticatable, p *Post) bool {
    return u.AuthID() == strconv.FormatInt(p.UserID, 10)
})
gate.Before(func(ctx context.Context, u auth.Authenticatable, ability string) (allow, decided bool) {
    return true, u.(*User).IsSuperAdmin
})

if err := gate.Authorize(ctx, auth.User(ctx), "update", post); err != nil { … } // ErrForbidden
mux.Handle("/dashboard", auth.Required(guard)(gate.Can("view-dashboard")(handler)))
```

Checks run in order: `Before` hooks, the policy for the first argument's type,
the defined ability, then RBAC permissions. Anything else, including every
check for an anonymous user, is denied.

## Roles and permissions

```go
rbac := auth.NewRBAC()
rbac.Grant("editor", "posts.create", "posts.update")
rbac.Grant("admin", "*")
rbac.Inherit("admin", "editor")
gate.UseRBAC(rbac)

func (u *User) AuthRoles() []string { return strings.Split(u.Roles, ",") } // auth.HasRoles

rbac.RequireRole("admin")
rbac.RequirePermission("posts.update")
```

Role definitions live in memory; users' role assignments come from your model
through `HasRoles`, for example a column or an ORM relation.

## Password reset

```go
broker := &auth.PasswordBroker{
    Provider: provider,                       // must implement auth.PasswordUpdater
    Store:    &auth.SQLResetStore{DB: db},
    Send: func(ctx context.Context, u auth.Authenticatable, token string) error {
        return mailer.Send(…, "https://app.example/reset?token="+token)
    },
    OnReset: func(ctx context.Context, u auth.Authenticatable) error {
        return tokenStore.DeleteUser(ctx, u.AuthID()) // log out API clients
    },
}
err := broker.SendResetLink(ctx, email)              // nil for unknown emails
err = broker.Reset(ctx, email, token, newPassword)
```

Tokens are random 256-bit values stored as SHA-256 hashes, expire after `TTL`
(1 hour), are single use and are throttled to one link per `Throttle`
(1 minute). Passwords shorter than `MinLength` (8) are rejected. Show the same
message whether or not the email exists.

## Email verification

```go
verification := &auth.EmailVerification{
    Secret:   []byte(os.Getenv("APP_KEY")), // at least 32 random bytes
    Provider: provider,                     // must implement auth.EmailVerifiedMarker
    Send:     func(ctx context.Context, u auth.MustVerifyEmail, token string) error { … },
}
err := verification.SendVerification(ctx, user)
user, err := verification.Verify(ctx, token)
```

Users implement `auth.MustVerifyEmail` (`AuthEmail()`, `EmailVerified()`).
Tokens are HMAC-SHA256 signed, expire after `TTL` (24 hours) and are bound to
the current email address, so changing it invalidates outstanding links.

## Rate limiting

```go
limiter := auth.NewLimiter(10, time.Minute)
mux.Handle("POST /login", auth.RateLimit(limiter, nil)(loginHandler)) // per client IP
```

Over-limit requests get `429 Too Many Requests` with `Retry-After`. The default
key is the connection's IP; `X-Forwarded-For` is ignored because clients can
forge it. Behind a trusted proxy, pass a key function that reads the proxy's
header. Limits are kept per process.

## CSRF

`auth.CSRF` requires the session's token on every request except GET, HEAD,
OPTIONS and TRACE. Send it as the `X-CSRF-Token` header or the `_token` form
field:

```html
<input type="hidden" name="_token" value="{{ .CSRFToken }}">
```

where `CSRFToken` comes from `auth.CSRFToken(r)`. Requests without a session
token are rejected. APIs authenticated only by bearer tokens do not need CSRF
protection; mount them without it.
