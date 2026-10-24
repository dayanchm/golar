# Query builder

The query API supports `Table`, `Select`, AND-connected `Where`, `WhereNull`,
`WhereNotNull`, `Join`, `LeftJoin`, `RightJoin`, `OrderBy`, `Limit`, `Offset`,
`ForPage`, `Paginate`, the aggregates `Count`, `Sum`, `Avg`, `Min` and `Max`,
single-row `Insert`, `Update` and `Delete`. ORM models and result mapping remain
planned.

```go
q := db.Table("users").
    Select("id", "name", "email").
    Where("active", "=", true).
    OrderBy("id", "desc").
    Limit(20).
    Offset(40)
statement, bindings, err := q.Compile() // ToSQL() is an alias
```

Check `err` before using SQL. To execute directly, call `q.QueryContext(ctx)`,
scan the returned `*sql.Rows`, close it, and check `rows.Err()`.
`tx.Table(...)` uses an existing Golar transaction and leaves its lifecycle to
`TransactionContext`. Query errors wrap the driver error with `%w`.

Builders are immutable; derived queries do not change their parents. Connections
and bound objects remain caller-owned. Do not mutate bound objects concurrently.
`Select` replaces the projection; the default is `*`. `Where` and `OrderBy`
append clauses. `Limit` replaces the limit; zero selects no rows.

`query.New(runner, dialect)` also accepts standard SQL connections or transactions.
Use a nil runner for compilation only. Driver names are `sqlite`, `postgres`,
and `mysql`, matching the existing database configuration.

SQLite and PostgreSQL quote identifiers with double quotes; MySQL uses backticks.
PostgreSQL binds `$1`, `$2`, etc.; SQLite and MySQL bind `?`, including limits.
Identifiers accept ASCII letters, underscores and subsequent digits, with dotted
qualification. Projections also accept `*` and `table.*`. PostgreSQL and MySQL
identifier components respect their 63/64-byte limits. Aliases, expressions and
raw fragments are not supported in this foundation.

Supported comparisons: `=`, `!=`, `<>`, `<`, `<=`, `>`, `>=`, `LIKE`, `NOT LIKE`.
Directions must be `asc` or `desc`. Unknown dialects, invalid identifiers,
operators, directions, empty explicit projections, nil comparisons and negative
limits return errors during compilation. Values are always bound, never SQL text.
Use `WhereNull(column)` and `WhereNotNull(column)` for NULL checks; they bind nothing.

`WhereIn(column, values)` and `WhereNotIn(column, values)` bind each value
(`"id" IN (?, ?, ?)`). An empty `WhereIn` list matches no rows and an empty
`WhereNotIn` list matches every row; NULL list entries are rejected.

`Offset` binds a non-negative row count. Without `Limit`, SQLite compiles
`LIMIT -1 OFFSET ?` and MySQL `LIMIT 18446744073709551615 OFFSET ?`, since both
require a limit before an offset; PostgreSQL compiles `OFFSET $n` alone.

A trusted raw SQL escape hatch remains future work.

## Insert

```go
result, err := db.Table("users").Insert(map[string]any{
    "name":  "Dayanch",
    "email": "user@example.com",
})
// SQLite/MySQL: INSERT INTO "users" ("email", "name") VALUES (?, ?)
// PostgreSQL:   INSERT INTO "users" ("email", "name") VALUES ($1, $2)
```

Columns are sorted, so SQL and bindings never depend on Go map order. Values are
always bound; a nil value inserts NULL. Empty data, invalid column names and
builders carrying `Select`, `Where`, `OrderBy`, `Limit` or `Offset` return errors
before any database call. `InsertContext(ctx, values)` honours cancellation, and
`CompileInsert(values)` returns SQL and bindings without executing. Driver errors
are wrapped with `%w`.

`InsertGetID(ctx, values, "id")` returns the generated integer key on every
dialect: PostgreSQL appends `RETURNING "id"`, SQLite and MySQL use `LastInsertId`.

`result.RowsAffected()` works on all drivers. `result.LastInsertId()` works on
SQLite and MySQL; the PostgreSQL driver does not support it (use `RETURNING`
with raw SQL until the builder supports it). Custom runners passed to `query.New`
must implement both `QueryContext` and `ExecContext`.

## Update and delete

```go
result, err := db.Table("users").
    Where("id", "=", 7).
    Update(map[string]any{"name": "New", "email": nil})
// UPDATE "users" SET "email" = ?, "name" = ? WHERE "id" = ?

result, err = db.Table("users").Where("active", "=", false).Delete()
// DELETE FROM "users" WHERE "active" = ?
```

SET columns are sorted; SET bindings come before WHERE bindings, and PostgreSQL
numbers them continuously (`$1` … `$n`). `UpdateContext`, `DeleteContext`,
`CompileUpdate` and `CompileDelete` mirror the insert API.

`Update` and `Delete` without a `Where` clause return an error instead of touching
every row. Call `AllRows()` to state that intent explicitly:

```go
db.Table("sessions").AllRows().Delete()
```

`Select`, joins, `OrderBy`, `Limit` and `Offset` are rejected on writes because
their meaning differs across databases.

## Joins

```go
rows, err := db.Table("users").
    Select("users.name", "posts.title").
    Join("posts", "posts.user_id", "=", "users.id").
    LeftJoin("comments", "comments.post_id", "=", "posts.id").
    QueryContext(ctx)
```

`Join` (INNER), `LeftJoin` and `RightJoin` compare two columns with `=`, `!=`,
`<>`, `<`, `<=`, `>` or `>=`. Both sides are identifiers, never bound values;
filter on values with `Where`. Table aliases are not supported yet. RIGHT JOIN
requires SQLite 3.39 or later (the bundled driver qualifies).

## Aggregates

```go
total, err := db.Table("users").Where("active", "=", true).Count(ctx) // int64
sum, err := db.Table("orders").Sum(ctx, "amount")                       // sql.NullFloat64
avg, err := db.Table("orders").Avg(ctx, "amount")                       // sql.NullFloat64
var newest time.Time
err = db.Table("posts").Max(ctx, "created_at", &newest)
```

Aggregates apply `Where` and joins but ignore `Select`, `OrderBy`, `Limit` and
`Offset`, so `Count` always returns the number of matching rows. `Sum` and `Avg`
are not `Valid` when no rows match. `Min` and `Max` scan into any pointer that
`sql.Rows.Scan` accepts; use a `sql.Null*` type when no rows may match.
`CompileAggregate(function, column)` returns the SQL without executing.

## Pagination

```go
rows, page, err := db.Table("users").OrderBy("id", "asc").Paginate(ctx, 2, 25)
defer rows.Close()
// page.Total, page.LastPage, page.HasMorePages()
```

`ForPage(page, perPage)` sets `Limit` and `Offset` for a 1-based page. `Paginate`
runs `Count` and then the page query. They are two statements; build the query
from `tx.Table` when both must see the same snapshot. `LastPage` is at least 1.
Pages and sizes below 1 return errors. Always order paginated queries so pages
are stable.

## Dialects

| | SQLite | PostgreSQL | MySQL |
|---|---|---|---|
| Identifier quote | `"` | `"` | `` ` `` |
| Placeholders | `?` | `$1`, `$2`, … | `?` |
| Identifier length | unchecked | 63 bytes | 64 bytes |
| OFFSET without LIMIT | `LIMIT -1 OFFSET ?` | `OFFSET $1` | `LIMIT 18446744073709551615 OFFSET ?` |
| `LastInsertId` | yes | no | yes |

The query tests run against all three databases in CI. Locally they use SQLite;
set `GOLAR_TEST_DRIVER` and `GOLAR_TEST_DSN` to run them against PostgreSQL or MySQL.
