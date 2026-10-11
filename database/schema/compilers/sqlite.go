package compilers

import "github.com/dayanchm/golar/database/schema"

type SQLite struct{ schema.SQLCompiler }

func NewSQLite() schema.Compiler { return SQLite{SQLCompiler: schema.SQLCompiler{Dialect: "sqlite"}} }
