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

func TestCompileUpdate(t *testing.T) {
	values := map[string]any{"name": "New", "age": 30, "email": nil}
	cases := map[string]string{
		"sqlite":   `UPDATE "users" SET "age" = ?, "email" = ?, "name" = ? WHERE "id" = ? AND "deleted_at" IS NULL`,
		"postgres": `UPDATE "users" SET "age" = $1, "email" = $2, "name" = $3 WHERE "id" = $4 AND "deleted_at" IS NULL`,
		"mysql":    "UPDATE `users` SET `age` = ?, `email` = ?, `name` = ? WHERE `id` = ? AND `deleted_at` IS NULL",
	}
	for dialect, expected := range cases {
		for range 20 {
			sql, args, err := query.New(nil, dialect).Table("users").Where("id", "=", 7).WhereNull("deleted_at").CompileUpdate(values)
			if err != nil || sql != expected || !reflect.DeepEqual(args, []any{30, nil, "New", 7}) {
				t.Fatalf("%s: %q %v %v", dialect, sql, args, err)
			}
		}
	}
	sql, args, err := query.New(nil, "sqlite").Table("users").AllRows().CompileUpdate(map[string]any{"active": false})
	if err != nil || sql != `UPDATE "users" SET "active" = ?` || !reflect.DeepEqual(args, []any{false}) {
		t.Fatalf("all rows: %q %v %v", sql, args, err)
	}
}

func TestCompileDelete(t *testing.T) {
	cases := map[string]string{
		"sqlite":   `DELETE FROM "users" WHERE "age" < ? AND "name" LIKE ?`,
		"postgres": `DELETE FROM "users" WHERE "age" < $1 AND "name" LIKE $2`,
		"mysql":    "DELETE FROM `users` WHERE `age` < ? AND `name` LIKE ?",
	}
	for dialect, expected := range cases {
		sql, args, err := query.New(nil, dialect).Table("users").Where("age", "<", 18).Where("name", "like", "x' OR 1=1 --").CompileDelete()
		if err != nil || sql != expected || !reflect.DeepEqual(args, []any{18, "x' OR 1=1 --"}) {
			t.Fatalf("%s: %q %v %v", dialect, sql, args, err)
		}
	}
	sql, args, err := query.New(nil, "mysql").Table("users").AllRows().CompileDelete()
	if err != nil || sql != "DELETE FROM `users`" || len(args) != 0 {
		t.Fatalf("all rows: %q %v %v", sql, args, err)
	}
}

func TestWriteValidation(t *testing.T) {
	base := query.New(nil, "sqlite").Table("users")
	where := base.Where("id", "=", 1)
	values := map[string]any{"name": "x"}
	invalid := []*query.Builder{
		base, // no where and no AllRows
		where.Select("id"), where.OrderBy("id", "asc"), where.Limit(1), where.Offset(1),
		where.Join("posts", "posts.user_id", "=", "users.id"),
		where.Table(""), where.Table("users; --"), base.Where("id", "=", nil), base.Where("id", "OR", 1),
		query.New(nil, "oracle").Table("users").AllRows(), base.Select(),
	}
	for i, q := range invalid {
		if sql, args, err := q.CompileUpdate(values); err == nil || sql != "" || args != nil {
			t.Fatalf("update case %d: %q %v %v", i, sql, args, err)
		}
		if sql, args, err := q.CompileDelete(); err == nil || sql != "" || args != nil {
			t.Fatalf("delete case %d: %q %v %v", i, sql, args, err)
		}
		if _, err := q.Update(values); err == nil {
			t.Fatalf("update exec case %d: expected error", i)
		}
		if _, err := q.Delete(); err == nil {
			t.Fatalf("delete exec case %d: expected error", i)
		}
	}
	for i, v := range []map[string]any{nil, {}, {"": 1}, {"name = 1; --": 1}} {
		if _, _, err := where.CompileUpdate(v); err == nil {
			t.Fatalf("values case %d: expected error", i)
		}
	}
	if !strings.Contains(fmtErr(base.CompileDelete()), "AllRows") {
		t.Fatal("error should explain AllRows")
	}
	if _, err := where.Delete(); err == nil || !strings.Contains(err.Error(), "connection is required") {
		t.Fatalf("missing connection: %v", err)
	}
}

func fmtErr(_ string, _ []any, err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestUpdateDeleteExecution(t *testing.T) {
	db, _ := connect(t)
	ctx := context.Background()
	seed(t, db, "Ada", "Bob", "Cem", "Dan")
	attack := "x'; DROP TABLE query_users; --"
	result, err := db.Table("query_users").Where("age", ">=", 30).Update(map[string]any{"name": attack, "email": nil})
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 2 {
		t.Fatalf("update rows: %d %v", n, err)
	}
	if got := names(t, db); !reflect.DeepEqual(got, []string{"Ada", "Bob", attack, attack}) {
		t.Fatalf("after update: %v", got)
	}
	err = db.TransactionContext(ctx, func(tx *database.Tx) error {
		_, err := tx.Table("query_users").Where("name", "=", attack).DeleteContext(ctx)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := names(t, db); !reflect.DeepEqual(got, []string{"Ada", "Bob"}) {
		t.Fatalf("after delete: %v", got)
	}
	if _, err := db.Table("query_users").Delete(); err == nil {
		t.Fatal("unconstrained delete must fail")
	}
	if got := names(t, db); len(got) != 2 {
		t.Fatalf("rows must survive rejected delete: %v", got)
	}
	result, err = db.Table("query_users").AllRows().Delete()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 2 {
		t.Fatalf("delete all rows: %d %v", n, err)
	}
	if _, err := db.Table("query_missing").Where("id", "=", 1).Update(map[string]any{"a": 1}); err == nil || !strings.HasPrefix(err.Error(), "query: update:") {
		t.Fatalf("update driver error: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.Table("query_users").Where("id", "=", 1).DeleteContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
