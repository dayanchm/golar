# Query builder foundation

The query API supports `Table`, `Select`, AND-connected `Where`, `WhereNull`,
`WhereNotNull`, `OrderBy`, `Limit`, `Offset` and single-row `Insert`. ORM,
updates, deletes, joins and result mapping remain planned.

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

`result.RowsAffected()` works on all drivers. `result.LastInsertId()` works on
SQLite and MySQL; the PostgreSQL driver does not support it (use `RETURNING`
with raw SQL until the builder supports it). Custom runners passed to `query.New`
must implement both `QueryContext` and `ExecContext`.
