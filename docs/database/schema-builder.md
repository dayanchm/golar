# Schema builder

```go
s := schema.New(db.DB, db.Driver)
err := s.Create("users", func(table *schema.Blueprint) {
    table.ID()
    table.String("name", 255)
    table.String("email", 255).Unique()
    table.Timestamps()
    table.SoftDeletes()
})
```

Types: ID, String, Text, Integer, BigInteger, Boolean, Float, Decimal, Date,
DateTime, Timestamp, JSON, UUID and Binary. Columns default to NOT NULL.
Modifiers: Nullable, NotNull, Default, Unique, Primary, AutoIncrement and Unsigned
(MySQL only). Defaults are escaped literals or schema.CurrentTimestamp.
Invalid types, lengths, precision or unsupported options return errors.

```go
err = s.Table("users", func(table *schema.Blueprint) {
    table.String("phone", 50).Nullable()
    table.String("name", 300).Change()
    table.Index("email", "phone").Named("users_contact_index")
})
```

Table supports DropColumn, RenameColumn, DropIndex and DropForeign. Index, Unique
and Primary accept composite lists; generated names are deterministic, shortened
for server identifier limits, or customized with Named. Foreign(...).References(...).
On(...).OnDelete(...).OnUpdate(...) defines foreign keys. ForeignID("user_id").
Constrained("users").CascadeOnDelete() also adds the integer column. Supported actions:
cascade, restrict, set null, no action and set default (not MySQL). Match types and
nullability with referenced keys/actions.

Operations include Drop, DropIfExists, Rename, HasTable and HasColumn. NewContext
supports cancellation. Compilers quote identifiers and use dialect-specific identity,
JSON, UUID and binary types; introspection binds values. PostgreSQL/MySQL/SQLite
compiler constructors are available under schema/compilers.

SQLite changes requiring new constraints or column modifications rebuild the table
using its [documented procedure](https://www.sqlite.org/lang_altertable.html),
preserving rows and indexes/triggers and checking foreign keys before commit.
A rebuild inside a foreign-key-enabled user transaction is explicitly rejected;
run it outside that transaction or use the migrator. Native drop/rename restrictions
apply: drop dependent indexes/objects before dropping their columns. Advanced raw
SQL definitions and dependent views need explicit review and migration code.

Create/native alter execute transactionally with a *sql.DB on SQLite/PostgreSQL.
MySQL DDL atomicity is server-defined; migrations retain partial-failure markers.
