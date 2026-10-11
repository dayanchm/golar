package schema

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func needsRebuild(b *Blueprint) bool {
	if len(b.ForeignKeys) > 0 || len(b.DroppedForeignKeys) > 0 {
		return true
	}
	for _, column := range b.Columns {
		_, expressionDefault := column.DefaultValue.(Expression)
		if column.Changing || column.IsUnique || column.IsPrimary || column.HasDefault && expressionDefault {
			return true
		}
	}
	for _, index := range b.Indexes {
		if index.Primary {
			return true
		}
	}
	return false
}

// splitDefinitions separates SQL definitions, preserving quoted strings and nested expressions.
func splitDefinitions(body string) ([]string, error) {
	var parts []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(body); i++ {
		ch := body[i]
		if quote != 0 {
			if ch == quote {
				if i+1 < len(body) && body[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '[':
			quote = ']'
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, strings.TrimSpace(body[start:i]))
				start = i + 1
			}
		}
		if depth < 0 {
			return nil, fmt.Errorf("schema: invalid CREATE SQL")
		}
	}
	if depth != 0 || quote != 0 {
		return nil, fmt.Errorf("schema: unbalanced CREATE SQL")
	}
	parts = append(parts, strings.TrimSpace(body[start:]))
	return parts, nil
}
func definitionName(sql string) string {
	sql = strings.TrimSpace(sql)
	if sql == "" {
		return ""
	}
	if sql[0] == '"' || sql[0] == '`' || sql[0] == '[' {
		end := sql[0]
		if end == '[' {
			end = ']'
		}
		var name strings.Builder
		for i := 1; i < len(sql); i++ {
			if sql[i] == end {
				if i+1 < len(sql) && sql[i+1] == end {
					name.WriteByte(end)
					i++
					continue
				}
				return name.String()
			}
			name.WriteByte(sql[i])
		}
		return ""
	}
	name, _, _ := strings.Cut(sql, " ")
	return name
}
func (s *Schema) rebuild(b *Blueprint) (err error) {
	if s.pretend != nil {
		return fmt.Errorf("schema: SQLite rebuild needs existing table metadata; cannot pretend offline")
	}
	if err = s.compiler.name(b.Table); err != nil {
		return err
	}
	executor := s.executor
	var conn *sql.Conn
	if db, ok := executor.(*sql.DB); ok {
		conn, err = db.Conn(s.ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		executor = conn
	}
	var foreignKeys int
	if err = executor.QueryRowContext(s.ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return err
	}
	if foreignKeys != 0 {
		if _, err = executor.ExecContext(s.ctx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		var enabled int
		_ = executor.QueryRowContext(s.ctx, "PRAGMA foreign_keys").Scan(&enabled)
		if enabled != 0 {
			return fmt.Errorf("schema: SQLite rebuild must begin outside a foreign-key-enabled transaction")
		}
		defer func() {
			_, e := executor.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
			err = errors.Join(err, e)
		}()
	}
	if _, err = executor.ExecContext(s.ctx, "SAVEPOINT golar_schema_rebuild"); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_, e := executor.ExecContext(context.Background(), "ROLLBACK TO golar_schema_rebuild")
			err = errors.Join(err, e)
		}
		_, e := executor.ExecContext(context.Background(), "RELEASE golar_schema_rebuild")
		err = errors.Join(err, e)
	}()
	var previousSequence sql.NullInt64
	var sequenceExists int
	if e := executor.QueryRowContext(s.ctx, "SELECT COUNT(*) FROM sqlite_master WHERE name='sqlite_sequence'").Scan(&sequenceExists); e != nil {
		return e
	}
	if sequenceExists > 0 {
		e := executor.QueryRowContext(s.ctx, "SELECT seq FROM sqlite_sequence WHERE name=?", b.Table).Scan(&previousSequence)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
	}
	var original string
	if err = executor.QueryRowContext(s.ctx, "SELECT sql FROM sqlite_master WHERE type='table' AND name=?", b.Table).Scan(&original); err != nil {
		return err
	}
	left, right := strings.Index(original, "("), strings.LastIndex(original, ")")
	if left < 0 || right <= left {
		return fmt.Errorf("schema: invalid table definition")
	}
	parts, e := splitDefinitions(original[left+1 : right])
	if e != nil {
		return e
	}
	for _, column := range b.Columns {
		definition, e := s.compiler.column(column)
		if e != nil {
			return e
		}
		if column.Changing {
			found := false
			for i, part := range parts {
				if definitionName(part) == column.Name {
					parts[i] = definition
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("schema: column %q not found", column.Name)
			}
		} else {
			parts = append(parts, definition)
		}
	}
	for _, name := range b.DroppedForeignKeys {
		found := false
		for i, part := range parts {
			if strings.HasPrefix(strings.ToUpper(part), "CONSTRAINT ") && definitionName(strings.TrimSpace(part[len("CONSTRAINT "):])) == name {
				parts = append(parts[:i], parts[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("schema: foreign key %q not found", name)
		}
	}
	for _, f := range b.ForeignKeys {
		definition, e := s.compiler.foreign(b.Table, f)
		if e != nil {
			return e
		}
		parts = append(parts, definition)
	}
	for _, index := range b.Indexes {
		if index.Primary {
			columns, e := s.compiler.columns(index.Columns)
			if e != nil {
				return e
			}
			parts = append(parts, "PRIMARY KEY ("+columns+")")
		}
	}
	rows, e := executor.QueryContext(s.ctx, "SELECT sql FROM sqlite_master WHERE tbl_name=? AND type IN ('index','trigger') AND sql IS NOT NULL", b.Table)
	if e != nil {
		return e
	}
	var dependent []string
	for rows.Next() {
		var query string
		if e = rows.Scan(&query); e != nil {
			break
		}
		dependent = append(dependent, query)
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return e
	}
	rows, e = executor.QueryContext(s.ctx, "PRAGMA table_xinfo("+s.compiler.Quote(b.Table)+")")
	if e != nil {
		return e
	}
	var columns []string
	for rows.Next() {
		var id, notNull, pk, hidden int
		var name, kind string
		var def any
		if e = rows.Scan(&id, &name, &kind, &notNull, &def, &pk, &hidden); e != nil {
			break
		}
		if hidden == 0 {
			columns = append(columns, name)
		}
	}
	e = errors.Join(e, rows.Err(), rows.Close())
	if e != nil {
		return e
	}
	temp := "_golar_rebuild_" + b.Table
	query := "CREATE TABLE " + s.compiler.Quote(temp) + " (" + strings.Join(parts, ", ") + ")" + original[right+1:]
	names, e := s.compiler.columns(columns)
	if e != nil {
		return e
	}
	for _, query := range []string{query, "INSERT INTO " + s.compiler.Quote(temp) + " (" + names + ") SELECT " + names + " FROM " + s.compiler.Quote(b.Table), "DROP TABLE " + s.compiler.Quote(b.Table), "ALTER TABLE " + s.compiler.Quote(temp) + " RENAME TO " + s.compiler.Quote(b.Table)} {
		if _, e = executor.ExecContext(s.ctx, query); e != nil {
			return fmt.Errorf("schema: rebuild: %w", e)
		}
	}
	if previousSequence.Valid {
		if _, e = executor.ExecContext(s.ctx, "UPDATE sqlite_sequence SET seq=MAX(seq,?) WHERE name=?", previousSequence.Int64, b.Table); e != nil {
			return e
		}
	}
	for _, query := range dependent {
		if _, e = executor.ExecContext(s.ctx, query); e != nil {
			return e
		}
	}
	copySchema := *s
	copySchema.executor = executor
	rest := *b
	rest.Columns = nil
	rest.ForeignKeys = nil
	rest.DroppedForeignKeys = nil
	rest.Indexes = nil
	for _, index := range b.Indexes {
		if !index.Primary {
			rest.Indexes = append(rest.Indexes, index)
		}
	}
	statements, e := s.compiler.CompileAlter(&rest)
	if e != nil {
		return e
	}
	if e = copySchema.run(statements); e != nil {
		return e
	}
	check, e := executor.QueryContext(s.ctx, "PRAGMA foreign_key_check")
	if e != nil {
		return e
	}
	violation := check.Next()
	e = errors.Join(check.Err(), check.Close())
	if e != nil {
		return e
	}
	if violation {
		return fmt.Errorf("schema: foreign key violation after rebuild")
	}
	return nil
}
