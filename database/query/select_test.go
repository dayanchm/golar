package query_test

import (
	"context"
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"github.com/dayanchm/golar/database/query"
)

// dialectSQL rewrites SQLite-style SQL for postgres ($n) or mysql (backticks).
func dialectSQL(dialect, statement string) string {
	switch dialect {
	case "mysql":
		return strings.ReplaceAll(statement, `"`, "`")
	case "postgres":
		for n := 1; strings.Contains(statement, "?"); n++ {
			statement = strings.Replace(statement, "?", "$"+string(rune('0'+n)), 1)
		}
	}
	return statement
}

func TestCompileJoins(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		q := query.New(nil, dialect).Table("users").Select("users.name", "posts.title").
			Join("posts", "posts.user_id", "=", "users.id").
			LeftJoin("comments", "comments.post_id", "=", "posts.id").
			RightJoin("teams", "teams.id", "<>", "users.team_id").
			Where("posts.title", "like", "%go%").OrderBy("posts.id", "desc").Limit(5)
		sql, args, err := q.Compile()
		expected := dialectSQL(dialect, `SELECT "users"."name", "posts"."title" FROM "users"`+
			` INNER JOIN "posts" ON "posts"."user_id" = "users"."id"`+
			` LEFT JOIN "comments" ON "comments"."post_id" = "posts"."id"`+
			` RIGHT JOIN "teams" ON "teams"."id" <> "users"."team_id"`+
			` WHERE "posts"."title" LIKE ? ORDER BY "posts"."id" DESC LIMIT ?`)
		if err != nil || sql != expected || !reflect.DeepEqual(args, []any{"%go%", 5}) {
			t.Fatalf("%s: %q %v %v", dialect, sql, args, err)
		}
	}
	base := query.New(nil, "sqlite").Table("users")
	for i, q := range []*query.Builder{
		base.Join("", "a", "=", "b"), base.Join("posts; --", "a", "=", "b"), base.Join("posts", "a OR 1=1", "=", "b"),
		base.Join("posts", "a", "=", "'x'"), base.Join("posts", "a", "LIKE", "b"), base.LeftJoin("posts", "a", "= b OR", "c"),
	} {
		if sql, args, err := q.Compile(); err == nil || sql != "" || args != nil {
			t.Fatalf("case %d: %q %v %v", i, sql, args, err)
		}
	}
}

func TestCompileAggregate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		q := query.New(nil, dialect).Table("users").Select("name").Join("posts", "posts.user_id", "=", "users.id").
			Where("age", ">", 18).OrderBy("id", "desc").Limit(5).Offset(10)
		for function, argument := range map[string]string{"COUNT": "*", "sum": `"users"."age"`, "Avg": `"age"`, "MIN": `"age"`, "MAX": `"age"`} {
			column := strings.ReplaceAll(argument, `"`, "")
			sql, args, err := q.CompileAggregate(function, column)
			expected := dialectSQL(dialect, `SELECT `+strings.ToUpper(function)+`(`+argument+`) FROM "users" INNER JOIN "posts" ON "posts"."user_id" = "users"."id" WHERE "age" > ?`)
			if err != nil || sql != expected || !reflect.DeepEqual(args, []any{18}) {
				t.Fatalf("%s %s: %q %v %v", dialect, function, sql, args, err)
			}
		}
	}
	base := query.New(nil, "sqlite").Table("users")
	for i, c := range [][2]string{{"MEDIAN", "age"}, {"SUM", "*"}, {"COUNT", "age; --"}, {"MAX", ""}, {"COUNT(*) --", "*"}} {
		if sql, args, err := base.CompileAggregate(c[0], c[1]); err == nil || sql != "" || args != nil {
			t.Fatalf("case %d: %q %v %v", i, sql, args, err)
		}
	}
	if sql, _, err := base.CompileAggregate("count", "id"); err != nil || sql != `SELECT COUNT("id") FROM "users"` {
		t.Fatalf("count column: %q %v", sql, err)
	}
	if _, err := base.Count(context.Background()); err == nil {
		t.Fatal("expected missing connection error")
	}
}

func TestForPage(t *testing.T) {
	sql, args, err := query.New(nil, "postgres").Table("users").OrderBy("id", "asc").ForPage(3, 25).Compile()
	if err != nil || sql != `SELECT * FROM "users" ORDER BY "id" ASC LIMIT $1 OFFSET $2` || !reflect.DeepEqual(args, []any{25, 50}) {
		t.Fatalf("%q %v %v", sql, args, err)
	}
	base := query.New(nil, "sqlite").Table("users")
	for i, q := range []*query.Builder{base.ForPage(0, 10), base.ForPage(1, 0), base.ForPage(-1, 10), base.ForPage(1<<62, 1<<10)} {
		if _, _, err := q.Compile(); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
	if _, _, err := base.Select().ForPage(1, 10).Compile(); err == nil || !strings.Contains(err.Error(), "select") {
		t.Fatalf("earlier error must win: %v", err)
	}
	if p := (query.Pagination{Page: 2, LastPage: 3}); !p.HasMorePages() {
		t.Fatal("page 2 of 3 has more pages")
	}
}

func TestJoinAggregatePaginateExecution(t *testing.T) {
	db, driver := connect(t)
	ctx := context.Background()
	seed(t, db, "Ada", "Bob", "Cem", "Dan", "Eve")
	for _, post := range []struct {
		user  int
		title string
	}{{1, "Go"}, {1, "SQL"}, {2, "Go"}, {9, "Orphan"}} {
		if _, err := db.Table("query_posts").Insert(map[string]any{"user_id": post.user, "title": post.title}); err != nil {
			t.Fatal(err)
		}
	}
	scanPairs := func(rows *sql.Rows, err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []string
		for rows.Next() {
			var name, title sql.NullString
			if err := rows.Scan(&name, &title); err != nil {
				t.Fatal(err)
			}
			result = append(result, name.String+":"+title.String)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	users := db.Table("query_users").Select("query_users.name", "query_posts.title")
	on := []string{"query_posts.user_id", "=", "query_users.id"}
	inner := scanPairs(users.Join("query_posts", on[0], on[1], on[2]).OrderBy("query_posts.id", "asc").QueryContext(ctx))
	if !reflect.DeepEqual(inner, []string{"Ada:Go", "Ada:SQL", "Bob:Go"}) {
		t.Fatalf("inner: %v", inner)
	}
	left := scanPairs(users.LeftJoin("query_posts", on[0], on[1], on[2]).WhereNull("query_posts.id").OrderBy("query_users.id", "asc").QueryContext(ctx))
	if !reflect.DeepEqual(left, []string{"Cem:", "Dan:", "Eve:"}) {
		t.Fatalf("left: %v", left)
	}
	right := scanPairs(users.RightJoin("query_posts", on[0], on[1], on[2]).WhereNull("query_users.id").QueryContext(ctx))
	if !reflect.DeepEqual(right, []string{":Orphan"}) {
		t.Fatalf("right: %v", right)
	}

	adults := db.Table("query_users").Where("age", ">=", 20)
	if n, err := adults.OrderBy("id", "desc").Limit(1).Count(ctx); err != nil || n != 4 {
		t.Fatalf("count: %d %v", n, err)
	}
	if n, err := db.Table("query_users").Join("query_posts", on[0], on[1], on[2]).Where("query_posts.title", "=", "Go").Count(ctx); err != nil || n != 2 {
		t.Fatalf("join count: %d %v", n, err)
	}
	if sum, err := adults.Sum(ctx, "age"); err != nil || !sum.Valid || sum.Float64 != 140 {
		t.Fatalf("sum: %v %v", sum, err)
	}
	if avg, err := adults.Avg(ctx, "age"); err != nil || !avg.Valid || avg.Float64 != 35 {
		t.Fatalf("avg: %v %v", avg, err)
	}
	var low, high int
	if err := adults.Min(ctx, "age", &low); err != nil || low != 20 {
		t.Fatalf("min: %d %v", low, err)
	}
	if err := adults.Max(ctx, "age", &high); err != nil || high != 50 {
		t.Fatalf("max: %d %v", high, err)
	}
	none := db.Table("query_users").Where("age", ">", 1000)
	if sum, err := none.Sum(ctx, "age"); err != nil || sum.Valid {
		t.Fatalf("empty sum: %v %v", sum, err)
	}
	var missing sql.NullInt64
	if err := none.Max(ctx, "age", &missing); err != nil || missing.Valid {
		t.Fatalf("empty max: %v %v", missing, err)
	}
	if _, err := db.Table("query_missing").Count(ctx); err == nil || !strings.HasPrefix(err.Error(), "query: COUNT:") {
		t.Fatalf("count driver error: %v", err)
	}

	pageNames := func(page int) ([]string, query.Pagination) {
		t.Helper()
		rows, p, err := db.Table("query_users").Select("name").Where("age", ">=", 20).OrderBy("id", "asc").Paginate(ctx, page, 2)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				t.Fatal(err)
			}
			result = append(result, name)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result, p
	}
	for page, expected := range map[int][]string{1: {"Bob", "Cem"}, 2: {"Dan", "Eve"}, 3: nil} {
		got, p := pageNames(page)
		if !reflect.DeepEqual(got, expected) || p.Total != 4 || p.LastPage != 2 || p.PerPage != 2 || p.Page != page || p.HasMorePages() != (page < 2) {
			t.Fatalf("%s page %d: %v %+v", driver, page, got, p)
		}
	}
	if _, _, err := db.Table("query_users").Paginate(ctx, 0, 10); err == nil {
		t.Fatal("invalid page must fail")
	}
	rows, p, err := db.Table("query_users").Where("age", ">", 1000).Paginate(ctx, 1, 10)
	if err != nil {
		t.Fatal(err)
	}
	rows.Close()
	if p.Total != 0 || p.LastPage != 1 || p.HasMorePages() {
		t.Fatalf("empty pagination: %+v", p)
	}
}

func TestWhereIn(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres", "mysql"} {
		sql, args, err := query.New(nil, dialect).Table("users").Where("age", ">", 1).WhereIn("id", []any{1, 2, 3}).WhereNotIn("name", []any{"x"}).Compile()
		expected := dialectSQL(dialect, `SELECT * FROM "users" WHERE "age" > ? AND "id" IN (?, ?, ?) AND "name" NOT IN (?)`)
		if err != nil || sql != expected || !reflect.DeepEqual(args, []any{1, 1, 2, 3, "x"}) {
			t.Fatalf("%s: %q %v %v", dialect, sql, args, err)
		}
	}
	sql, args, err := query.New(nil, "sqlite").Table("users").WhereIn("id", nil).WhereNotIn("id", []any{}).Compile()
	if err != nil || sql != `SELECT * FROM "users" WHERE 1 = 0 AND 1 = 1` || len(args) != 0 {
		t.Fatalf("empty lists: %q %v %v", sql, args, err)
	}
	values := []any{1}
	q := query.New(nil, "sqlite").Table("users").WhereIn("id", values)
	values[0] = 2
	if _, args, _ := q.Compile(); !reflect.DeepEqual(args, []any{1}) {
		t.Fatalf("WhereIn must copy its list: %v", args)
	}
	for i, q := range []*query.Builder{
		query.New(nil, "sqlite").Table("users").WhereIn("id; --", []any{1}),
		query.New(nil, "sqlite").Table("users").WhereIn("id", []any{1, nil}),
	} {
		if _, _, err := q.Compile(); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestInsertGetIDAndWhereInExecution(t *testing.T) {
	db, _ := connect(t)
	ctx := context.Background()
	var ids []any
	for _, name := range []string{"Ada", "Bob", "Cem"} {
		id, err := db.Table("query_users").InsertGetID(ctx, map[string]any{"name": name}, "id")
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) > 0 && id <= ids[len(ids)-1].(int64) {
			t.Fatalf("ids must increase: %v then %d", ids, id)
		}
		ids = append(ids, id)
	}
	if n, err := db.Table("query_users").WhereIn("id", ids[:2]).Count(ctx); err != nil || n != 2 {
		t.Fatalf("where in: %d %v", n, err)
	}
	if n, err := db.Table("query_users").WhereNotIn("id", ids[:2]).Count(ctx); err != nil || n != 1 {
		t.Fatalf("where not in: %d %v", n, err)
	}
	if n, err := db.Table("query_users").WhereIn("id", nil).Count(ctx); err != nil || n != 0 {
		t.Fatalf("empty where in: %d %v", n, err)
	}
	if _, err := db.Table("query_missing").InsertGetID(ctx, map[string]any{"name": "x"}, "id"); err == nil {
		t.Fatal("expected driver error")
	}
	if _, err := db.Table("query_users").InsertGetID(ctx, map[string]any{}, "id"); err == nil {
		t.Fatal("expected validation error")
	}
}
