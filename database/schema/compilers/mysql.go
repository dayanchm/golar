package compilers

import "github.com/dayanchm/golar/database/schema"

type MySQL struct{ schema.SQLCompiler }

func NewMySQL() schema.Compiler { return MySQL{SQLCompiler: schema.SQLCompiler{Dialect: "mysql"}} }
