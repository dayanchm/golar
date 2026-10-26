# Middleware

`github.com/dayanchm/golar/http/middleware` provides standard
`func(http.Handler) http.Handler` middleware. Each one is opt-in and works with
`app.Use`, the router, `http.ServeMux` or any other Go router.

```go
app.Use(
    middleware.RequestID(),
    middleware.Logger(),
    middleware.Recovery(),
    middleware.SecureHeaders(),
    middleware.CORS(middleware.CORSConfig{AllowOrigins: []string{"https://app.example.com"}}),
    middleware.BodyLimit(10 << 20),
    middleware.Timeout(30 * time.Second),
    middleware.Compress(),
)
```

Middleware runs in registration order, with the first outermost. Generated
projects use RequestID, Logger, Recovery, SecureHeaders, a 10 MiB BodyLimit and
Compress.

## Logger and Recovery

`Logger` logs method, path, status, bytes, duration and the request ID when
`RequestID` is used. `Recovery` turns panics into a logged stack trace and a
generic 500. Put Logger before Recovery so recovered panics are logged with
status 500. Both accept an optional `*log.Logger`.

## RequestID

```go
middleware.RequestID()
middleware.RequestID(middleware.RequestIDConfig{Header: "X-Trace-ID", Generate: newID})
id := middleware.RequestIDFrom(r.Context())
```

An incoming ID made of up to 128 letters, digits, `.`, `_`, `:` or `-` is
reused; anything else is replaced with 16 random bytes in hex. The ID is set on
the response and stored in the request context. Clients can choose their own
IDs, so use them for correlation, not for security decisions.

## CORS

```go
middleware.CORS(middleware.CORSConfig{
    AllowOrigins:     []string{"https://app.example.com"},
    AllowOriginFunc:  func(origin string) bool { return strings.HasSuffix(origin, ".preview.example.com") },
    AllowCredentials: true,
    ExposeHeaders:    []string{"X-Request-ID"},
    MaxAge:           10 * time.Minute,
})
```

Origins match exactly. Allowed origins get `Access-Control-Allow-Origin`
and, with credentials, `Access-Control-Allow-Credentials`. Preflight requests
(OPTIONS with `Access-Control-Request-Method`) are answered with 204 and do not
reach routes. Other origins get no CORS headers, so browsers block the response.

`AllowMethods` defaults to GET, HEAD, POST, PUT, PATCH and DELETE, and
`AllowHeaders` to Accept, Authorization, Content-Type, X-CSRF-Token and
X-Requested-With. `"*"` allows any origin but cannot be combined with
`AllowCredentials`; CORS panics on that configuration.

Cookie-authenticated APIs that accept other origins still need CSRF protection.

## SecureHeaders

`middleware.SecureHeaders()` uses `DefaultSecureHeaders`:

| Header | Value |
| --- | --- |
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains`, only over TLS or with `X-Forwarded-Proto: https` |
| `X-Content-Type-Options` | `nosniff` |
| `X-Frame-Options` | `DENY` |
| `Referrer-Policy` | `strict-origin-when-cross-origin` |

Pass a `SecureHeadersConfig` to choose your own values; empty fields are
omitted. `ContentSecurityPolicy` is off by default because a useful policy
depends on the application. Headers are set before the handler runs, so a
handler can override them for one response.

## BodyLimit

`middleware.BodyLimit(10 << 20)` answers requests that declare a larger
`Content-Length` with 413. Other bodies stop with `*http.MaxBytesError` when the
limit is reached; the default Golar error handler turns that error, including
from `c.Validate`, into 413.

## Timeout

`middleware.Timeout(30 * time.Second)` cancels the request context after the
duration. Handlers must pass `r.Context()` to database and HTTP calls and
return when it is done; Timeout cannot stop code that ignores it. If the
deadline passes before anything is written, the client gets 503. Responses that
have started are left alone.

## Compress

```go
middleware.Compress()
middleware.Compress(middleware.CompressConfig{Level: gzip.BestSpeed, MinSize: 512})
```

Responses are gzipped when the client sends `Accept-Encoding: gzip` and the body
reaches `MinSize` (1024 bytes by default). HEAD requests, 204, 206 and 304
responses, responses that already set `Content-Encoding`, `text/event-stream`
and already-compressed types (images other than SVG, video, audio, fonts,
archives, PDF and WebAssembly) are passed through. `Vary: Accept-Encoding` is
always set.

Flushing with `http.NewResponseController(w).Flush()` sends buffered output
immediately. A flush before `MinSize` bytes sends the response uncompressed.
