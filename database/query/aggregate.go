package query

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// CompileAggregate returns SELECT fn(column) SQL for COUNT, SUM, AVG, MIN or MAX.
// Use column "*" with COUNT. Joins and Where apply; Select, OrderBy, Limit and
// Offset are ignored because an aggregate always describes all matching rows.
func (b *Builder) CompileAggregate(function, column string) (string, []any, error) {
	if b.err != nil {
		return "", nil, b.err
	}
	function = strings.ToUpper(strings.TrimSpace(function))
	switch function {
	case "COUNT", "SUM", "AVG", "MIN", "MAX":
	default:
		return "", nil, fmt.Errorf("query: unsupported aggregate %q", function)
	}
	c, err := newCompiler(b.dialect)
	if err != nil {
		return "", nil, err
	}
	argument := "*"
	if column != "*" || function != "COUNT" {
		if argument, err = c.identifier(column, false); err != nil {
			return "", nil, err
		}
	}
	from, err := c.from(b)
	if err != nil {
		return "", nil, err
	}
	where, err := c.where(b.conditions)
	if err != nil {
		return "", nil, err
	}
	return "SELECT " + function + "(" + argument + ") FROM " + from + where, c.args, nil
}

// aggregate runs an aggregate query and scans its single value into dest.
func (b *Builder) aggregate(ctx context.Context, function, column string, dest any) error {
	statement, args, err := b.CompileAggregate(function, column)
	if err != nil {
		return err
	}
	if b.runner == nil {
		return fmt.Errorf("query: connection is required")
	}
	rows, err := b.runner.QueryContext(ctx, statement, args...)
	if err != nil {
		return fmt.Errorf("query: %s: %w", function, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("query: %s: %w", function, err)
		}
		return fmt.Errorf("query: %s: no result row", function)
	}
	if err := rows.Scan(dest); err != nil {
		return fmt.Errorf("query: %s: %w", function, err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("query: %s: %w", function, err)
	}
	return nil
}

// Count returns the number of matching rows.
func (b *Builder) Count(ctx context.Context) (int64, error) {
	var n int64
	err := b.aggregate(ctx, "COUNT", "*", &n)
	return n, err
}

// Sum returns the sum of column over matching rows; Valid is false when no rows match.
func (b *Builder) Sum(ctx context.Context, column string) (sql.NullFloat64, error) {
	var n sql.NullFloat64
	err := b.aggregate(ctx, "SUM", column, &n)
	return n, err
}

// Avg returns the average of column over matching rows; Valid is false when no rows match.
func (b *Builder) Avg(ctx context.Context, column string) (sql.NullFloat64, error) {
	var n sql.NullFloat64
	err := b.aggregate(ctx, "AVG", column, &n)
	return n, err
}

// Min scans the smallest value of column into dest, which must be a pointer
// accepted by sql.Rows.Scan. Use a sql.Null* type when no rows may match.
func (b *Builder) Min(ctx context.Context, column string, dest any) error {
	return b.aggregate(ctx, "MIN", column, dest)
}

// Max scans the largest value of column into dest. See Min.
func (b *Builder) Max(ctx context.Context, column string, dest any) error {
	return b.aggregate(ctx, "MAX", column, dest)
}
