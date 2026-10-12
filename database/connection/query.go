package connection

import "github.com/dayanchm/golar/database/query"

// Table starts an immutable query using this connection's driver.
func (db *DB) Table(name string) *query.Builder { return query.New(db, db.Driver).Table(name) }

// Table uses the existing transaction; it never commits or rolls it back.
func (tx *Tx) Table(name string) *query.Builder { return query.New(tx, tx.Driver).Table(name) }
