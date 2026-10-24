package query

import (
	"fmt"
	"strings"
)

// compiler quotes identifiers and collects bindings for one statement.
type compiler struct {
	*dialect
	args []any
}

func newCompiler(name string) (*compiler, error) {
	d, ok := dialects[name]
	if !ok {
		return nil, fmt.Errorf("query: unsupported dialect %q", name)
	}
	return &compiler{dialect: d}, nil
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
		if c.maxIdentifier > 0 && len(part) > c.maxIdentifier {
			return "", fmt.Errorf("query: identifier too long %q", name)
		}
		parts[i] = c.quote + part + c.quote
	}
	return strings.Join(parts, "."), nil
}

func (c *compiler) bind(value any) string {
	c.args = append(c.args, value)
	return c.placeholder(len(c.args))
}

func comparison(operator string) bool {
	switch operator {
	case "=", "!=", "<>", "<", "<=", ">", ">=":
		return true
	}
	return false
}

// from compiles the table and joins.
func (c *compiler) from(b *Builder) (string, error) {
	table, err := c.identifier(b.table, false)
	if err != nil {
		return "", err
	}
	for _, j := range b.joins {
		target, err := c.identifier(j.table, false)
		if err != nil {
			return "", err
		}
		first, err := c.identifier(j.first, false)
		if err != nil {
			return "", err
		}
		second, err := c.identifier(j.second, false)
		if err != nil {
			return "", err
		}
		if !comparison(j.operator) {
			return "", fmt.Errorf("query: invalid join operator %q", j.operator)
		}
		table += " " + j.kind + " JOIN " + target + " ON " + first + " " + j.operator + " " + second
	}
	return table, nil
}

// where compiles AND-connected predicates, or "" when there are none.
func (c *compiler) where(conditions []condition) (string, error) {
	predicates := make([]string, 0, len(conditions))
	for _, condition := range conditions {
		column, err := c.identifier(condition.column, false)
		if err != nil {
			return "", err
		}
		if condition.null {
			predicates = append(predicates, column+" "+condition.operator)
			continue
		}
		if condition.operator == "IN" || condition.operator == "NOT IN" {
			if len(condition.values) == 0 {
				// Empty lists are invalid SQL; use constant predicates with the same meaning.
				predicates = append(predicates, map[string]string{"IN": "1 = 0", "NOT IN": "1 = 1"}[condition.operator])
				continue
			}
			placeholders := make([]string, len(condition.values))
			for i, value := range condition.values {
				if value == nil {
					return "", fmt.Errorf("query: %s list must not contain NULL", condition.operator)
				}
				placeholders[i] = c.bind(value)
			}
			predicates = append(predicates, column+" "+condition.operator+" ("+strings.Join(placeholders, ", ")+")")
			continue
		}
		if !comparison(condition.operator) && condition.operator != "LIKE" && condition.operator != "NOT LIKE" {
			return "", fmt.Errorf("query: invalid operator %q", condition.operator)
		}
		if condition.value == nil {
			return "", fmt.Errorf("query: NULL requires WhereNull or WhereNotNull")
		}
		predicates = append(predicates, column+" "+condition.operator+" "+c.bind(condition.value))
	}
	if len(predicates) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(predicates, " AND "), nil
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
	from, err := c.from(b)
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
	where, err := c.where(b.conditions)
	if err != nil {
		return "", nil, err
	}
	statement := "SELECT " + strings.Join(columns, ", ") + " FROM " + from + where
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
	if b.limit != nil {
		statement += " LIMIT " + c.bind(*b.limit)
	} else if b.offset != nil {
		// SQLite and MySQL only accept OFFSET after LIMIT; use their "no limit" forms.
		statement += c.offsetOnly
	}
	if b.offset != nil {
		statement += " OFFSET " + c.bind(*b.offset)
	}
	return statement, c.args, nil
}

// ToSQL is an alias for Compile.
func (b *Builder) ToSQL() (string, []any, error) { return b.Compile() }
