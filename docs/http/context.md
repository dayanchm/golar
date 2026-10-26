# Request context

Handlers receive `*golar.Context` (`github.com/dayanchm/golar/http/context`).
It keeps the raw `Request` and `Writer` and adds helpers for input and
responses. Return the errors these helpers produce; the default error handler
turns them into the right status.

## Input

```go
id := c.Param("id")                 // route parameter
q := c.Query("q")                   // "" when missing
sort := c.QueryDefault("sort", "name")
page, err := c.QueryInt("page", 1)  // *httpcontext.InputError when not an integer
name := c.Form("name")              // URL-encoded or multipart body field
ctx := c.Context()                  // request context, canceled on disconnect
```

`Form` returns `""` when the body cannot be parsed; call `c.ParseForm()` to get
the error. Query values are not included in `Form`.

### Binding

```go
type ProfileRequest struct {
    Name    string                `form:"name" json:"name" validate:"required"`
    Age     int                   `form:"age" json:"age"`
    Tags    []string              `form:"tag" json:"tags"`
    Born    time.Time             `form:"born" json:"born"`
    Address struct{ City string } `form:"address" json:"address"`
    Avatar  *multipart.FileHeader `form:"avatar" json:"-"`
}

var req ProfileRequest
if err := c.Bind(&req); err != nil { // or c.Validate(&req) to bind and validate
    return err
}
```

`Bind` chooses a decoder by `Content-Type`:

| Content-Type | Decoder |
| --- | --- |
| none, `application/json`, `*+json` | `encoding/json`; data after the value is rejected |
| `application/x-www-form-urlencoded`, `multipart/form-data` | form fields into a struct |
| anything else | `*BindError` wrapping `ErrUnsupportedMediaType` (415) |

Form fields match the `form` tag, then the `json` tag, then the Go field name.
Supported types are strings, booleans (`true`, `1`, `on`, `yes` and their
opposites), integers, floats, pointers and slices of these,
`encoding.TextUnmarshaler` types such as `time.Time`, `*multipart.FileHeader`
and `[]*multipart.FileHeader`. Nested structs use dotted keys such as
`address.City`; embedded structs share the outer keys. Missing fields, and
empty values for non-string fields, leave the field unchanged so
`validate:"required"` reports them. Decoding errors are returned as
`*BindError` (400).

`BindJSON` still decodes JSON without looking at `Content-Type`.

### Body limits

Bodies are read through `http.MaxBytesReader`. Larger bodies fail with
`*http.MaxBytesError`, which the default error handler renders as 413.

| `httpcontext.Config` field | Default | Applies to |
| --- | --- | --- |
| `MaxBodySize` | 4 MiB | JSON, URL-encoded and other bodies |
| `MaxMultipartSize` | 32 MiB | the whole multipart body, files included |
| `MaxMultipartMemory` | 8 MiB | multipart data kept in memory; the rest goes to temp files |

A negative size disables a limit. Set them for the application:

```go
app.SetContextConfig(httpcontext.Config{
    MaxBodySize:      1 << 20,
    MaxMultipartSize: 100 << 20,
})
```

The `BodyLimit` middleware still applies on top of these limits.

### Files

```go
file, err := c.File("avatar") // first file for the field
if err != nil {
    return err // 400 when missing or not multipart, 413 when too large
}
if file.Size > 2<<20 {
    return &golar.HTTPError{Code: 422, Message: "Avatar must be at most 2 MiB"}
}
err = c.SaveFile(file, filepath.Join("storage", "avatars", rand.Text()+".png"))
```

`SaveFile` creates parent directories and removes a partly written file on
error. `file.Filename` and the part's `Content-Type` come from the client:
never use them as a path, and check the content if the type matters.

## Responses

```go
c.JSON(http.StatusOK, user)
c.String(http.StatusOK, "pong")
c.HTML(http.StatusOK, "<h1>Hello</h1>")          // trusted HTML only
c.Render(http.StatusOK, web.Templates, "index.html", data)
c.Blob(http.StatusOK, "text/csv", data)
c.NoContent(http.StatusNoContent)
c.Redirect(http.StatusSeeOther, "/login")
c.Download("storage/reports/2026.csv", "report.csv")
```

`Render` accepts any `html/template` or `text/template` template. It renders
into a buffer first, so a template error leaves the response unwritten and the
error handler can still send a 500. Generated Web and Full Stack projects
expose their templates as `web.Templates`.

`Redirect` only accepts 3xx statuses. Do not redirect to a URL taken from the
request, such as `?next=`, without checking it.

`Download` sends a file as an attachment and supports `Range`, `HEAD` and
conditional requests. Non-ASCII file names are encoded for browsers. It returns
an error, without writing, when the file is missing or a directory. Never build
the path from request input.

## Cookies

```go
theme, err := c.Cookie("theme") // http.ErrNoCookie when missing
err = c.SetCookie("theme", "dark", 30*24*time.Hour)
c.ClearCookie("theme")
err = c.SetRawCookie(&http.Cookie{Name: "embed", Value: "1", SameSite: http.SameSiteNoneMode, Secure: true})
```

`SetCookie` uses Path `/`, `HttpOnly`, `SameSite=Lax`, and `Secure` when the
request came over HTTPS. A zero duration makes a session cookie.
`SetRawCookie` gives full control. Both return an error for invalid names or
values instead of dropping the cookie as `http.SetCookie` does. Cookie values
are not signed; use sessions for data the client must not change.

## Clients and proxies

```go
ip := c.IP()
secure := c.IsTLS()
if c.WantsJSON() { /* Accept prefers JSON over HTML */ }
```

Without trusted proxies, `IP` returns the connection address and ignores
`X-Forwarded-For`, which any client can send. Behind a load balancer, list its
addresses:

```go
app.SetContextConfig(httpcontext.Config{
    TrustedProxies: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
})
```

`IP` then walks `X-Forwarded-For` from the right and returns the first address
that is not a trusted proxy. `IsTLS` believes `X-Forwarded-Proto` only from a
trusted proxy.
