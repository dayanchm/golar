package query

import (
	"fmt"
	"strings"
)

// Compile returns SQL and bindings in placeholder order. It never emits partial
// SQL on validation failure and does not modify the builder.
func (b *Builder) Compile() (string, []any, error) {
	if b.err != nil {
		return "", nil, b.err
	}
	quote := `"`
	switch b.dialect {
	case "sqlite", "postgres":
	case "mysql":
		quote = "`"
	default:
		return "", nil, fmt.Errorf("query: unsupported dialect %q", b.dialect)
	}
	identifier := func(name string, wildcard bool) (string, error) {
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
			if b.dialect == "postgres" && len(part) > 63 || b.dialect == "mysql" && len(part) > 64 {
				return "", fmt.Errorf("query: identifier too long %q", name)
			}
			parts[i] = quote + part + quote
		}
		return strings.Join(parts, "."), nil
	}
	table, err := identifier(b.table, false)
	if err != nil {
		return "", nil, err
	}
	columns := []string{"*"}
	if len(b.columns) > 0 {
		columns = make([]string, len(b.columns))
		for i, name := range b.columns {
			columns[i], err = identifier(name, true)
			if err != nil {
				return "", nil, err
			}
		}
	}
	args := make([]any, 0, len(b.conditions)+1)
	bind := func(value any) string {
		args = append(args, value)
		if b.dialect == "postgres" {
			return fmt.Sprintf("$%d", len(args))
		}
		return "?"
	}
	statement := "SELECT " + strings.Join(columns, ", ") + " FROM " + table
	predicates := make([]string, 0, len(b.conditions))
	for _, condition := range b.conditions {
		column, err := identifier(condition.column, false)
		if err != nil {
			return "", nil, err
		}
		switch condition.operator {
		case "=", "!=", "<>", "<", "<=", ">", ">=", "LIKE", "NOT LIKE":
		default:
			return "", nil, fmt.Errorf("query: invalid operator %q", condition.operator)
		}
		if condition.value == nil {
			return "", nil, fmt.Errorf("query: NULL requires an explicit NULL predicate")
		}
		predicates = append(predicates, column+" "+condition.operator+" "+bind(condition.value))
	}
	if len(predicates) > 0 {
		statement += " WHERE " + strings.Join(predicates, " AND ")
	}
	orders := make([]string, 0, len(b.orders))
	for _, order := range b.orders {
		column, err := identifier(order.column, false)
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
	if b.limit != nil {
		if *b.limit < 0 {
			return "", nil, fmt.Errorf("query: limit must not be negative")
		}
		statement += " LIMIT " + bind(*b.limit)
	}
	return statement, args, nil
}
