package resource

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/dayanchm/golar/database/query"
)

type user struct {
	ID           int64
	Name         string
	Email        string
	PasswordHash string // must never appear in output
	Admin        bool
	Team         *team
}

type team struct{ Name string }

type viewerKey struct{}

func userResource(ctx context.Context, u user) Object {
	admin, _ := ctx.Value(viewerKey{}).(bool)
	return Object{
		"id":    u.ID,
		"name":  u.Name,
		"email": When(admin, u.Email),
		"team":  WhenNotNil(u.Team),
		"stats": WhenFunc(admin, func() any { return Object{"posts": 3, "hidden": Missing} }),
	}.MergeWhen(u.Admin, Object{"role": "admin"})
}

// sameJSON compares two JSON documents by value, ignoring escaping.
func sameJSON(t *testing.T, a, b string) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal([]byte(a), &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(b), &y); err != nil {
		t.Fatal(err)
	}
	return encode(t, x) == encode(t, y)
}

func encode(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func TestItemAndConditionalFields(t *testing.T) {
	ada := user{ID: 1, Name: "Ada", Email: "ada@example.com", PasswordHash: "secret", Admin: true, Team: &team{"Core"}}
	got := encode(t, Item(context.Background(), ada, userResource))
	if want := `{"data":{"id":1,"name":"Ada","role":"admin","team":{"Name":"Core"}}}`; got != want {
		t.Errorf("guest view = %s, want %s", got, want)
	}
	admin := context.WithValue(context.Background(), viewerKey{}, true)
	got = encode(t, Item(admin, user{ID: 2, Name: "Eve", Email: "eve@example.com"}, userResource))
	if want := `{"data":{"email":"eve@example.com","id":2,"name":"Eve","stats":{"posts":3}}}`; got != want {
		t.Errorf("admin view = %s, want %s", got, want)
	}
	called := false
	WhenFunc(false, func() any { called = true; return nil })
	if called {
		t.Error("WhenFunc built a value for a false condition")
	}
}

func TestCollection(t *testing.T) {
	if got := encode(t, Collection(context.Background(), nil, userResource)); got != `{"data":[]}` {
		t.Errorf("empty collection = %s", got)
	}
	users := []user{{ID: 1, Name: "Ada"}, {ID: 2, Name: "Eve"}}
	doc := Collection(context.Background(), users, userResource)
	doc.Extra = Object{"version": "1", "data": "ignored"}
	if got, want := encode(t, doc), `{"data":[{"id":1,"name":"Ada"},{"id":2,"name":"Eve"}],"version":"1"}`; got != want {
		t.Errorf("collection = %s, want %s", got, want)
	}
}

func TestPaginated(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/users?sort=name&page=2", nil)
	users := []user{{ID: 16, Name: "P"}, {ID: 17, Name: "Q"}}
	got := encode(t, Paginated(context.Background(), users, query.NewPagination(2, 15, 32), userResource, PageURL(r, "")))
	want := `{"data":[{"id":16,"name":"P"},{"id":17,"name":"Q"}],` +
		`"links":{"first":"/api/users?page=1&sort=name","last":"/api/users?page=3&sort=name","next":"/api/users?page=3&sort=name","prev":"/api/users?page=1&sort=name"},` +
		`"meta":{"current_page":2,"from":16,"last_page":3,"per_page":15,"to":17,"total":32}}`
	if !sameJSON(t, got, want) {
		t.Errorf("page 2 =\n%s\nwant\n%s", got, want)
	}

	var first struct {
		Meta  map[string]any `json:"meta"`
		Links map[string]any `json:"links"`
	}
	json.Unmarshal([]byte(encode(t, Paginated(context.Background(), users, query.NewPagination(1, 2, 2), userResource, PageURL(r, "p")))), &first)
	if first.Links["prev"] != nil || first.Links["next"] != nil || first.Links["first"] != "/api/users?p=1&page=2&sort=name" || first.Meta["to"] != float64(2) {
		t.Errorf("only page = %+v", first)
	}

	empty := encode(t, Paginated(context.Background(), []user{}, query.NewPagination(5, 10, 0), userResource, nil))
	if want := `{"data":[],"meta":{"current_page":5,"from":null,"last_page":1,"per_page":10,"to":null,"total":0}}`; empty != want {
		t.Errorf("empty page = %s, want %s", empty, want)
	}
	beyond := Paginated(context.Background(), []user{}, query.NewPagination(9, 10, 25), userResource, PageURL(r, ""))
	if beyond.Links["prev"] != "/api/users?page=3&sort=name" || beyond.Links["next"] != nil {
		t.Errorf("page past the end links = %v", beyond.Links)
	}
}
