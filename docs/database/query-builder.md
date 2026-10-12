# Query builder foundation

The initial query API supports `Table`, `Select`, AND-connected `Where`,
`OrderBy` and `Limit`. ORM, writes, joins and result mapping remain planned.

```go
q := db.Table("users").
    Select("id", "name", "email").
    Where("active", "=", true).
    OrderBy("id", "desc").
    Limit(20)
statement, bindings, err := q.Compile()
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
Explicit NULL predicates and a trusted raw SQL escape hatch remain future work.
