package context

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dayanchm/golar/validation"
)

func newRequest(method, contentType, body string) *http.Request {
	r := httptest.NewRequest(method, "/", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r
}

func multipartBody(t *testing.T, fields map[string]string, files map[string]string) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for name, value := range fields {
		if err := w.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range files {
		part, err := w.CreateFormFile(name, name+".txt")
		if err != nil {
			t.Fatal(err)
		}
		part.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String(), w.FormDataContentType()
}

func TestQueryHelpers(t *testing.T) {
	c := New(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/?page=3&sort=&bad=x1", nil))
	if got := c.QueryDefault("sort", "name"); got != "name" {
		t.Errorf("QueryDefault(empty) = %q", got)
	}
	if got := c.QueryDefault("missing", "name"); got != "name" {
		t.Errorf("QueryDefault(missing) = %q", got)
	}
	if got := c.QueryDefault("page", "1"); got != "3" {
		t.Errorf("QueryDefault(page) = %q", got)
	}
	if n, err := c.QueryInt("page", 1); n != 3 || err != nil {
		t.Errorf("QueryInt(page) = %d, %v", n, err)
	}
	if n, err := c.QueryInt("missing", 7); n != 7 || err != nil {
		t.Errorf("QueryInt(missing) = %d, %v", n, err)
	}
	n, err := c.QueryInt("bad", 1)
	var inputErr *InputError
	if n != 1 || !errors.As(err, &inputErr) || inputErr.Source != "query" || inputErr.Name != "bad" {
		t.Errorf("QueryInt(bad) = %d, %v", n, err)
	}
}

func TestBodyLimits(t *testing.T) {
	var dst map[string]any
	t.Run("default", func(t *testing.T) {
		body := `{"a":"` + strings.Repeat("x", DefaultMaxBodySize) + `"}`
		c := New(httptest.NewRecorder(), newRequest(http.MethodPost, "application/json", body))
		var tooLarge *http.MaxBytesError
		if err := c.BindJSON(&dst); !errors.As(err, &tooLarge) {
			t.Fatalf("error = %v, want *http.MaxBytesError", err)
		}
	})
	t.Run("configured", func(t *testing.T) {
		c := New(httptest.NewRecorder(), newRequest(http.MethodPost, "application/json", `{"name":"Ali Veli"}`), Config{MaxBodySize: 8})
		var tooLarge *http.MaxBytesError
		if err := c.Bind(&dst); !errors.As(err, &tooLarge) {
			t.Fatalf("error = %v, want *http.MaxBytesError", err)
		}
	})
	t.Run("form", func(t *testing.T) {
		c := New(httptest.NewRecorder(), newRequest(http.MethodPost, "application/x-www-form-urlencoded", "name="+strings.Repeat("x", 20)), Config{MaxBodySize: 8})
		var tooLarge *http.MaxBytesError
		if err := c.ParseForm(); !errors.As(err, &tooLarge) {
			t.Fatalf("error = %v, want *http.MaxBytesError", err)
		}
		if c.Form("name") != "" {
			t.Error("Form returned a value from a rejected body")
		}
	})
	t.Run("multipart", func(t *testing.T) {
		body, contentType := multipartBody(t, nil, map[string]string{"avatar": strings.Repeat("x", 100)})
		c := New(httptest.NewRecorder(), newRequest(http.MethodPost, contentType, body), Config{MaxBodySize: 8, MaxMultipartSize: 64})
		var tooLarge *http.MaxBytesError
		if _, err := c.File("avatar"); !errors.As(err, &tooLarge) {
			t.Fatalf("error = %v, want *http.MaxBytesError", err)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		c := New(httptest.NewRecorder(), newRequest(http.MethodPost, "application/json", `{"name":"Ali Veli"}`), Config{MaxBodySize: -1})
		if err := c.Bind(&dst); err != nil {
			t.Fatal(err)
		}
	})
}

type address struct {
	City string `form:"city"`
}

type Audit struct {
	Note string `form:"note"`
}

type signup struct {
	Audit
	Name     string    `form:"name" validate:"required"`
	Email    string    `json:"email"`
	Age      int       `form:"age"`
	Score    *float64  `form:"score"`
	Terms    bool      `form:"terms"`
	Tags     []string  `form:"tag"`
	IDs      []uint    `form:"id"`
	Born     time.Time `form:"born"`
	Address  address   `form:"address"`
	Ignored  string    `form:"-"`
	Nickname string
	secret   string
}

func TestBindForm(t *testing.T) {
	form := url.Values{
		"name": {"Ali"}, "email": {"ali@example.com"}, "age": {"30"}, "score": {"9.5"}, "terms": {"on"},
		"tag": {"a", "b"}, "id": {"1", "2"}, "born": {"1990-01-02T00:00:00Z"}, "address.city": {"Aşgabat"},
		"note": {"embedded"}, "Ignored": {"x"}, "-": {"x"}, "Nickname": {"ali"}, "secret": {"x"},
	}
	c := New(httptest.NewRecorder(), newRequest(http.MethodPost, "application/x-www-form-urlencoded", form.Encode()))
	var got signup
	if err := c.Bind(&got); err != nil {
		t.Fatal(err)
	}
	born := time.Date(1990, 1, 2, 0, 0, 0, 0, time.UTC)
	if got.Name != "Ali" || got.Email != "ali@example.com" || got.Age != 30 || got.Score == nil || *got.Score != 9.5 ||
		!got.Terms || strings.Join(got.Tags, ",") != "a,b" || len(got.IDs) != 2 || got.IDs[1] != 2 ||
		!got.Born.Equal(born) || got.Address.City != "Aşgabat" || got.Note != "embedded" ||
		got.Ignored != "" || got.Nickname != "ali" || got.secret != "" {
		t.Errorf("bound %+v", got)
	}
	if c.Form("name") != "Ali" {
		t.Errorf("Form(name) = %q", c.Form("name"))
	}
}

func TestBindFormLeavesEmptyValues(t *testing.T) {
	c := New(httptest.NewRecorder(), newRequest(http.MethodPost, "application/x-www-form-urlencoded", "age=&name="))
	got := signup{Age: 5, Name: "old"}
	if err := c.Bind(&got); err != nil {
		t.Fatal(err)
	}
	if got.Age != 5 || got.Name != "" {
		t.Errorf("bound %+v, want Age kept and Name cleared", got)
	}
}

func TestBindErrors(t *testing.T) {
	for _, tt := range []struct {
		name, contentType, body string
		dst                     any
		unsupported             bool
	}{
		{"malformed JSON", "application/json", `{"name":`, &signup{}, false},
		{"trailing JSON", "application/json", `{"name":"a"} {"name":"b"}`, &signup{}, false},
		{"invalid int", "application/x-www-form-urlencoded", "age=old", &signup{}, false},
		{"invalid bool", "application/x-www-form-urlencoded", "terms=maybe", &signup{}, false},
		{"invalid slice item", "application/x-www-form-urlencoded", "id=1&id=-2", &signup{}, false},
		{"form into map", "application/x-www-form-urlencoded", "a=b", &map[string]string{}, false},
		{"unsupported", "application/xml", "<a/>", &signup{}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := New(httptest.NewRecorder(), newRequest(http.MethodPost, tt.contentType, tt.body))
			err := c.Bind(tt.dst)
			var bindErr *BindError
			if !errors.As(err, &bindErr) {
				t.Fatalf("error = %v, want *BindError", err)
			}
			if errors.Is(err, ErrUnsupportedMediaType) != tt.unsupported {
				t.Errorf("error = %v, unsupported = %v", err, tt.unsupported)
			}
		})
	}
}

func TestBindJSONVariants(t *testing.T) {
	for _, contentType := range []string{"", "application/json; charset=utf-8", "application/merge-patch+json"} {
		c := New(httptest.NewRecorder(), newRequest(http.MethodPost, contentType, `{"name":"Ali"} `))
		var got signup
		if err := c.Bind(&got); err != nil || got.Name != "Ali" {
			t.Errorf("%q: Bind = %+v, %v", contentType, got, err)
		}
	}
}

func TestValidateForm(t *testing.T) {
	c := New(httptest.NewRecorder(), newRequest(http.MethodPost, "application/x-www-form-urlencoded", "age=3"))
	var invalid *validation.Error
	if err := c.Validate(&signup{}); !errors.As(err, &invalid) || len(invalid.Errors["name"]) == 0 {
		t.Fatalf("Validate error = %v, want a name error", err)
	}
	c = New(httptest.NewRecorder(), newRequest(http.MethodPost, "text/csv", "a,b"))
	if err := c.Validate(&signup{}); !errors.Is(err, ErrUnsupportedMediaType) {
		t.Fatalf("Validate error = %v, want ErrUnsupportedMediaType", err)
	}
}

func TestMultipart(t *testing.T) {
	body, contentType := multipartBody(t, map[string]string{"name": "Ali"}, map[string]string{"avatar": "hello"})
	c := New(httptest.NewRecorder(), newRequest(http.MethodPost, contentType, body))
	var got struct {
		Name   string                  `form:"name"`
		Avatar *multipart.FileHeader   `form:"avatar"`
		All    []*multipart.FileHeader `form:"avatar"`
	}
	if err := c.Bind(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "Ali" || got.Avatar == nil || got.Avatar.Filename != "avatar.txt" || len(got.All) != 1 {
		t.Fatalf("bound %+v", got)
	}
	if c.Form("name") != "Ali" {
		t.Errorf("Form(name) = %q", c.Form("name"))
	}
	file, err := c.File("avatar")
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "uploads", "a.txt")
	if err := c.SaveFile(file, dst); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(dst); err != nil || string(data) != "hello" {
		t.Errorf("saved %q, %v", data, err)
	}
	if _, err := c.File("missing"); !errors.Is(err, http.ErrMissingFile) {
		t.Errorf("File(missing) error = %v", err)
	}
	if err := c.SaveFile(file, filepath.Join(dst, "nested")); err == nil {
		t.Error("SaveFile under a file succeeded")
	}
}

func TestFileWithoutMultipart(t *testing.T) {
	c := New(httptest.NewRecorder(), newRequest(http.MethodPost, "application/json", `{}`))
	var bindErr *BindError
	if _, err := c.File("avatar"); !errors.As(err, &bindErr) || !errors.Is(err, http.ErrNotMultipart) {
		t.Errorf("File error = %v", err)
	}
	if c.Form("name") != "" {
		t.Error("Form read a JSON body")
	}
}

type postRequest struct {
	Title string `json:"title" validate:"required,max=10"`
	Owner string `json:"owner"`
	calls *[]string
}

func (p *postRequest) Authorize(c *Context) error {
	*p.calls = append(*p.calls, "authorize")
	if c.Request.Header.Get("X-User") != p.Owner {
		return ErrForbidden
	}
	return nil
}

func (p *postRequest) Prepare(*Context) error {
	*p.calls = append(*p.calls, "prepare")
	p.Title = strings.TrimSpace(p.Title)
	return nil
}

func TestFormRequest(t *testing.T) {
	run := func(body, user string) ([]string, *postRequest, error) {
		var calls []string
		r := newRequest(http.MethodPost, "application/json", body)
		r.Header.Set("X-User", user)
		in := &postRequest{calls: &calls}
		err := New(httptest.NewRecorder(), r).Validate(in)
		return calls, in, err
	}
	calls, in, err := run(`{"title":"  Hello   ","owner":"ada"}`, "ada")
	if err != nil || in.Title != "Hello" || strings.Join(calls, ",") != "authorize,prepare" {
		t.Fatalf("Validate = %v, title %q, calls %v", err, in.Title, calls)
	}
	if calls, _, err := run(`{"title":"Hello","owner":"ada"}`, "eve"); !errors.Is(err, ErrForbidden) || len(calls) != 1 {
		t.Errorf("forbidden = %v, calls %v", err, calls)
	}
	var verr *validation.Error
	if _, _, err := run(`{"title":"   ","owner":"ada"}`, "ada"); !errors.As(err, &verr) || len(verr.Errors["title"]) != 1 {
		t.Errorf("prepared blank title = %v", err)
	}
	if calls, _, err := run(`{"title":`, "ada"); len(calls) != 0 || err == nil {
		t.Errorf("bad body ran hooks: %v %v", calls, err)
	}
}
