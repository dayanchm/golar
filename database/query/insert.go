package query

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// Insert inserts one row using context.Background. See InsertContext.
func (b *Builder) Insert(values map[string]any) (sql.Result, error) {
	return b.InsertContext(context.Background(), values)
}

// InsertContext inserts one row. Columns are sorted so SQL and bindings are
// deterministic. LastInsertId support depends on the driver; PostgreSQL does
// not provide it. Driver and cancellation errors are preserved.
func (b *Builder) InsertContext(ctx context.Context, values map[string]any) (sql.Result, error) {
	statement, args, err := b.CompileInsert(values)
	if err != nil {
		return nil, err
	}
	return b.exec(ctx, "insert", statement, args)
}

// CompileInsert returns INSERT SQL and bindings in column order without executing.
// A nil value inserts NULL. Select, Where, OrderBy, Limit and Offset are rejected.
func (b *Builder) CompileInsert(values map[string]any) (string, []any, error) {
	if b.err != nil {
		return "", nil, b.err
	}
	if len(b.columns) > 0 || len(b.conditions) > 0 || len(b.orders) > 0 || len(b.joins) > 0 || b.limit != nil || b.offset != nil {
		return "", nil, fmt.Errorf("query: insert does not accept select, where, join, order, limit or offset clauses")
	}
	if len(values) == 0 {
		return "", nil, fmt.Errorf("query: insert requires values")
	}
	c, err := newCompiler(b.dialect)
	if err != nil {
		return "", nil, err
	}
	table, err := c.identifier(b.table, false)
	if err != nil {
		return "", nil, err
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	columns := make([]string, len(names))
	placeholders := make([]string, len(names))
	for i, name := range names {
		if columns[i], err = c.identifier(name, false); err != nil {
			return "", nil, err
		}
		placeholders[i] = c.bind(values[name])
	}
	statement := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	return statement, c.args, nil
}
