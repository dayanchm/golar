# Configuration

Import github.com/dayanchm/golar/database; SQLite, PostgreSQL and MySQL drivers
are registered automatically and isolated under database/drivers.

```go
config := database.Config{
    Driver: "postgres", Host: "localhost", Port: 5432,
    Database: "golar", Username: "postgres", Password: "secret",
    SSLMode: "require", ConnectTimeout: 5 * time.Second,
    MaxOpenConnections: 10, MaxIdleConnections: 1,
    ConnectionLifetime: 30 * time.Minute,
}
db, err := database.Connect(config)
```

ConfigFromEnv reads DB_DRIVER, DB_HOST, DB_PORT, DB_DATABASE, DB_USERNAME,
DB_PASSWORD, DB_SSLMODE, DB_CONNECT_TIMEOUT, DB_MAX_OPEN_CONNECTIONS,
DB_MAX_IDLE_CONNECTIONS and DB_CONNECTION_LIFETIME. Durations use Go notation,
e.g. 5s. DATABASE_DSN overrides structured connection parameters. The function
reads process variables; CLI/scaffolds load .env first without overriding process values.

Defaults: SQLite at storage/app.db, localhost, ports 5432/3306, timeout 5s,
max open 10 (SQLite 1), max idle 1, lifetime 30m. Zero pool fields select defaults.
PostgreSQL SSL defaults to require (disable for local tests); MySQL TLS defaults
to false and also supports true, preferred and skip-verify. SQLite enables foreign
keys and busy_timeout per connection. Configuration is validated. Failed pings
close the connection. ConnectContext honors cancellation. Keep configuration and
DB.Driver unchanged while serving; Golar does not log passwords.
