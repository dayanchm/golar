package compilers

import "github.com/dayanchm/golar/database/schema"

type Postgres struct{ schema.SQLCompiler }

func NewPostgres() schema.Compiler {
	return Postgres{SQLCompiler: schema.SQLCompiler{Dialect: "postgres"}}
}
