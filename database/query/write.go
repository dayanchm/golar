package query

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// AllRows allows Update and Delete without a Where clause. Without it, an
// unconstrained write is rejected so a forgotten Where cannot touch every row.
func (b *Builder) AllRows() *Builder { c := b.clone(); c.allRows = true; return c }

// writable validates the clauses UPDATE and DELETE accept.
func (b *Builder) writable(operation string) error {
	if b.err != nil {
		return b.err
	}
	if len(b.columns) > 0 || len(b.orders) > 0 || len(b.joins) > 0 || b.limit != nil || b.offset != nil {
		return fmt.Errorf("query: %s does not accept select, join, order, limit or offset clauses", operation)
	}
	if len(b.conditions) == 0 && !b.allRows {
		return fmt.Errorf("query: %s requires a where clause; call AllRows to affect every row", operation)
	}
	return nil
}

func (b *Builder) exec(ctx context.Context, operation, statement string, args []any) (sql.Result, error) {
	if b.runner == nil {
		return nil, fmt.Errorf("query: connection is required")
	}
	result, err := b.runner.ExecContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query: %s: %w", operation, err)
	}
	return result, nil
}

// Update updates matching rows using context.Background. See UpdateContext.
func (b *Builder) Update(values map[string]any) (sql.Result, error) {
	return b.UpdateContext(context.Background(), values)
}

// UpdateContext updates the rows matched by Where. Columns are sorted so SQL
// and bindings are deterministic; a nil value sets NULL.
func (b *Builder) UpdateContext(ctx context.Context, values map[string]any) (sql.Result, error) {
	statement, args, err := b.CompileUpdate(values)
	if err != nil {
		return nil, err
	}
	return b.exec(ctx, "update", statement, args)
}

// CompileUpdate returns UPDATE SQL with SET bindings followed by WHERE bindings.
func (b *Builder) CompileUpdate(values map[string]any) (string, []any, error) {
	if err := b.writable("update"); err != nil {
		return "", nil, err
	}
	if len(values) == 0 {
		return "", nil, fmt.Errorf("query: update requires values")
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
	assignments := make([]string, len(names))
	for i, name := range names {
		column, err := c.identifier(name, false)
		if err != nil {
			return "", nil, err
		}
		assignments[i] = column + " = " + c.bind(values[name])
	}
	where, err := c.where(b.conditions)
	if err != nil {
		return "", nil, err
	}
	return "UPDATE " + table + " SET " + strings.Join(assignments, ", ") + where, c.args, nil
}

// Delete deletes matching rows using context.Background. See DeleteContext.
func (b *Builder) Delete() (sql.Result, error) { return b.DeleteContext(context.Background()) }

// DeleteContext deletes the rows matched by Where.
func (b *Builder) DeleteContext(ctx context.Context) (sql.Result, error) {
	statement, args, err := b.CompileDelete()
	if err != nil {
		return nil, err
	}
	return b.exec(ctx, "delete", statement, args)
}

// CompileDelete returns DELETE SQL and WHERE bindings without executing.
func (b *Builder) CompileDelete() (string, []any, error) {
	if err := b.writable("delete"); err != nil {
		return "", nil, err
	}
	c, err := newCompiler(b.dialect)
	if err != nil {
		return "", nil, err
	}
	table, err := c.identifier(b.table, false)
	if err != nil {
		return "", nil, err
	}
	where, err := c.where(b.conditions)
	if err != nil {
		return "", nil, err
	}
	return "DELETE FROM " + table + where, c.args, nil
}
