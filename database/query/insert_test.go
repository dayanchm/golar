package query_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/query"
)

func TestCompileInsert(t *testing.T) {
	values := map[string]any{"name": "Dayanch", "email": "user@example.com", "age": 29}
	cases := map[string]string{
		"sqlite":   `INSERT INTO "users" ("age", "email", "name") VALUES (?, ?, ?)`,
		"postgres": `INSERT INTO "users" ("age", "email", "name") VALUES ($1, $2, $3)`,
		"mysql":    "INSERT INTO `users` (`age`, `email`, `name`) VALUES (?, ?, ?)",
	}
	for dialect, expected := range cases {
		for range 20 { // map iteration order must not leak into SQL
			sql, args, err := query.New(nil, dialect).Table("users").CompileInsert(values)
			if err != nil || sql != expected || !reflect.DeepEqual(args, []any{29, "user@example.com", "Dayanch"}) {
				t.Fatalf("%s: %q %v %v", dialect, sql, args, err)
			}
		}
	}
	sql, args, err := query.New(nil, "sqlite").Table("users").CompileInsert(map[string]any{"email": nil})
	if err != nil || sql != `INSERT INTO "users" ("email") VALUES (?)` || !reflect.DeepEqual(args, []any{nil}) {
		t.Fatalf("single column: %q %v %v", sql, args, err)
	}
	attack := "name'); DROP TABLE users; --"
	sql, args, err = query.New(nil, "sqlite").Table("users").CompileInsert(map[string]any{"name": attack})
	if err != nil || strings.Contains(sql, "DROP") || !reflect.DeepEqual(args, []any{attack}) {
		t.Fatalf("injection: %q %v %v", sql, args, err)
	}
}

func TestCompileInsertValidation(t *testing.T) {
	base := query.New(nil, "sqlite").Table("users")
	invalid := []struct {
		q      *query.Builder
		values map[string]any
	}{
		{base, nil},
		{base, map[string]any{}},
		{base.Table(""), map[string]any{"a": 1}},
		{base, map[string]any{"": 1}},
		{base, map[string]any{"name) VALUES (1); --": 1}},
		{base, map[string]any{"*": 1}},
		{query.New(nil, "oracle").Table("users"), map[string]any{"a": 1}},
		{base.Where("id", "=", 1), map[string]any{"a": 1}},
		{base.Select("id"), map[string]any{"a": 1}},
		{base.Limit(1), map[string]any{"a": 1}},
		{base.Select(), map[string]any{"a": 1}},
	}
	for i, c := range invalid {
		if sql, args, err := c.q.CompileInsert(c.values); err == nil || sql != "" || args != nil {
			t.Fatalf("case %d: %q %v %v", i, sql, args, err)
		}
		if _, err := c.q.Insert(c.values); err == nil {
			t.Fatalf("case %d: expected insert error", i)
		}
	}
}

func TestSQLiteInsert(t *testing.T) {
	db, err := database.Connect(database.Config{Driver: "sqlite", Database: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = db.ExecContext(ctx, "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT UNIQUE)"); err != nil {
		t.Fatal(err)
	}
	attack := "name'); DROP TABLE users; --"
	result, err := db.Table("users").Insert(map[string]any{"name": attack, "email": "user@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatalf("rows affected: %d %v", n, err)
	}
	if id, err := result.LastInsertId(); err != nil || id != 1 {
		t.Fatalf("last insert id: %d %v", id, err)
	}
	err = db.TransactionContext(ctx, func(tx *database.Tx) error {
		_, err := tx.Table("users").InsertContext(ctx, map[string]any{"name": "Tx", "email": nil})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := db.Table("users").Select("name").WhereNull("email").QueryContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil || !reflect.DeepEqual(names, []string{"Tx"}) {
		t.Fatalf("null rows: %v %v", names, err)
	}
	var stored string
	if err := db.QueryRowContext(ctx, "SELECT name FROM users WHERE id = 1").Scan(&stored); err != nil || stored != attack {
		t.Fatalf("stored: %q %v", stored, err)
	}
	if _, err := db.Table("users").Insert(map[string]any{"name": "Dup", "email": "user@example.com"}); err == nil || !strings.HasPrefix(err.Error(), "query: insert:") {
		t.Fatalf("expected constraint error: %v", err)
	}
	if _, err := db.Table("missing").Insert(map[string]any{"name": "x"}); err == nil {
		t.Fatal("expected missing table error")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.Table("users").InsertContext(canceled, map[string]any{"name": "x"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := query.New(nil, "sqlite").Table("users").Insert(map[string]any{"name": "x"}); err == nil {
		t.Fatal("expected missing connection error")
	}
}
