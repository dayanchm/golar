# API resources

`github.com/dayanchm/golar/http/resource` turns models into JSON responses.
A resource is a function that names the fields to send, so a password hash or
a newly added column never reaches clients unless a resource includes it.

```go
func UserResource(ctx context.Context, u models.User) resource.Object {
    return resource.Object{
        "id":         u.ID,
        "name":       u.Name,
        "email":      resource.When(isAdmin(ctx), u.Email),
        "created_at": u.CreatedAt,
    }
}

app.Get("/api/users/{id}", func(c *golar.Context) error {
    id, err := strconv.ParseInt(c.Param("id"), 10, 64)
    if err != nil {
        return &golar.HTTPError{Code: http.StatusNotFound}
    }
    user, err := orm.Find[models.User](c.Context(), db, id)
    if err != nil {
        return err
    }
    return c.JSON(http.StatusOK, resource.Item(c.Context(), *user, UserResource))
})
```

```json
{"data": {"id": 1, "name": "Ada", "created_at": "2026-10-29T10:00:00Z"}}
```

The context is the request's, so resources can read the authenticated user
with `auth.User(ctx)`.

## Collections and pagination

```go
resource.Collection(ctx, users, UserResource) // {"data": [...]}; nil encodes as []

users, page, err := orm.Query[models.User](db).OrderBy("id", "asc").Paginate(ctx, pageNumber, 15)
return c.JSON(http.StatusOK, resource.Paginated(ctx, users, page, UserResource, resource.PageURL(c.Request, "page")))
```

```json
{
  "data": [{"id": 16, "name": "P"}, {"id": 17, "name": "Q"}],
  "meta": {"current_page": 2, "per_page": 15, "total": 32, "last_page": 3, "from": 16, "to": 17},
  "links": {"first": "/api/users?page=1", "last": "/api/users?page=3",
            "prev": "/api/users?page=1", "next": "/api/users?page=3"}
}
```

`from` and `to` are the 1-based positions of the page's first and last items,
or `null` for an empty page. `prev` and `next` are `null` at the ends. A page
past the end has `prev` pointing at the last page.

`PageURL` keeps the request's path and query and replaces the page parameter.
Its links are relative, so a forged `Host` header cannot redirect clients.
Pass `nil` instead to leave `links` out, or your own `func(page int) string`.

## Conditional fields

| Helper | Result |
| --- | --- |
| `resource.When(ok, value)` | `value` when `ok`, otherwise the field is left out |
| `resource.WhenFunc(ok, func() any { ... })` | builds the value only when `ok` |
| `resource.WhenNotNil(ptr)` | `*ptr`, or left out for `nil`, such as a relation that was not loaded |
| `obj.MergeWhen(ok, resource.Object{...})` | adds several fields when `ok` |
| `resource.Missing` | a value that leaves its field out |

Missing fields are dropped from `resource.Object` values at every level, so
nest `resource.Object` rather than `map[string]any`. A `Missing` value inside
a slice is not removed.

## Documents

`Item`, `Collection` and `Paginated` return a `resource.Document`. Set `Extra`
for more top-level fields:

```go
doc := resource.Collection(ctx, posts, PostResource)
doc.Extra = resource.Object{"version": "2026-10"}
```

`data`, `meta` and `links` cannot be replaced through `Extra`.
