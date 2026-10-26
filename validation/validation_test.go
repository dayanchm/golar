package validation

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/dayanchm/golar/database"
)

// failures returns the validation errors for value and fails the test on any other error.
func failures(t *testing.T, v *Validator, value any) Errors {
	t.Helper()
	err := v.Validate(value)
	if err == nil {
		return nil
	}
	var verr *Error
	if !errors.As(err, &verr) {
		t.Fatalf("Validate(%#v) = %v, want *Error", value, err)
	}
	return verr.Errors
}

func TestRules(t *testing.T) {
	type (
		required struct {
			S string            `json:"s" validate:"required"`
			P *int              `json:"p" validate:"required"`
			L []string          `json:"l" validate:"required"`
			M map[string]string `json:"m" validate:"required"`
		}
		email struct {
			V string `json:"v" validate:"email"`
		}
		urlField struct {
			V string `json:"v" validate:"url"`
		}
		uuidField struct {
			V string `json:"v" validate:"uuid"`
		}
		regex struct {
			V string `json:"v" validate:"regex=^[a-z]{2,3}$"`
		}
		numeric struct {
			V string `json:"v" validate:"numeric"`
		}
		sizes struct {
			Name  string   `json:"name" validate:"min=2,max=4"`
			Age   int      `json:"age" validate:"between=18 65"`
			Code  string   `json:"code" validate:"len=3"`
			Tags  []string `json:"tags" validate:"max=2"`
			Score string   `json:"score" validate:"numeric,min=10"`
		}
		in struct {
			Status string `json:"status" validate:"in=draft published"`
			Level  int    `json:"level" validate:"in=1 2 3"`
		}
	)
	v := New()
	for _, tt := range []struct {
		name  string
		value any
		want  []string // failing keys, sorted
	}{
		{"required missing", required{S: "  "}, []string{"l", "m", "p", "s"}},
		{"required present", required{S: "x", P: new(int), L: []string{"a"}, M: map[string]string{"a": "b"}}, nil},
		{"email valid", email{"ada@example.com"}, nil},
		{"email display name", email{"Ada <ada@example.com>"}, []string{"v"}},
		{"email invalid", email{"ada"}, []string{"v"}},
		{"email empty skips", email{""}, nil},
		{"url valid", urlField{"https://golar.dev/docs"}, nil},
		{"url relative", urlField{"/docs"}, []string{"v"}},
		{"uuid valid", uuidField{"123e4567-e89b-12d3-a456-426614174000"}, nil},
		{"uuid invalid", uuidField{"123e4567"}, []string{"v"}},
		{"regex valid", regex{"abc"}, nil},
		{"regex invalid", regex{"abcd"}, []string{"v"}},
		{"numeric valid", numeric{"-1.5"}, nil},
		{"numeric invalid", numeric{"1e"}, []string{"v"}},
		{"numeric NaN", numeric{"NaN"}, []string{"v"}},
		{"sizes valid", sizes{Name: "Ada", Age: 30, Code: "abc", Tags: []string{"a"}, Score: "12"}, nil},
		{"sizes invalid", sizes{Name: "A", Age: 70, Code: "ab", Tags: []string{"a", "b", "c"}, Score: "9"}, []string{"age", "code", "name", "score", "tags"}},
		{"size counts runes", sizes{Name: "Çağrı"}, []string{"name"}},
		{"in valid", in{"draft", 2}, nil},
		{"in invalid", in{"deleted", 4}, []string{"level", "status"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			errs := failures(t, v, tt.value)
			if got := slices.Sorted(maps.Keys(errs)); !slices.Equal(got, tt.want) {
				t.Fatalf("failing fields = %v, want %v (%v)", got, tt.want, errs)
			}
		})
	}
}

func TestMessages(t *testing.T) {
	type request struct {
		Name     string   `json:"full_name" validate:"required,max=3"`
		Age      int      `json:"age" validate:"between=18 65"`
		Tags     []string `json:"tags" validate:"min=2"`
		Status   string   `json:"status" validate:"in=draft published"`
		Password string   `json:"password" validate:"required,confirmed"`
		Confirm  string   `json:"password_confirmation"`
	}
	v := New()
	v.Messages["password.confirmed"] = "Passwords must match."
	v.Messages["between.numeric"] = ":attribute out of range :min-:max."
	v.Attributes["tags"] = "labels"
	errs := failures(t, v, request{Name: "Grace", Age: 5, Tags: []string{"a"}, Status: "x", Password: "secret", Confirm: "other"})
	want := Errors{
		"full_name": {"The full name field must not be greater than 3 characters."},
		"age":       {"age out of range 18-65."},
		"tags":      {"The labels field must have at least 2 items."},
		"status":    {"The selected status is invalid."},
		"password":  {"Passwords must match."},
	}
	if !reflect.DeepEqual(errs, want) {
		t.Fatalf("errors = %#v\nwant %#v", errs, want)
	}
	if errs := failures(t, v, request{}); !reflect.DeepEqual(errs["password"], []string{"The password field is required."}) || len(errs["full_name"]) != 1 {
		t.Fatalf("missing values should only fail required: %v", errs)
	}
}

func TestConfirmed(t *testing.T) {
	type goNames struct {
		Password             string `validate:"confirmed"`
		PasswordConfirmation string
	}
	type explicit struct {
		Email  string `json:"email" validate:"confirmed=repeat"`
		Repeat string `json:"repeat"`
	}
	type missing struct {
		Password string `json:"password" validate:"confirmed"`
	}
	v := New()
	if errs := failures(t, v, goNames{"a", "a"}); errs != nil {
		t.Fatal(errs)
	}
	if errs := failures(t, v, explicit{"a", "b"}); len(errs["email"]) != 1 {
		t.Fatal(errs)
	}
	if errs := failures(t, v, missing{"a"}); len(errs["password"]) != 1 {
		t.Fatal(errs)
	}
}

func TestNestedStructs(t *testing.T) {
	type Address struct {
		City string `json:"city" validate:"required"`
	}
	type Timestamps struct {
		Note string `json:"note" validate:"max=2"`
	}
	type user struct {
		Timestamps
		Home *Address `json:"home" validate:"required"`
		Work *Address `json:"work"`
		Ship Address  `json:"ship"`
	}
	errs := failures(t, New(), user{Timestamps: Timestamps{"long"}, Work: &Address{}})
	want := []string{"home", "note", "ship.city", "work.city"}
	if got := slices.Sorted(maps.Keys(errs)); !slices.Equal(got, want) {
		t.Fatalf("errors = %v, want keys %v", errs, want)
	}
}

func TestCustomRule(t *testing.T) {
	type post struct {
		Slug string `json:"slug" validate:"slug"`
	}
	v := New()
	if err := v.Validate(post{"x"}); err == nil || !strings.Contains(err.Error(), `unknown rule "slug"`) {
		t.Fatalf("err = %v", err)
	}
	v.Register("slug", "The :attribute must be a slug.", func(_ context.Context, f Field) (bool, error) {
		return !strings.ContainsAny(f.Value.String(), " /"), nil
	})
	if errs := failures(t, v, post{"a b"}); !reflect.DeepEqual(errs["slug"], []string{"The slug must be a slug."}) {
		t.Fatal(errs)
	}
	if errs := failures(t, v, post{"a-b"}); errs != nil {
		t.Fatal(errs)
	}
	boom := errors.New("boom")
	v.Register("slug", "", func(context.Context, Field) (bool, error) { return false, boom })
	if err := v.Validate(post{"a"}); !errors.Is(err, boom) {
		t.Fatalf("rule errors must abort validation, got %v", err)
	}
}

func TestInvalidInput(t *testing.T) {
	v := New()
	for _, value := range []any{nil, 3, (*struct{})(nil)} {
		if err := v.Validate(value); err == nil || errors.As(err, new(*Error)) {
			t.Errorf("Validate(%#v) = %v", value, err)
		}
	}
	for _, value := range []any{
		struct {
			V int `validate:"min=x"`
		}{},
		struct {
			V int `validate:"between=1"`
		}{},
		struct {
			V string `validate:"regex=("`
		}{},
		struct {
			V string `validate:"email=yes"`
		}{},
		struct {
			V string `validate:"unique"`
		}{},
	} {
		if err := v.Validate(value); err == nil || errors.As(err, new(*Error)) {
			t.Errorf("Validate(%T) = %v, want a tag error", value, err)
		}
	}
	if !errors.Is(v.Validate(struct {
		V string `validate:"unique=users"`
	}{"a"}), errNoDB) {
		t.Error("unique without DB should fail")
	}
}

func TestErrorString(t *testing.T) {
	err := &Error{Errors: Errors{"b": {"B."}, "a": {"A1.", "A2."}}}
	if got := err.Error(); got != "validation failed: a: A1. A2.; b: B." {
		t.Fatal(got)
	}
}

func TestPlansAreCached(t *testing.T) {
	type request struct {
		Name string `json:"name" validate:"required"`
	}
	v := New()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _ = v.Validate(request{}) })
	}
	wg.Wait()
	first, _ := v.plan(reflect.TypeFor[request]())
	second, _ := v.plan(reflect.TypeFor[request]())
	if first != second {
		t.Fatal("plan should be parsed once per type")
	}
}

func TestDatabaseRules(t *testing.T) {
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(t.TempDir(), "validation.db")}
	if driver != "sqlite" {
		config.Database, config.DSN = "", os.Getenv("GOLAR_TEST_DSN")
	}
	db, err := database.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	for _, statement := range []string{
		"DROP TABLE IF EXISTS validation_users",
		"CREATE TABLE validation_users (id INTEGER NOT NULL, email VARCHAR(100) NOT NULL)",
		"INSERT INTO validation_users (id, email) VALUES (1, 'taken@example.com')",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DROP TABLE validation_users") })
	type request struct {
		Email  string `json:"email" validate:"unique=validation_users.email"`
		UserID int    `json:"user_id" validate:"exists=validation_users.id"`
	}
	v := New()
	v.DB = db
	if errs := failures(t, v, request{Email: "new@example.com", UserID: 1}); errs != nil {
		t.Fatal(errs)
	}
	errs := failures(t, v, request{Email: "taken@example.com", UserID: 2})
	if !reflect.DeepEqual(errs, Errors{"email": {"The email has already been taken."}, "user_id": {"The selected user id is invalid."}}) {
		t.Fatal(errs)
	}
}
