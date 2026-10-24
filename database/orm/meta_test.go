package orm

import (
	"database/sql"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestNaming(t *testing.T) {
	for in, want := range map[string]string{"ID": "id", "UserID": "user_id", "HTMLBody": "html_body", "CreatedAt": "created_at", "Name2FA": "name2_fa", "name": "name"} {
		if got := snake(in); got != want {
			t.Errorf("snake(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"user": "users", "category": "categories", "day": "days", "box": "boxes", "status": "statuses", "match": "matches", "": ""} {
		if got := plural(in); got != want {
			t.Errorf("plural(%q) = %q, want %q", in, got, want)
		}
	}
}

type author struct {
	Model
	SoftDeletes
	Name     string
	Nickname sql.NullString `db:"nick"`
	Secret   string         `db:"-"`
	internal string
	Books    []book `orm:"hasMany"`
	Bio      *bio   `orm:"hasOne,foreignKey=writer_id"`
	Tags     []tag  `orm:"belongsToMany"`
}
type book struct {
	ID       int64
	AuthorID int64
	Writer   *author `orm:"belongsTo,foreignKey=author_id"`
}
type bio struct {
	Key      string `db:"key,pk"`
	WriterID int64
}
type tag struct {
	ID int
}
type category struct{ ID uint }

func (tag) TableName() string { return "labels" }

func TestMetadata(t *testing.T) {
	m, err := metaOf(reflect.TypeFor[*author]())
	if err != nil {
		t.Fatal(err)
	}
	var columns []string
	for _, f := range m.fields {
		columns = append(columns, f.column)
	}
	if m.table != "authors" || m.pk.column != "id" || !m.autoPK || m.deletedAt == nil || m.createdAt == nil || m.updatedAt == nil ||
		!reflect.DeepEqual(columns, []string{"id", "created_at", "updated_at", "deleted_at", "name", "nick"}) {
		t.Fatalf("author meta: %s %v %+v", m.table, columns, m.pk)
	}
	info, err := Describe(author{})
	if err != nil || info.Table != "authors" || info.PrimaryKey != "id" || !info.SoftDeletes || !reflect.DeepEqual(info.Relations, []string{"Bio", "Books", "Tags"}) {
		t.Fatalf("describe: %+v %v", info, err)
	}
	if info, err := Describe(&bio{}); err != nil || info.PrimaryKey != "key" || info.Table != "bios" {
		t.Fatalf("bio: %+v %v", info, err)
	}
	if info, _ := Describe(tag{}); info.Table != "labels" {
		t.Fatalf("Tabler: %+v", info)
	}
	if m, _ := metaOf(reflect.TypeFor[category]()); m.table != "categories" || !m.autoPK {
		t.Fatalf("category: %+v", m)
	}
	if err := Register(author{}, &book{}, bio{}, tag{}); err != nil {
		t.Fatal(err)
	}
	res := func(owner any, name string) *resolved {
		t.Helper()
		m, _ := metaOf(reflect.TypeOf(owner))
		r, err := m.relations[name].resolve(m)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := res(author{}, "Books"); r.local.column != "id" || r.foreign.column != "author_id" {
		t.Fatalf("hasMany defaults: %+v", r)
	}
	if r := res(author{}, "Bio"); r.foreign.column != "writer_id" {
		t.Fatalf("hasOne option: %+v", r)
	}
	if r := res(book{}, "Writer"); r.foreign.column != "author_id" || r.local.column != "id" {
		t.Fatalf("belongsTo: %+v", r)
	}
	if r := res(author{}, "Tags"); r.pivot != "author_tag" || r.pivotLocal != "author_id" || r.pivotRel != "tag_id" || r.foreign.column != "id" {
		t.Fatalf("belongsToMany defaults: %+v", r)
	}
}

type (
	noKey   struct{ Name string }
	twoKeys struct {
		A, B int `db:",pk"`
	}
	dupColumn struct {
		ID   int
		Name string
		Alt  string `db:"name"`
	}
	badOption struct {
		ID int `db:"id,primary"`
	}
	badDeleted struct {
		ID        int
		DeletedAt time.Time
	}
	badStamp struct {
		ID        int
		CreatedAt string
	}
	badRelKind struct {
		ID    int
		Books []book `orm:"hasLots"`
	}
	badRelType struct {
		ID    int
		Books *book `orm:"hasMany"`
	}
	badRelOpt struct {
		ID    int
		Books []book `orm:"hasMany,pivot=x"`
	}
	badRelKey struct {
		ID    int
		Books []book `orm:"hasMany,foreignKey=missing"`
	}
	ptrEmbed struct {
		*Model
	}
	emptyTable struct{ ID int }
)

func (emptyTable) TableName() string { return "" }

func TestMetadataErrors(t *testing.T) {
	for _, model := range []any{noKey{}, twoKeys{}, dupColumn{}, badOption{}, badDeleted{}, badStamp{}, badRelKind{}, badRelType{}, badRelOpt{}, badRelKey{}, ptrEmbed{}, emptyTable{}, 42, nil} {
		if err := Register(model); err == nil || !strings.HasPrefix(err.Error(), "orm: ") {
			t.Errorf("%T: expected orm error, got %v", model, err)
		}
	}
}

func TestMetadataConcurrent(t *testing.T) {
	type fresh struct{ ID int }
	var wg sync.WaitGroup
	metas := make([]*meta, 20)
	for i := range metas {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metas[i], _ = metaOf(reflect.TypeFor[fresh]())
		}()
	}
	wg.Wait()
	for _, m := range metas {
		if m != metas[0] {
			t.Fatal("metadata must be cached once")
		}
	}
}

func TestKeyOf(t *testing.T) {
	n := int32(7)
	for _, c := range []struct {
		in   any
		want any
		ok   bool
	}{
		{7, int64(7), true}, {int64(7), int64(7), true}, {uint8(7), int64(7), true}, {&n, int64(7), true},
		{"a", "a", true}, {[]byte("a"), "a", true}, {sql.NullInt64{Int64: 7, Valid: true}, int64(7), true},
		{sql.NullInt64{}, nil, false}, {(*int)(nil), nil, false}, {[]int{1}, nil, false},
	} {
		got, ok := keyOf(reflect.ValueOf(c.in))
		if got != c.want || ok != c.ok {
			t.Errorf("keyOf(%#v) = %#v %v", c.in, got, ok)
		}
	}
}
