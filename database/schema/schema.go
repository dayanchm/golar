package schema

import (
	"context"
	"database/sql"
	"fmt"
	"io"
)

type Executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
type Schema struct {
	executor Executor
	compiler SQLCompiler
	ctx      context.Context
	pretend  io.Writer
}

func New(executor Executor, dialect string) *Schema {
	return NewContext(context.Background(), executor, dialect)
}
func NewContext(ctx context.Context, executor Executor, dialect string) *Schema {
	return &Schema{executor: executor, compiler: SQLCompiler{Dialect: dialect}, ctx: ctx}
}
func Pretend(dialect string, out io.Writer) *Schema {
	s := New(nil, dialect)
	s.pretend = out
	return s
}
func (s *Schema) Exec(query string, args ...any) error {
	if _, err := NewCompiler(s.compiler.Dialect); err != nil {
		return err
	}
	if s.pretend != nil {
		if len(args) > 0 {
			return fmt.Errorf("schema: bound raw SQL is unavailable in pretend mode")
		}
		_, err := fmt.Fprintln(s.pretend, query+";")
		return err
	}
	if s.executor == nil {
		return fmt.Errorf("schema: executor is unavailable")
	}
	_, err := s.executor.ExecContext(s.ctx, query, args...)
	if err != nil {
		return fmt.Errorf("schema: execute: %w", err)
	}
	return nil
}
func (s *Schema) run(statements []string) error {
	if db, ok := s.executor.(*sql.DB); ok && s.pretend == nil && s.compiler.Dialect != "mysql" {
		tx, err := db.BeginTx(s.ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		copy := *s
		copy.executor = tx
		if err = copy.run(statements); err != nil {
			return err
		}
		return tx.Commit()
	}
	for _, statement := range statements {
		if err := s.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}
func (s *Schema) Create(table string, build func(*Blueprint)) error {
	b := &Blueprint{Table: table}
	build(b)
	statements, err := s.compiler.CompileCreate(b)
	if err != nil {
		return err
	}
	return s.run(statements)
}
func (s *Schema) Table(table string, build func(*Blueprint)) error {
	b := &Blueprint{Table: table}
	build(b)
	if s.compiler.Dialect == "sqlite" && needsRebuild(b) {
		return s.rebuild(b)
	}
	statements, err := s.compiler.CompileAlter(b)
	if err != nil {
		return err
	}
	return s.run(statements)
}
func (s *Schema) Drop(table string) error {
	if err := s.compiler.name(table); err != nil {
		return err
	}
	return s.Exec("DROP TABLE " + s.compiler.Quote(table))
}
func (s *Schema) DropIfExists(table string) error {
	if err := s.compiler.name(table); err != nil {
		return err
	}
	return s.Exec("DROP TABLE IF EXISTS " + s.compiler.Quote(table))
}
func (s *Schema) Rename(from, to string) error {
	if err := s.compiler.name(from); err != nil {
		return err
	}
	if err := s.compiler.name(to); err != nil {
		return err
	}
	return s.Exec("ALTER TABLE " + s.compiler.Quote(from) + " RENAME TO " + s.compiler.Quote(to))
}
func (s *Schema) HasTable(table string) (bool, error) {
	if s.executor == nil {
		return false, fmt.Errorf("schema: introspection is unavailable in pretend mode")
	}
	query := ""
	switch s.compiler.Dialect {
	case "sqlite":
		query = "SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?"
	case "postgres":
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=current_schema() AND table_name=$1"
	case "mysql":
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name=?"
	default:
		return false, fmt.Errorf("schema: unknown dialect")
	}
	var count int
	err := s.executor.QueryRowContext(s.ctx, query, table).Scan(&count)
	return count > 0, err
}
func (s *Schema) HasColumn(table, column string) (bool, error) {
	if _, err := NewCompiler(s.compiler.Dialect); err != nil {
		return false, err
	}
	if s.executor == nil {
		return false, fmt.Errorf("schema: introspection is unavailable in pretend mode")
	}
	if s.compiler.Dialect == "sqlite" {
		if err := s.compiler.name(table); err != nil {
			return false, err
		}
		rows, err := s.executor.QueryContext(s.ctx, "PRAGMA table_info("+s.compiler.Quote(table)+")")
		if err != nil {
			return false, err
		}
		defer rows.Close()
		for rows.Next() {
			var id, notNull, pk int
			var name, kind string
			var defaultValue any
			if err = rows.Scan(&id, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
				return false, err
			}
			if name == column {
				return true, nil
			}
		}
		return false, rows.Err()
	}
	query := "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name=?"
	if s.compiler.Dialect == "postgres" {
		query = "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 AND column_name=$2"
	}
	var count int
	err := s.executor.QueryRowContext(s.ctx, query, table, column).Scan(&count)
	return count > 0, err
}
