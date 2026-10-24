// Package query builds parameterized SELECT, INSERT, UPDATE and DELETE queries
// for SQLite, PostgreSQL and MySQL.
package query

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// Querier is implemented by database/sql connections and transactions.
type Querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

type condition struct {
	column, operator string
	value            any
	values           []any // bound list for IN and NOT IN
	null             bool  // operator is IS NULL or IS NOT NULL and binds nothing
}
type ordering struct{ column, direction string }
type join struct{ kind, table, first, operator, second string }

// Builder is immutable: chaining returns a copy and never changes the receiver.
// Bound values and the connection remain caller-owned; do not mutate them concurrently.
// Validation errors are returned by Compile or QueryContext.
type Builder struct {
	runner         Querier
	dialect, table string
	columns        []string
	conditions     []condition
	orders         []ordering
	joins          []join
	limit, offset  *int
	allRows        bool
	err            error
}

// New accepts the existing driver name (sqlite, postgres or mysql).
// A nil runner permits compilation without a database.
func New(runner Querier, dialect string) *Builder { return &Builder{runner: runner, dialect: dialect} }
func (b *Builder) clone() *Builder {
	c := *b
	c.columns = slices.Clone(b.columns)
	c.conditions = slices.Clone(b.conditions)
	c.orders = slices.Clone(b.orders)
	c.joins = slices.Clone(b.joins)
	return &c
}
func (b *Builder) Table(name string) *Builder { c := b.clone(); c.table = name; return c }

// Select replaces the projection. Omit Select to select all columns.
func (b *Builder) Select(columns ...string) *Builder {
	c := b.clone()
	c.columns = slices.Clone(columns)
	if len(columns) == 0 && c.err == nil {
		c.err = fmt.Errorf("query: select requires columns")
	}
	return c
}

// Where appends an AND predicate. Use WhereNull or WhereNotNull for NULL checks.
func (b *Builder) Where(column, operator string, value any) *Builder {
	c := b.clone()
	c.conditions = append(c.conditions, condition{column: column, operator: strings.ToUpper(strings.TrimSpace(operator)), value: value})
	return c
}

// WhereNull appends an AND "column IS NULL" predicate.
func (b *Builder) WhereNull(column string) *Builder {
	c := b.clone()
	c.conditions = append(c.conditions, condition{column: column, operator: "IS NULL", null: true})
	return c
}

// WhereIn appends an AND "column IN (...)" predicate. An empty list matches no rows.
func (b *Builder) WhereIn(column string, values []any) *Builder {
	c := b.clone()
	c.conditions = append(c.conditions, condition{column: column, operator: "IN", values: slices.Clone(values)})
	return c
}

// WhereNotIn appends an AND "column NOT IN (...)" predicate. An empty list matches every row.
func (b *Builder) WhereNotIn(column string, values []any) *Builder {
	c := b.clone()
	c.conditions = append(c.conditions, condition{column: column, operator: "NOT IN", values: slices.Clone(values)})
	return c
}

// WhereNotNull appends an AND "column IS NOT NULL" predicate.
func (b *Builder) WhereNotNull(column string) *Builder {
	c := b.clone()
	c.conditions = append(c.conditions, condition{column: column, operator: "IS NOT NULL", null: true})
	return c
}

// Join appends an INNER JOIN comparing two columns, for example
// Join("posts", "posts.user_id", "=", "users.id"). Columns are never bound values.
func (b *Builder) Join(table, first, operator, second string) *Builder {
	return b.addJoin("INNER", table, first, operator, second)
}

// LeftJoin appends a LEFT JOIN. See Join.
func (b *Builder) LeftJoin(table, first, operator, second string) *Builder {
	return b.addJoin("LEFT", table, first, operator, second)
}

// RightJoin appends a RIGHT JOIN. See Join. SQLite supports it from version 3.39.
func (b *Builder) RightJoin(table, first, operator, second string) *Builder {
	return b.addJoin("RIGHT", table, first, operator, second)
}

func (b *Builder) addJoin(kind, table, first, operator, second string) *Builder {
	c := b.clone()
	c.joins = append(c.joins, join{kind, table, first, strings.TrimSpace(operator), second})
	return c
}

func (b *Builder) OrderBy(column, direction string) *Builder {
	c := b.clone()
	c.orders = append(c.orders, ordering{column, strings.ToUpper(strings.TrimSpace(direction))})
	return c
}

// Limit binds a non-negative row limit; zero returns no rows.
func (b *Builder) Limit(n int) *Builder { c := b.clone(); c.limit = &n; return c }

// Offset binds a non-negative number of rows to skip.
func (b *Builder) Offset(n int) *Builder { c := b.clone(); c.offset = &n; return c }

// QueryContext executes the compiled query. The caller must close and scan rows,
// and check rows.Err(). Driver and cancellation errors are preserved.
func (b *Builder) QueryContext(ctx context.Context) (*sql.Rows, error) {
	statement, args, err := b.Compile()
	if err != nil {
		return nil, err
	}
	if b.runner == nil {
		return nil, fmt.Errorf("query: connection is required")
	}
	rows, err := b.runner.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("query: select: %w", err)
	}
	return rows, nil
}
