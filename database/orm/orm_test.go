package orm_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/orm"
	"github.com/dayanchm/golar/database/query"
	"github.com/dayanchm/golar/database/schema"
)

// Tables use an orm_ prefix so CI can run this package in parallel with
// others against the same database.
type User struct {
	orm.Model
	Name    string
	Email   *string
	Profile *Profile `orm:"hasOne"`
	Posts   []Post   `orm:"hasMany"`
	Roles   []Role   `orm:"belongsToMany,pivot=orm_role_user"`
}
type Profile struct {
	ID     int64
	UserID int64
	Bio    string
}
type Post struct {
	orm.Model
	orm.SoftDeletes
	UserID   int64
	Title    string
	Author   *User     `orm:"belongsTo,foreignKey=user_id"`
	Comments []Comment `orm:"hasMany"`
}
type Comment struct {
	ID     int64
	PostID int64
	Body   string
}
type Role struct {
	ID    int64
	Name  string
	Users []User `orm:"belongsToMany,pivot=orm_role_user"`
}

func (User) TableName() string    { return "orm_users" }
func (Profile) TableName() string { return "orm_profiles" }
func (Post) TableName() string    { return "orm_posts" }
func (Comment) TableName() string { return "orm_comments" }
func (Role) TableName() string    { return "orm_roles" }

// counter wraps a connection and counts statements, to prove eager loading
// issues a fixed number of queries.
type counter struct {
	db *database.DB
	n  atomic.Int64
}

func (c *counter) Table(name string) *query.Builder { return query.New(c, c.db.Driver).Table(name) }
func (c *counter) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	c.n.Add(1)
	return c.db.QueryContext(ctx, q, args...)
}
func (c *counter) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	c.n.Add(1)
	return c.db.ExecContext(ctx, q, args...)
}

var tables = []string{"orm_role_user", "orm_comments", "orm_posts", "orm_profiles", "orm_roles", "orm_users"}

func connect(t *testing.T) *database.DB {
	t.Helper()
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(t.TempDir(), "orm.db")}
	if driver != "sqlite" {
		config.Database = ""
		config.DSN = os.Getenv("GOLAR_TEST_DSN")
		if config.DSN == "" {
			t.Fatal("GOLAR_TEST_DSN is required")
		}
	}
	db, err := database.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := schema.New(db.DB, driver)
	drop := func() {
		for _, table := range tables {
			if err := s.DropIfExists(table); err != nil {
				t.Fatal(err)
			}
		}
	}
	drop()
	t.Cleanup(drop)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Create("orm_users", func(b *schema.Blueprint) {
		b.ID()
		b.String("name", 100)
		b.String("email", 100).Nullable()
		b.Timestamps()
	}))
	must(s.Create("orm_profiles", func(b *schema.Blueprint) { b.ID(); b.BigInteger("user_id"); b.String("bio", 100) }))
	must(s.Create("orm_posts", func(b *schema.Blueprint) {
		b.ID()
		b.BigInteger("user_id")
		b.String("title", 100)
		b.Timestamps()
		b.SoftDeletes()
	}))
	must(s.Create("orm_comments", func(b *schema.Blueprint) { b.ID(); b.BigInteger("post_id"); b.String("body", 100) }))
	must(s.Create("orm_roles", func(b *schema.Blueprint) { b.ID(); b.String("name", 100) }))
	must(s.Create("orm_role_user", func(b *schema.Blueprint) { b.BigInteger("role_id"); b.BigInteger("user_id") }))
	return db
}

func TestCRUD(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	email := "ada@example.com"
	ada := User{Name: "Ada", Email: &email}
	before := time.Now().Add(-time.Second)
	if err := orm.Create(ctx, db, &ada); err != nil {
		t.Fatal(err)
	}
	if ada.ID == 0 || ada.CreatedAt.Before(before) || !ada.UpdatedAt.Equal(ada.CreatedAt) {
		t.Fatalf("create must set id and timestamps: %+v", ada)
	}
	bob := User{Name: "Bob"}
	if err := orm.Create(ctx, db, &bob); err != nil || bob.ID <= ada.ID {
		t.Fatalf("second create: %+v %v", bob, err)
	}
	found, err := orm.Find[User](ctx, db, ada.ID)
	if err != nil || found.Name != "Ada" || found.Email == nil || *found.Email != email || found.CreatedAt.Sub(ada.CreatedAt).Abs() > time.Second {
		t.Fatalf("find: %+v %v", found, err)
	}
	if _, err := orm.Find[User](ctx, db, 9999); !errors.Is(err, orm.ErrNotFound) || !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing: %v", err)
	}
	found.Name = "Ada Lovelace"
	found.Email = nil
	created := found.CreatedAt
	found.CreatedAt = time.Time{} // Save must never overwrite created_at
	time.Sleep(1100 * time.Millisecond)
	if err := orm.Save(ctx, db, found); err != nil {
		t.Fatal(err)
	}
	again, err := orm.Find[User](ctx, db, ada.ID)
	if err != nil || again.Name != "Ada Lovelace" || again.Email != nil || again.CreatedAt.Sub(created).Abs() > time.Second || !again.UpdatedAt.After(created) {
		t.Fatalf("save: %+v %v", again, err)
	}
	if err := orm.Save(ctx, db, &User{Name: "x"}); !errors.Is(err, orm.ErrMissingPrimaryKey) {
		t.Fatalf("save without id: %v", err)
	}

	for _, name := range []string{"Cem", "Dan", "Eve"} {
		if err := orm.Create(ctx, db, &User{Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	users, err := orm.Query[User](db).Where("name", "!=", "Bob").OrderBy("name", "desc").Get(ctx)
	if err != nil || len(users) != 4 || users[0].Name != "Eve" || users[3].Name != "Ada Lovelace" {
		t.Fatalf("get: %+v %v", users, err)
	}
	first, err := orm.Query[User](db).OrderBy("id", "asc").First(ctx)
	if err != nil || first.ID != ada.ID {
		t.Fatalf("first: %+v %v", first, err)
	}
	if _, err := orm.Query[User](db).Where("name", "=", "nobody").First(ctx); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("first missing: %v", err)
	}
	if n, err := orm.Query[User](db).WhereNull("email").Count(ctx); err != nil || n != 5 {
		t.Fatalf("count: %d %v", n, err)
	}
	page, p, err := orm.Query[User](db).OrderBy("id", "asc").Paginate(ctx, 2, 2)
	if err != nil || len(page) != 2 || page[0].Name != "Cem" || p.Total != 5 || p.LastPage != 3 || !p.HasMorePages() {
		t.Fatalf("paginate: %+v %+v %v", page, p, err)
	}
	if _, _, err := orm.Query[User](db).Paginate(ctx, 0, 2); err == nil {
		t.Fatal("invalid page must fail")
	}

	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := db.Table("orm_users").AllRows().Update(map[string]any{"updated_at": old}); err != nil {
		t.Fatal(err)
	}
	if n, err := orm.Query[User](db).WhereIn("name", []any{"Cem", "Dan"}).Update(ctx, map[string]any{"email": "team@example.com"}); err != nil || n != 2 {
		t.Fatalf("bulk update: %d %v", n, err)
	}
	cem, _ := orm.Query[User](db).Where("name", "=", "Cem").First(ctx)
	if cem.Email == nil || *cem.Email != "team@example.com" || !cem.UpdatedAt.After(old.Add(time.Hour)) {
		t.Fatalf("bulk update must set updated_at: %+v", cem)
	}
	if _, err := orm.Query[User](db).Update(ctx, map[string]any{"name": "x"}); err == nil || !strings.Contains(err.Error(), "AllRows") {
		t.Fatalf("unconstrained bulk update must fail: %v", err)
	}
	if _, err := orm.Query[User](db).Delete(ctx); err == nil {
		t.Fatal("unconstrained bulk delete must fail")
	}
	if err := orm.Delete(ctx, db, &bob); err != nil {
		t.Fatal(err)
	}
	if _, err := orm.Find[User](ctx, db, bob.ID); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("hard delete: %v", err)
	}
	if n, err := orm.Query[User](db).Where("name", "LIKE", "%e%").Delete(ctx); err != nil || n != 3 {
		t.Fatalf("bulk delete: %d %v", n, err)
	}

	err = db.TransactionContext(ctx, func(tx *database.Tx) error {
		if err := orm.Create(ctx, tx, &User{Name: "Rolled back"}); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	if n, _ := orm.Query[User](db).Where("name", "=", "Rolled back").Count(ctx); n != 0 {
		t.Fatal("transaction must roll back ORM writes")
	}
	if err := orm.Create(ctx, db, &User{Name: strings.Repeat("x", 1000)}); err == nil && db.Driver != "sqlite" {
		t.Fatal("expected driver error for oversized value")
	}
	if err := orm.Create[User](ctx, db, nil); err == nil {
		t.Fatal("nil model must fail")
	}
	if _, err := orm.Query[User](nil).Get(ctx); err == nil {
		t.Fatal("nil connection must fail")
	}
}

func TestRelationsAndEagerLoading(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	create := func(model any) {
		t.Helper()
		var err error
		switch m := model.(type) {
		case *User:
			err = orm.Create(ctx, db, m)
		case *Profile:
			err = orm.Create(ctx, db, m)
		case *Post:
			err = orm.Create(ctx, db, m)
		case *Comment:
			err = orm.Create(ctx, db, m)
		case *Role:
			err = orm.Create(ctx, db, m)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	ada, bob, cem := &User{Name: "Ada"}, &User{Name: "Bob"}, &User{Name: "Cem"}
	create(ada)
	create(bob)
	create(cem)
	create(&Profile{UserID: ada.ID, Bio: "math"})
	create(&Profile{UserID: bob.ID, Bio: "builder"})
	p1, p2, p3 := &Post{UserID: ada.ID, Title: "Engines"}, &Post{UserID: ada.ID, Title: "Notes"}, &Post{UserID: bob.ID, Title: "Bridges"}
	create(p1)
	create(p2)
	create(p3)
	create(&Comment{PostID: p1.ID, Body: "great"})
	create(&Comment{PostID: p1.ID, Body: "agreed"})
	create(&Comment{PostID: p3.ID, Body: "solid"})
	admin, editor := &Role{Name: "admin"}, &Role{Name: "editor"}
	create(admin)
	create(editor)
	for _, pair := range [][2]int64{{admin.ID, ada.ID}, {editor.ID, ada.ID}, {editor.ID, bob.ID}} {
		if _, err := db.Table("orm_role_user").Insert(map[string]any{"role_id": pair[0], "user_id": pair[1]}); err != nil {
			t.Fatal(err)
		}
	}

	// Lazy loading onto one model.
	if err := orm.Load(ctx, db, ada, "Posts", "Profile", "Roles"); err != nil {
		t.Fatal(err)
	}
	if len(ada.Posts) != 2 || ada.Posts[0].Title != "Engines" || ada.Profile == nil || ada.Profile.Bio != "math" || len(ada.Roles) != 2 || ada.Roles[0].Name != "admin" {
		t.Fatalf("load: %+v", ada)
	}

	// Eager loading: a fixed number of queries regardless of how many users.
	c := &counter{db: db}
	users, err := orm.Query[User](c).OrderBy("id", "asc").With("Posts.Comments", "Posts.Author", "Profile", "Roles").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// users, posts, comments, post authors, profiles, roles
	if n := c.n.Load(); n != 6 {
		t.Fatalf("eager loading issued %d queries, want 6", n)
	}
	titles := func(posts []Post) (out []string) {
		for _, p := range posts {
			out = append(out, p.Title)
		}
		return out
	}
	if len(users) != 3 || !reflect.DeepEqual(titles(users[0].Posts), []string{"Engines", "Notes"}) || !reflect.DeepEqual(titles(users[1].Posts), []string{"Bridges"}) {
		t.Fatalf("posts: %+v", users)
	}
	if users[2].Posts == nil || len(users[2].Posts) != 0 || users[2].Profile != nil || len(users[2].Roles) != 0 {
		t.Fatalf("user without relations: %+v", users[2])
	}
	if len(users[0].Posts[0].Comments) != 2 || users[0].Posts[0].Comments[1].Body != "agreed" || len(users[0].Posts[1].Comments) != 0 || len(users[1].Posts[0].Comments) != 1 {
		t.Fatalf("nested comments: %+v", users[0].Posts)
	}
	if a := users[0].Posts[1].Author; a == nil || a.Name != "Ada" || users[1].Posts[0].Author.Name != "Bob" {
		t.Fatalf("belongsTo: %+v", users[0].Posts[1])
	}
	if users[1].Profile.Bio != "builder" || len(users[1].Roles) != 1 || users[1].Roles[0].Name != "editor" {
		t.Fatalf("hasOne/belongsToMany: %+v", users[1])
	}
	roles, err := orm.Query[Role](db).OrderBy("id", "asc").With("Users").Get(ctx)
	if err != nil || len(roles) != 2 || len(roles[0].Users) != 1 || len(roles[1].Users) != 2 || roles[1].Users[1].Name != "Bob" {
		t.Fatalf("inverse belongsToMany: %+v %v", roles, err)
	}
	if _, err := orm.Query[User](db).With("Missing").Get(ctx); err == nil || !strings.Contains(err.Error(), `no relation "Missing"`) {
		t.Fatalf("unknown relation: %v", err)
	}
	if _, err := orm.Query[User](db).With("Posts.Missing").Get(ctx); err == nil {
		t.Fatal("unknown nested relation must fail")
	}

	// Soft-deleted posts disappear from queries and eager loads.
	if err := orm.Delete(ctx, db, p2); err != nil {
		t.Fatal(err)
	}
	if p2.DeletedAt == nil {
		t.Fatal("delete must set DeletedAt on the model")
	}
	if err := orm.Load(ctx, db, ada, "Posts"); err != nil || !reflect.DeepEqual(titles(ada.Posts), []string{"Engines"}) {
		t.Fatalf("eager load must skip trashed: %v %v", titles(ada.Posts), err)
	}
	assertCount := func(b *orm.Builder[Post], want int64) {
		t.Helper()
		if n, err := b.Count(ctx); err != nil || n != want {
			t.Fatalf("count = %d %v, want %d", n, err, want)
		}
	}
	posts := orm.Query[Post](db)
	assertCount(posts, 2)
	assertCount(posts.WithTrashed(), 3)
	assertCount(posts.OnlyTrashed(), 1)
	if _, err := orm.Find[Post](ctx, db, p2.ID); !errors.Is(err, orm.ErrNotFound) {
		t.Fatalf("find trashed: %v", err)
	}
	trashed, err := posts.WithTrashed().Find(ctx, p2.ID)
	if err != nil || trashed.DeletedAt == nil || time.Since(*trashed.DeletedAt) > time.Minute {
		t.Fatalf("with trashed: %+v %v", trashed, err)
	}
	if err := orm.Restore(ctx, db, trashed); err != nil || trashed.DeletedAt != nil {
		t.Fatalf("restore: %v", err)
	}
	assertCount(posts, 3)
	if n, err := posts.Where("user_id", "=", ada.ID).Delete(ctx); err != nil || n != 2 {
		t.Fatalf("bulk soft delete: %d %v", n, err)
	}
	assertCount(posts.WithTrashed(), 3)
	if n, err := posts.Where("user_id", "=", ada.ID).Restore(ctx); err != nil || n != 2 {
		t.Fatalf("bulk restore: %d %v", n, err)
	}
	if err := orm.ForceDelete(ctx, db, p3); err != nil {
		t.Fatal(err)
	}
	assertCount(posts.WithTrashed(), 2)
	if n, err := posts.AllRows().ForceDelete(ctx); err != nil || n != 2 {
		t.Fatalf("force delete all: %d %v", n, err)
	}
	if err := orm.Restore(ctx, db, ada); err == nil {
		t.Fatal("restore on a model without soft deletes must fail")
	}
}
