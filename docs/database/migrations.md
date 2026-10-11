# Migrations

```sh
golar make:migration create_users_table
golar migrate
golar migrate:status
golar migrate:rollback
golar migrate:reset
golar migrate:refresh
golar migrate --pretend
```

Use --dir for another project. The generator uses UTC timestamp filenames,
normalizes names, formats Go and rejects duplicate logical names/filenames.
Create_*_table migrations contain a starter schema; others have empty Up/Down
methods to fill in. Names require a valid YYYYMMDD date, optional HHMMSS and a
lowercase description. Registration rejects duplicates and nil implementations.
Generated init functions register Up/Down implementations. The CLI compiles a
temporary runner importing the project's migration package: discovery uses Go
imports, not runtime plugins. New scaffolds include an empty migrations package;
older projects acquire it with their first generated migration.

Migrate sorts names, skips applied entries and records successful runs in
golar_migrations (id, migration, batch, executed_at). Rollback reverses the latest
batch; reset reverses all batches; refresh holds one lock across both operations.
Status includes missing source files; rollback checks all selected sources before
changing anything. Do not edit applied migrations: create a new migration.

SQLite uses BEGIN IMMEDIATE with an atomic batch and foreign-key validation;
PostgreSQL uses an advisory lock and a transaction per migration. Locks remain
on one dedicated connection until completion. LockTimeout defaults to 5s.
Errors stop execution and preserve the underlying cause. Panics clean up locks
and transactions before being rethrown. Earlier PostgreSQL migrations may have
committed on a later failure; retry skips them.

MySQL [DDL can commit implicitly](https://dev.mysql.com/doc/refman/8.4/en/implicit-commit.html).
GET_LOCK serializes runners. A durable golar_migration_state marker is written
before Up/Down. Success updates the ledger and clears it together. Failure/crash
retains it and blocks future commands. Inspect and repair partial DDL manually,
then delete that specific state row only after schema and ledger are consistent.
Do not automatically clear it on retry. Rollback/reset execute Down and can delete data.

Pretend opens no database, acquires no lock and writes no metadata. It prints SQL
for all registered migrations, not just pending ones. Existing-schema introspection
and SQLite rebuilds requiring metadata explicitly fail offline. Its no-mutation
contract applies to the supplied Schema API; independent DB calls or other arbitrary
side effects written inside migration/init code cannot be intercepted. Keep
migration logic deterministic and restricted to its supplied Schema.

Programmatic use: migrations.New(db).Migrate(ctx). Set Out for progress or Registry
for an isolated migration list, as demonstrated in examples/database.
