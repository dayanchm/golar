# Connections

Connect creates an independent pool; call Close when done. DB embeds *sql.DB,
including Ping/PingContext, statistics and raw SQL access.

```go
manager := database.NewManager()
db, err := manager.Register("default", config)
analytics, err := manager.Register("analytics", analyticsConfig)
db, err = manager.Connection("default")
err = manager.Close("analytics")
err = manager.CloseAll()
```

Duplicate registration is rejected, including concurrent requests. Failed opens
are not retained. Connection("") and Close("") mean default; missing names are errors.
Connection/driver registries use mutexes. Global equivalents are database.Register,
Connection, Close and CloseAll; standalone Connect does not implicitly register.
SQLite defaults to one connection for :memory: correctness; use a file for
cross-process locking. Access to closed pools returns database/sql errors.
Wrapped errors preserve errors.Is/errors.As. This release has no ORM.
