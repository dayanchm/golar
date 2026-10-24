package query

import (
	"fmt"
	"strings"
)

// compiler quotes identifiers and collects bindings for one statement.
type compiler struct {
	dialect, quote string
	args           []any
}

func newCompiler(dialect string) (*compiler, error) {
	switch dialect {
	case "sqlite", "postgres":
		return &compiler{dialect: dialect, quote: `"`}, nil
	case "mysql":
		return &compiler{dialect: dialect, quote: "`"}, nil
	default:
		return nil, fmt.Errorf("query: unsupported dialect %q", dialect)
	}
}

func (c *compiler) identifier(name string, wildcard bool) (string, error) {
	parts := strings.Split(name, ".")
	for i, part := range parts {
		if wildcard && part == "*" && i == len(parts)-1 {
			continue
		}
		if part == "" {
			return "", fmt.Errorf("query: invalid identifier %q", name)
		}
		for j, r := range part {
			if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || j > 0 && r >= '0' && r <= '9') {
				return "", fmt.Errorf("query: invalid identifier %q", name)
			}
		}
		if c.dialect == "postgres" && len(part) > 63 || c.dialect == "mysql" && len(part) > 64 {
			return "", fmt.Errorf("query: identifier too long %q", name)
		}
		parts[i] = c.quote + part + c.quote
	}
	return strings.Join(parts, "."), nil
}

func (c *compiler) bind(value any) string {
	c.args = append(c.args, value)
	if c.dialect == "postgres" {
		return fmt.Sprintf("$%d", len(c.args))
	}
	return "?"
}

// Compile returns SQL and bindings in placeholder order. It never emits partial
// SQL on validation failure and does not modify the builder.
func (b *Builder) Compile() (string, []any, error) {
	if b.err != nil {
		return "", nil, b.err
	}
	c, err := newCompiler(b.dialect)
	if err != nil {
		return "", nil, err
	}
	c.args = make([]any, 0, len(b.conditions)+2)
	table, err := c.identifier(b.table, false)
	if err != nil {
		return "", nil, err
	}
	columns := []string{"*"}
	if len(b.columns) > 0 {
		columns = make([]string, len(b.columns))
		for i, name := range b.columns {
			columns[i], err = c.identifier(name, true)
			if err != nil {
				return "", nil, err
			}
		}
	}
	statement := "SELECT " + strings.Join(columns, ", ") + " FROM " + table
	predicates := make([]string, 0, len(b.conditions))
	for _, condition := range b.conditions {
		column, err := c.identifier(condition.column, false)
		if err != nil {
			return "", nil, err
		}
		if condition.null {
			predicates = append(predicates, column+" "+condition.operator)
			continue
		}
		switch condition.operator {
		case "=", "!=", "<>", "<", "<=", ">", ">=", "LIKE", "NOT LIKE":
		default:
			return "", nil, fmt.Errorf("query: invalid operator %q", condition.operator)
		}
		if condition.value == nil {
			return "", nil, fmt.Errorf("query: NULL requires WhereNull or WhereNotNull")
		}
		predicates = append(predicates, column+" "+condition.operator+" "+c.bind(condition.value))
	}
	if len(predicates) > 0 {
		statement += " WHERE " + strings.Join(predicates, " AND ")
	}
	orders := make([]string, 0, len(b.orders))
	for _, order := range b.orders {
		column, err := c.identifier(order.column, false)
		if err != nil {
			return "", nil, err
		}
		if order.direction != "ASC" && order.direction != "DESC" {
			return "", nil, fmt.Errorf("query: invalid order direction %q", order.direction)
		}
		orders = append(orders, column+" "+order.direction)
	}
	if len(orders) > 0 {
		statement += " ORDER BY " + strings.Join(orders, ", ")
	}
	if b.limit != nil && *b.limit < 0 {
		return "", nil, fmt.Errorf("query: limit must not be negative")
	}
	if b.offset != nil && *b.offset < 0 {
		return "", nil, fmt.Errorf("query: offset must not be negative")
	}
	switch {
	case b.limit != nil:
		statement += " LIMIT " + c.bind(*b.limit)
	case b.offset != nil && c.dialect == "sqlite":
		// SQLite and MySQL only accept OFFSET after LIMIT; use their "no limit" forms.
		statement += " LIMIT -1"
	case b.offset != nil && c.dialect == "mysql":
		statement += " LIMIT 18446744073709551615"
	}
	if b.offset != nil {
		statement += " OFFSET " + c.bind(*b.offset)
	}
	return statement, c.args, nil
}

// ToSQL is an alias for Compile.
func (b *Builder) ToSQL() (string, []any, error) { return b.Compile() }
