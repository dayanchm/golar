# Golar v0.3.0 — Database & Migrations

## Goal

Build Golar's first database layer and Laravel-inspired migration system.

Developers should be able to configure a database, create migrations, run migrations, rollback changes, and build tables using Golar's schema API.

---

# 1. Database Package Structure

Create the initial database structure.

```text
database/
├── connection/
│   ├── connection.go
│   ├── manager.go
│   └── manager_test.go
│
├── drivers/
│   ├── sqlite/
│   │   └── sqlite.go
│   ├── postgres/
│   │   └── postgres.go
│   └── mysql/
│       └── mysql.go
│
├── migrations/
│   ├── migration.go
│   ├── migrator.go
│   ├── repository.go
│   └── migrator_test.go
│
├── schema/
│   ├── schema.go
│   ├── blueprint.go
│   ├── column.go
│   ├── index.go
│   └── compiler.go
│
└── database.go
```

## Tasks

- [x] Create `database` package
- [x] Create connection package
- [x] Create driver structure
- [x] Create migration package
- [x] Create schema package
- [x] Add unit tests

---

# 2. Database Configuration

## Goal

Applications should configure database connections without directly working with `database/sql`.

Example configuration:

```go
db := database.Config{
    Driver:   "postgres",
    Host:     "localhost",
    Port:     5432,
    Database: "golar",
    Username: "postgres",
    Password: "secret",
}
```

Environment variables:

```env
DB_DRIVER=postgres
DB_HOST=localhost
DB_PORT=5432
DB_DATABASE=golar
DB_USERNAME=postgres
DB_PASSWORD=
```

## Tasks

- [x] Create `Config`
- [x] Driver configuration
- [x] Host
- [x] Port
- [x] Database name
- [x] Username
- [x] Password
- [x] SSL configuration
- [x] Connection timeout
- [x] Environment variable support
- [x] Configuration validation

---

# 3. Connection Manager

## Goal

Create a central database connection manager.

Desired API:

```go
db, err := database.Connect(config)
```

Later:

```go
db := golar.DB()
```

## Tasks

- [x] Create `Connection` interface
- [x] Create connection manager
- [x] Open database connections
- [x] Close database connections
- [x] Ping database
- [x] Handle connection errors
- [x] Connection pooling
- [x] Max open connections
- [x] Max idle connections
- [x] Connection lifetime
- [x] Tests

---

# 4. Database Drivers

Initially support:

- [x] SQLite
- [x] PostgreSQL
- [x] MySQL

Use Go's standard:

```go
database/sql
```

as the common database abstraction.

Database-specific drivers should stay isolated under:

```text
database/drivers/
```

## Driver Interface

Design an internal abstraction such as:

```go
type Driver interface {
    Name() string
    Open(config Config) (*sql.DB, error)
}
```

## Tasks

- [x] Driver interface
- [x] Driver registry
- [x] SQLite driver
- [x] PostgreSQL driver
- [x] MySQL driver
- [x] Unknown driver error
- [x] Driver tests

---

# 5. Multiple Database Connections

Support named connections.

Example:

```go
database.Connection("default")
database.Connection("analytics")
database.Connection("legacy")
```

Configuration example:

```text
default   → PostgreSQL
analytics → PostgreSQL
legacy    → MySQL
```

## Tasks

- [x] Named connections
- [x] Default connection
- [x] Register connection
- [x] Retrieve connection
- [x] Close one connection
- [x] Close all connections
- [x] Duplicate connection protection
- [x] Tests

---

# 6. Migration System

## Goal

Developers should be able to version database schema changes.

Desired command:

```bash
golar make:migration create_users_table
```

Generated migration:

```text
database/migrations/
└── 20261010_120000_create_users_table.go
```

## Tasks

- [x] Define Migration interface
- [x] Migration registration
- [x] Migration discovery strategy
- [x] Migration ordering
- [x] Migration execution
- [x] Track executed migrations
- [x] Prevent duplicate execution
- [x] Migration errors
- [x] Migration tests

---

# 7. Migration Interface

Initial design:

```go
type Migration interface {
    Up(schema *schema.Schema) error
    Down(schema *schema.Schema) error
}
```

Example:

```go
type CreateUsersTable struct{}

func (m CreateUsersTable) Up(schema *schema.Schema) error {
    return nil
}

func (m CreateUsersTable) Down(schema *schema.Schema) error {
    return nil
}
```

## Tasks

- [x] `Migration` interface
- [x] `Up()`
- [x] `Down()`
- [x] Migration name
- [x] Migration timestamp/version
- [x] Migration validation

---

# 8. Migrations Table

Golar needs its own internal table:

```text
golar_migrations
```

Example:

```text
id
migration
batch
executed_at
```

Example data:

```text
1 | 20261010_create_users_table | 1 | ...
2 | 20261010_create_posts_table | 1 | ...
3 | 20261011_add_email_to_users | 2 | ...
```

## Tasks

- [x] Automatically create migrations table
- [x] Record completed migration
- [x] Store batch number
- [x] Read executed migrations
- [x] Determine pending migrations
- [x] Delete records during rollback
- [x] Tests

---

# 9. Migration Runner

## Command

```bash
golar migrate
```

Process:

```text
Load migrations
      ↓
Read golar_migrations
      ↓
Find pending migrations
      ↓
Sort migrations
      ↓
Run Up()
      ↓
Record migration
```

## Tasks

- [x] Find pending migrations
- [x] Sort migrations
- [x] Execute `Up()`
- [x] Record successful migration
- [x] Stop on error
- [x] Return useful error messages
- [x] Print migration results
- [x] Tests

Example output:

```text
Golar Migrations

RUNNING  20261010_create_users_table
DONE     20261010_create_users_table  14ms

RUNNING  20261010_create_posts_table
DONE     20261010_create_posts_table  8ms

2 migrations completed.
```

---

# 10. Migration Rollback

## Command

```bash
golar migrate:rollback
```

Rollback the latest batch.

Example:

```text
Batch 1
  create_users_table
  create_posts_table

Batch 2
  add_avatar_to_users
```

Running:

```bash
golar migrate:rollback
```

should rollback only:

```text
add_avatar_to_users
```

## Tasks

- [x] Find latest batch
- [x] Reverse migration order
- [x] Execute `Down()`
- [x] Delete migration record
- [x] Stop on rollback failure
- [x] Tests

---

# 11. Migration Reset

Command:

```bash
golar migrate:reset
```

Roll back all migrations.

## Tasks

- [x] Read all executed migrations
- [x] Reverse order
- [x] Execute every `Down()`
- [x] Clear migration records
- [x] Tests

---

# 12. Migration Refresh

Command:

```bash
golar migrate:refresh
```

Equivalent to:

```text
reset
+
migrate
```

## Tasks

- [x] Rollback all migrations
- [x] Run migrations again
- [x] Preserve error handling
- [x] Tests

---

# 13. Migration Status

Command:

```bash
golar migrate:status
```

Example:

```text
Migration                              Status
------------------------------------------------
20261010_create_users_table            Ran
20261010_create_posts_table            Ran
20261011_create_comments_table         Pending
```

## Tasks

- [x] List migrations
- [x] Mark executed migrations
- [x] Mark pending migrations
- [x] Show batch
- [x] Stable output ordering

---

# 14. Schema Builder

## Goal

Avoid writing raw SQL for common schema operations.

Desired API:

```go
schema.Create("users", func(table *schema.Blueprint) {
    table.ID()
    table.String("name", 255)
    table.String("email", 255).Unique()
    table.String("password", 255)
    table.Timestamps()
})
```

This should compile to database-specific SQL.

---

# 15. Blueprint

Create:

```go
type Blueprint struct {
    Table   string
    Columns []Column
    Indexes []Index
}
```

## Tasks

- [x] Blueprint
- [x] Columns
- [x] Indexes
- [x] Primary keys
- [x] Foreign keys

---

# 16. Column Types

Initial schema API:

```go
table.ID()

table.String("name", 255)

table.Text("description")

table.Integer("age")

table.BigInteger("views")

table.Boolean("active")

table.Float("price")

table.Decimal("amount", 10, 2)

table.Date("birthday")

table.DateTime("published_at")

table.Timestamp("verified_at")

table.JSON("metadata")

table.UUID("uuid")

table.Binary("data")
```

## Modifiers

```go
table.String("email", 255).
    Unique().
    NotNull()
```

Other modifiers:

- [x] Nullable
- [x] NotNull
- [x] Default
- [x] Unique
- [x] Primary
- [x] AutoIncrement
- [x] Unsigned where supported

---

# 17. Timestamps Helper

Laravel-like:

```go
table.Timestamps()
```

Generates:

```text
created_at
updated_at
```

Also:

```go
table.SoftDeletes()
```

Generates:

```text
deleted_at
```

## Tasks

- [x] `Timestamps()`
- [x] `SoftDeletes()`
- [x] Tests

---

# 18. Schema Operations

Support:

```go
schema.Create(...)
```

```go
schema.Drop("users")
```

```go
schema.DropIfExists("users")
```

```go
schema.Rename("users", "customers")
```

```go
schema.HasTable("users")
```

```go
schema.HasColumn("users", "email")
```

## Tasks

- [x] Create table
- [x] Drop table
- [x] Drop if exists
- [x] Rename table
- [x] Check table
- [x] Check column
- [x] Tests

---

# 19. Alter Tables

Desired API:

```go
schema.Table("users", func(table *schema.Blueprint) {
    table.String("phone", 50).Nullable()
})
```

Later:

```go
table.DropColumn("phone")
table.RenameColumn("name", "full_name")
```

## Tasks

- [x] Add column
- [x] Drop column
- [x] Rename column
- [x] Modify column
- [x] Add index
- [x] Drop index
- [x] Add foreign key
- [x] Drop foreign key

---

# 20. Indexes

Desired API:

```go
table.Index("email")
table.Unique("email")
```

Composite:

```go
table.Index("user_id", "created_at")
```

## Tasks

- [x] Normal index
- [x] Unique index
- [x] Composite index
- [x] Primary index
- [x] Index names
- [x] Drop index

---

# 21. Foreign Keys

Desired API:

```go
table.Foreign("user_id").
    References("id").
    On("users").
    OnDelete("cascade")
```

Potential shortcut:

```go
table.ForeignID("user_id").
    Constrained("users").
    CascadeOnDelete()
```

## Tasks

- [x] Foreign key definition
- [x] References
- [x] Target table
- [x] ON DELETE
- [x] ON UPDATE
- [x] Cascade helpers
- [x] Tests

---

# 22. SQL Compiler

The Schema Builder must not generate identical SQL for every database.

Structure:

```text
database/schema/
├── compiler.go
└── compilers/
    ├── sqlite.go
    ├── postgres.go
    └── mysql.go
```

Example:

```go
compiler.CompileCreate(blueprint)
```

## Tasks

- [x] Compiler interface
- [x] SQLite compiler
- [x] PostgreSQL compiler
- [x] MySQL compiler
- [x] Quote identifiers correctly
- [x] Compile column types
- [x] Compile indexes
- [x] Compile foreign keys
- [x] Compiler unit tests

---

# 23. Transactions

Desired API:

```go
err := db.Transaction(func(tx *database.Tx) error {
    // database operations

    return nil
})
```

Behavior:

```text
return nil
    ↓
COMMIT

return error
    ↓
ROLLBACK
```

## Tasks

- [x] Begin transaction
- [x] Commit
- [x] Rollback
- [x] Transaction callback
- [x] Panic safety
- [x] Tests

---

# 24. Raw SQL

Developers must still be able to access SQL directly.

```go
db.Exec(...)
db.Query(...)
db.QueryRow(...)
```

## Tasks

- [x] Exec
- [x] Query
- [x] QueryRow
- [x] Context variants
- [x] Parameter binding
- [x] Error propagation

Do not hide `database/sql` completely.

---

# 25. Migration Generator

## Command

```bash
golar make:migration create_users_table
```

Generated file:

```text
database/migrations/
20261010_120000_create_users_table.go
```

Template:

```go
package migrations

import "github.com/dayanchm/golar/database/schema"

type CreateUsersTable struct{}

func (m CreateUsersTable) Up(s *schema.Schema) error {
    return s.Create("users", func(table *schema.Blueprint) {
        table.ID()
        table.String("name", 255)
        table.String("email", 255).Unique()
        table.Timestamps()
    })
}

func (m CreateUsersTable) Down(s *schema.Schema) error {
    return s.DropIfExists("users")
}
```

## Tasks

- [x] Generate timestamp
- [x] Normalize migration name
- [x] Generate Go struct name
- [x] Generate filename
- [x] Generate Up
- [x] Generate Down
- [x] Prevent duplicate filenames
- [x] Format generated Go source
- [x] Generator tests

---

# 26. CLI Commands

Implement:

```bash
golar migrate
golar migrate:rollback
golar migrate:reset
golar migrate:refresh
golar migrate:status

golar make:migration create_users_table
```

Optional later:

```bash
golar migrate --step=1
golar migrate --pretend
golar migrate --force
```

---

# 27. Dry Run

Useful development feature:

```bash
golar migrate --pretend
```

Instead of modifying the database:

```text
CREATE TABLE users (...)
CREATE UNIQUE INDEX ...
```

is printed.

## Tasks

- [x] Compile migrations without executing
- [x] Print SQL
- [x] Guarantee no database mutation
- [x] Tests

---

# 28. Migration Safety

## Tasks

- [x] Migration execution locking
- [x] Prevent concurrent migration runs
- [x] Stop after failed migration
- [x] Do not record failed migrations
- [x] Handle partial failure carefully
- [x] Validate migration names
- [x] Detect duplicate migrations

Production migrations must not silently continue after failures.

---

# 29. Database Errors

Create useful Golar errors.

Examples:

```text
database: unsupported driver "oracle"

database: connection "default" not found

migration: duplicate migration "create_users_table"

migration: failed running "create_users_table"

schema: unsupported column type for sqlite
```

## Tasks

- [x] Driver errors
- [x] Connection errors
- [x] Migration errors
- [x] Schema errors
- [x] Wrap underlying errors
- [x] Preserve error inspection with `errors.Is` / `errors.As`

---

# 30. Testing

## Unit Tests

- [x] Configuration
- [x] Connection manager
- [x] Migration repository
- [x] Migrator
- [x] Blueprint
- [x] Columns
- [x] Indexes
- [x] Foreign keys
- [x] SQL compilers

## Integration Tests

Run real migrations against:

- [x] SQLite
- [x] PostgreSQL
- [x] MySQL

Test:

- [x] migrate
- [x] rollback
- [x] reset
- [x] refresh
- [x] transactions
- [x] indexes
- [x] foreign keys

---

# 31. Race Testing

Run:

```bash
go test -race ./...
```

## Requirements

- [x] Connection manager has no data races
- [x] Driver registry has no data races
- [x] Migration registry has no data races

---

# 32. CI Database Matrix

GitHub Actions should test:

```text
SQLite
PostgreSQL
MySQL
```

## Tasks

- [x] SQLite tests
- [x] PostgreSQL service
- [x] MySQL service
- [x] Migration tests
- [x] Race tests
- [x] Build

---

# 33. Documentation

Create:

```text
docs/database/
├── configuration.md
├── connections.md
├── migrations.md
├── schema-builder.md
└── transactions.md
```

Document:

- [x] Configuration
- [x] Supported databases
- [x] Creating migrations
- [x] Running migrations
- [x] Rollbacks
- [x] Schema builder
- [x] Transactions
- [x] Raw SQL

---

# 34. Example Application

Create:

```text
examples/database/
└── main.go
```

Example should demonstrate:

```text
Connect
   ↓
Migrate
   ↓
Create user
   ↓
Query database
   ↓
Transaction
   ↓
Close connection
```

---

# Recommended Development Order

Do **not** build everything simultaneously.

Follow this order:

```text
1. Config
       ↓
2. Connection Manager
       ↓
3. SQLite Driver
       ↓
4. Migration Interface
       ↓
5. Migration Repository
       ↓
6. Migrator
       ↓
7. Schema Blueprint
       ↓
8. SQLite SQL Compiler
       ↓
9. migrate
       ↓
10. rollback
       ↓
11. PostgreSQL
       ↓
12. MySQL
       ↓
13. Transactions
       ↓
14. CLI Generator
       ↓
15. Integration Tests
```

SQLite should be used first because it makes initial development and testing easier.

Do not implement the ORM in this version.

ORM belongs to:

```text
v0.4.0
```

---

# First Milestone

Before implementing PostgreSQL/MySQL, this must work:

```bash
golar make:migration create_users_table
```

Then:

```bash
golar migrate
```

Database:

```text
users
golar_migrations
```

Then:

```bash
golar migrate:status
```

Expected:

```text
Migration                           Batch    Status
---------------------------------------------------
20261010_create_users_table         1        Ran
```

Then:

```bash
golar migrate:rollback
```

Result:

```text
users table removed
migration record removed
```

---

# v0.3.0 Definition of Done

## Connections

- [x] SQLite
- [x] PostgreSQL
- [x] MySQL
- [x] Connection pooling
- [x] Named connections
- [x] Connection lifecycle

## Migrations

- [x] Create migration
- [x] Run migration
- [x] Rollback
- [x] Reset
- [x] Refresh
- [x] Status
- [x] Migration batches
- [x] Migration locking

## Schema Builder

- [x] Create table
- [x] Alter table
- [x] Drop table
- [x] Columns
- [x] Indexes
- [x] Foreign keys
- [x] SQLite compiler
- [x] PostgreSQL compiler
- [x] MySQL compiler

## Database

- [x] Transactions
- [x] Raw SQL access
- [x] Error handling

## Quality

- [x] Unit tests
- [x] Integration tests
- [x] Race tests
- [x] CI database matrix
- [x] Documentation
- [x] Database example

---

# v0.3.0 Release

Before tagging:

```bash
go fmt ./...
go vet ./...
go test ./...
go test -race ./...
```

Then:

```bash
git tag v0.3.0
git push origin v0.3.0
```

---

# Next Version

After v0.3.0:

```text
v0.4.0 — ORM & Query Builder
```

Planned features:

- Query Builder
- Models
- CRUD
- Model metadata
- Relationships
- HasOne
- HasMany
- BelongsTo
- ManyToMany
- Eager Loading
- Pagination
- Soft Deletes
- Hooks
- Scopes
- Factories
- Seeders

## v0.3.0 Validation Notes

- SQLite, PostgreSQL 17 and MySQL 8.4 migration/transaction/index/foreign-key integration tests passed against isolated databases with race detection.
- CLI generator, migrate, rollback, reset, refresh, status and offline pretend passed against generated SQLite projects.
- Schema alter operations include SQLite table rebuilds; native database restrictions and metadata-dependent offline pretend limitations are documented in docs/database.
- MySQL DDL failure markers require manual inspection and repair; failed migrations are not added to the successful ledger.
- CI database service matrix is configured; its remote GitHub execution remains to be confirmed.
- No release tag or remote push was performed.
