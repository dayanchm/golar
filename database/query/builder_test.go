package query_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/query"
)

func TestDialects(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		t.Run(dialect, func(t *testing.T) {
			q := query.New(nil, dialect).Table("public.users").Select("users.id", "name").Where("active", "=", true).Where("name", "like", "x' OR 1=1 --").OrderBy("id", "desc").Limit(20)
			sql, args, err := q.Compile()
			expected := `SELECT "users"."id", "name" FROM "public"."users" WHERE "active" = ? AND "name" LIKE ? ORDER BY "id" DESC LIMIT ?`
			if dialect == "mysql" {
				expected = strings.ReplaceAll(expected, `"`, "`")
			}
			if dialect == "postgres" {
				for _, placeholder := range []string{"$1", "$2", "$3"} {
					expected = strings.Replace(expected, "?", placeholder, 1)
				}
			}
			if err != nil || sql != expected || !reflect.DeepEqual(args, []any{true, "x' OR 1=1 --", 20}) {
				t.Fatalf("%q %v %v", sql, args, err)
			}
		})
	}
}

func TestValidation(t *testing.T) {
	base := query.New(nil, "sqlite").Table("users")
	invalid := []*query.Builder{
		query.New(nil, "unknown").Table("users"), base.Table(""), base.Table("users; DROP TABLE users"), base.Table("a..b"),
		base.Select(), base.Select("COUNT(*)"), base.Select("name AS n"), base.Select("*.id"), base.Select("id--"),
		base.Where("id OR 1=1", "=", 1), base.Where("id", "= ? OR 1=1", 1), base.Where("id", "=", nil),
		base.OrderBy("id", "desc;--"), base.OrderBy("id;--", "asc"), base.Limit(-1),
		query.New(nil, "postgres").Table(strings.Repeat("a", 64)), query.New(nil, "mysql").Table(strings.Repeat("a", 65)),
	}
	for i, q := range invalid {
		if sql, args, err := q.Compile(); err == nil || sql != "" || args != nil {
			t.Fatalf("case %d: %q %v %v", i, sql, args, err)
		}
	}
	for _, op := range []string{"=", "!=", "<>", "<", "<=", ">", ">=", "LIKE", "NOT LIKE"} {
		if _, _, err := base.Where("id", op, 1).Compile(); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := base.Select("users.*").Limit(0).Compile(); err != nil {
		t.Fatal(err)
	}
}

func TestIsolation(t *testing.T) {
	columns := []string{"id"}
	base := query.New(nil, "postgres").Table("users").Select(columns...).Where("active", "=", true).OrderBy("id", "asc").Limit(10)
	columns[0] = "invalid;"
	original, bindings, _ := base.Compile()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			branch := base.Where("id", ">", i).OrderBy("name", "desc").Limit(i)
			_, args, err := branch.Compile()
			if err != nil || !reflect.DeepEqual(args, []any{true, i, i}) {
				t.Errorf("branch: %v %v", args, err)
			}
		}(i)
	}
	wg.Wait()
	bindings[0] = false
	sql, args, err := base.Compile()
	if err != nil || sql != original || !reflect.DeepEqual(args, []any{true, 10}) {
		t.Fatalf("base changed: %q %v %v", sql, args, err)
	}
}

func TestSQLiteExecution(t *testing.T) {
	db, err := database.Connect(database.Config{Driver: "sqlite", Database: ":memory:"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err = db.ExecContext(ctx, "CREATE TABLE users (id INTEGER, name TEXT)"); err != nil {
		t.Fatal(err)
	}
	err = db.TransactionContext(ctx, func(tx *database.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO users VALUES (?, ?)", 1, "Türkmen"); err != nil {
			return err
		}
		rows, err := tx.Table("users").Select("name").Where("id", "=", 1).Limit(1).QueryContext(ctx)
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal("missing row")
		}
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name != "Türkmen" {
			t.Fatalf("name: %q", name)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := db.Table("users").QueryContext(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	if _, err := db.Table("missing").QueryContext(ctx); err == nil {
		t.Fatal("expected database error")
	}
	if _, err := query.New(nil, "sqlite").Table("users").QueryContext(ctx); err == nil {
		t.Fatal("expected missing connection error")
	}
}
