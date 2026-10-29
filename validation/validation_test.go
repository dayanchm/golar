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
	"time"

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

func TestFormTagKeys(t *testing.T) {
	var input struct {
		Name  string `form:"full_name" validate:"required"`
		Email string `json:"email" form:"mail" validate:"required"`
	}
	var invalid *Error
	if err := Validate(&input); !errors.As(err, &invalid) {
		t.Fatalf("error = %v", err)
	}
	if len(invalid.Errors["full_name"]) == 0 || len(invalid.Errors["email"]) == 0 || len(invalid.Errors) != 2 {
		t.Errorf("errors = %v, want full_name and email", invalid.Errors)
	}
}

func TestTypeAndFormatRules(t *testing.T) {
	type input struct {
		Name     any       `json:"name" validate:"string"`
		Count    any       `json:"count" validate:"integer"`
		Active   any       `json:"active" validate:"boolean"`
		First    string    `json:"first" validate:"alpha"`
		Handle   string    `json:"handle" validate:"alpha_num"`
		Slug     string    `json:"slug" validate:"alpha_dash"`
		Site     string    `json:"site" validate:"starts_with=http:// https://"`
		File     string    `json:"file" validate:"ends_with=.png .jpg"`
		Born     string    `json:"born" validate:"date"`
		Due      string    `json:"due" validate:"date=02.01.2006"`
		Starts   time.Time `json:"starts" validate:"date"`
		Optional string    `json:"optional" validate:"alpha"`
	}
	valid := input{
		Name: "Ayşe", Count: float64(3), Active: "1", First: "Ayşe", Handle: "user42", Slug: "my-post_1",
		Site: "https://example.com", File: "a.png", Born: "1990-01-02", Due: "31.12.2026",
		Starts: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	if err := Validate(valid); err != nil {
		t.Fatalf("valid input: %v", err)
	}
	for _, variant := range []any{int64(7), uint8(1), "12", "-4"} {
		valid.Count = variant
		if err := Validate(valid); err != nil {
			t.Errorf("integer %#v: %v", variant, err)
		}
	}
	for _, variant := range []any{true, false, 0, 1, "true", "0"} {
		valid.Active = variant
		if err := Validate(valid); err != nil {
			t.Errorf("boolean %#v: %v", variant, err)
		}
	}
	valid.Born = "2026-10-29T10:00:00Z"
	if err := Validate(valid); err != nil {
		t.Errorf("RFC 3339 date: %v", err)
	}

	invalid := input{
		Name: 42, Count: 1.5, Active: "yes", First: "Ayşe1", Handle: "user 42", Slug: "my post",
		Site: "ftp://example.com", File: "a.gif", Born: "02/01/1990", Due: "2026-12-31",
		Starts: time.Time{}, Optional: "",
	}
	var verr *Error
	if err := Validate(invalid); !errors.As(err, &verr) {
		t.Fatalf("invalid input error = %v", err)
	}
	want := []string{"active", "born", "count", "due", "file", "first", "handle", "name", "site", "slug"}
	got := slices.Sorted(maps.Keys(verr.Errors))
	if !slices.Equal(got, want) {
		t.Errorf("failing keys = %v, want %v", got, want)
	}
	if msg := verr.Errors["site"][0]; msg != "The site field must start with one of the following: http://, https://." {
		t.Errorf("starts_with message = %q", msg)
	}
	for _, value := range []any{"12.5", "x", true, nil} {
		invalid := struct {
			V any `json:"v" validate:"required,integer"`
		}{value}
		if err := Validate(invalid); err == nil {
			t.Errorf("integer accepted %#v", value)
		}
	}
	for _, tag := range []string{"starts_with", "string=x", "alpha_dash=1"} {
		if _, err := New().parseTag(tag); err == nil {
			t.Errorf("tag %q accepted", tag)
		}
	}
}

func TestCollections(t *testing.T) {
	type item struct {
		SKU      string `json:"sku" validate:"required,alpha_dash"`
		Quantity int    `json:"quantity" validate:"required,min=1"`
	}
	type order struct {
		Items    []item            `json:"items" validate:"required,max=3"`
		Pointers []*item           `json:"pointers"`
		Fixed    [2]item           `json:"fixed"`
		ByCode   map[string]item   `json:"by_code"`
		Emails   []string          `json:"emails" validate:"max=3,each,required,email"`
		Scores   []any             `json:"scores" validate:"each,integer,between=1 5"`
		Labels   map[string]string `json:"labels" validate:"each,max=5"`
	}
	in := order{
		Items:    []item{{SKU: "a-1", Quantity: 1}, {SKU: "b 2", Quantity: 0}},
		Pointers: []*item{nil, {SKU: "", Quantity: 2}},
		Fixed:    [2]item{{SKU: "x", Quantity: 1}, {SKU: "y"}},
		ByCode:   map[string]item{"tr": {SKU: "t", Quantity: 1}, "en": {Quantity: 1}},
		Emails:   []string{"ada@example.com", "", "nope"},
		Scores:   []any{float64(3), 1.5, "9"},
		Labels:   map[string]string{"en": "Hello", "tr": "Merhaba"},
	}
	var verr *Error
	if err := Validate(in); !errors.As(err, &verr) {
		t.Fatalf("error = %v", err)
	}
	want := map[string]string{
		"items.1.sku":      "The sku field must only contain letters, numbers, dashes and underscores.",
		"items.1.quantity": "The quantity field is required.",
		"pointers.1.sku":   "The sku field is required.",
		"fixed.1.quantity": "The quantity field is required.",
		"by_code.en.sku":   "The sku field is required.",
		"emails.1":         "The emails.1 field is required.",
		"emails.2":         "The emails.2 field must be a valid email address.",
		"scores.1":         "The scores.1 field must be an integer.",
		"scores.2":         "The scores.2 field must be between 1 and 5.",
		"labels.tr":        "The labels.tr field must not be greater than 5 characters.",
	}
	if len(verr.Errors) != len(want) {
		t.Errorf("errors = %v", verr.Errors)
	}
	for key, message := range want {
		if got := verr.Errors[key]; len(got) != 1 || got[0] != message {
			t.Errorf("%s = %q, want %q", key, got, message)
		}
	}

	// A collection that fails its own rules is not checked element by element.
	tooMany := order{Items: make([]item, 4), Fixed: [2]item{{SKU: "a", Quantity: 1}, {SKU: "b", Quantity: 1}}}
	if err := Validate(tooMany); !errors.As(err, &verr) || len(verr.Errors) != 1 || len(verr.Errors["items"]) != 1 {
		t.Errorf("max items error = %v", err)
	}

	v := New()
	v.Attributes["items.*.quantity"] = "item quantity"
	v.Messages["emails.*.email"] = "Use a real address for :attribute."
	err := v.Validate(order{Items: []item{{SKU: "a"}}, Emails: []string{"x"}})
	if !errors.As(err, &verr) || verr.Errors["items.0.quantity"][0] != "The item quantity field is required." ||
		verr.Errors["emails.0"][0] != "Use a real address for emails.0." {
		t.Errorf("wildcard overrides = %v", err)
	}

	for _, bad := range []any{
		struct {
			V string `validate:"each,email"`
		}{},
		struct {
			V []string `validate:"each,nope"`
		}{},
	} {
		if err := New().Validate(bad); err == nil || errors.As(err, &verr) {
			t.Errorf("%T: error = %v, want a tag error", bad, err)
		}
	}
	regexEach := struct {
		V string `json:"v" validate:"regex=^each$"`
	}{"each"}
	if err := Validate(regexEach); err != nil {
		t.Errorf("each inside a regex: %v", err)
	}
}
