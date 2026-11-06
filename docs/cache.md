# Cache

`github.com/dayanchm/golar/cache` stores short-lived values behind one
interface. `Memory` keeps them in the process, `File` in a directory and
`SQLStore` in a database table. It adds no dependencies.

```go
store := cache.NewMemory()

posts, err := cache.Remember(ctx, store, "posts:latest", 10*time.Minute, func(ctx context.Context) ([]Post, error) {
    return orm.Query[Post](db).OrderBy("id", "desc").Limit(20).Get(ctx)
})

err = cache.Put(ctx, store, "settings", settings, time.Hour)
settings, ok, err := cache.Get[Settings](ctx, store, "settings")
err = store.Delete(ctx, "settings")
```

## Stores

| Store | Survives restarts | Shared between instances | Use it for |
| --- | --- | --- | --- |
| `cache.NewMemory()` | no | no | development, tests and single instances |
| `cache.NewFile(dir)` | yes | no | single instances with a slow data source |
| `&cache.SQLStore{DB: db}` | yes | yes | several instances |

`Memory` removes expired values periodically as new ones are written, and
nothing else limits its size, so keep it away from keys built from unbounded
user input.

`File` names files by the SHA-256 of their key, keeps them private to the
current user and writes atomically. `Flush` and `Prune` only touch names the
store creates. `Add` and `Increment` are atomic within one process, so use
`SQLStore` when several processes share them.

`SQLStore` needs a table, which a migration creates:

```go
func (CreateCacheTable) Up(s *schema.Schema) error { return s.Create(cache.Table, cache.Columns) }
```

Keys are stored as SHA-256 hashes, so they can be any length and stay
case-sensitive on every database. Values are limited by the column type, 64 KiB
on MySQL.

`cache.FromEnv(db)` reads `CACHE_DRIVER` (`memory`, `file` or `database`) and
`CACHE_PATH`, by default `storage/cache`. Generated projects call it from
`internal/cache`, which holds the store as `cache.Store`.

## Expiry

A zero ttl keeps a value until it is deleted. Expired values are never
returned, but `File` and `SQLStore` leave them on disk until they are replaced
or pruned:

```go
n, err := store.Prune(ctx) // Memory, File and SQLStore

app.OnStart(func(ctx context.Context) error {
    go cache.PruneEvery(ctx, store, time.Hour) // until shutdown
    return nil
})
```

## Remember

`Remember` returns the cached value or calls the function and stores its
result. Errors are returned and not cached. A value that no longer decodes into
the type asked for, such as one written before a deploy changed a struct, is
computed again. Concurrent callers that miss each call the function, so an
expensive query can run more than once when a popular key expires.

`Get`, `Put` and `Remember` encode values as JSON. `store.Get` and `store.Put`
work with raw bytes.

## Counters and locks

```go
hits, err := store.Increment(ctx, "hits", 1)
left, err := cache.Decrement(ctx, store, "quota", 1)

acquired, err := store.Add(ctx, "lock:report", []byte("1"), time.Minute)
```

`Increment` starts missing keys at 0 with no expiry and keeps the expiry of
existing keys. To count in a window, create the key with `Add` first. Values
that are not integers, and results that overflow, return `cache.ErrNotInteger`.

`Add` stores a value only if the key has no live value, and only one concurrent
caller succeeds. That makes it a simple lock, but one with no owner: anything
that deletes the key releases it, and it expires even if the work is still
running.

Implement `cache.Store` for other backends such as Redis.
