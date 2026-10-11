# Transactions and raw SQL

```go
err := db.Transaction(func(tx *database.Tx) error {
    _, err := tx.Exec("INSERT INTO users (name) VALUES (?)", "Ali")
    return err
})
```

Nil commits, errors roll back, panics roll back and are rethrown. Begin, callback,
rollback and commit errors propagate with errors.Is/errors.As inspection.
TransactionContext honors cancellation. Tx embeds *sql.Tx.

DB embeds *sql.DB: Exec, Query, QueryRow and Context variants accept bound parameters.
Use ? for SQLite/MySQL and $1, $2 etc for PostgreSQL. Close rows and check rows.Err;
do not interpolate user input. Underlying SQL errors remain inspectable.
Schema.Exec supports raw SQL within its executor and wraps errors. Pretend prints
parameter-free raw SQL but rejects bound raw statements. Independent DB calls
inside migrations bypass transactional/dry-run guarantees. MySQL DDL cannot be
made transactional by wrapping it in Transaction because of implicit commits.
