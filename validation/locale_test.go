package validation

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTurkishCatalogueIsComplete(t *testing.T) {
	for key := range defaultMessages {
		if _, ok := turkish[key]; !ok {
			t.Errorf("turkish lacks %q", key)
		}
	}
	for key, message := range turkish {
		if !strings.Contains(message, ":attribute") {
			t.Errorf("turkish %q does not name the field", key)
		}
	}
}

func TestLocales(t *testing.T) {
	type signup struct {
		Email string   `json:"email" validate:"required,email"`
		Name  string   `json:"name" validate:"min=3"`
		Tags  []string `json:"tags" validate:"each,alpha"`
		Code  string   `json:"code" validate:"slug"`
	}
	v := New()
	v.Register("slug", "The :attribute must be a slug.", func(_ context.Context, f Field) (bool, error) {
		return !strings.Contains(f.Value.String(), " "), nil
	})
	v.Messages["email.required"] = "English override for :attribute."
	v.AddLocale("tr", map[string]string{"name.min": ":attribute çok kısa."}, map[string]string{"email": "e-posta", "tags.*": "etiket"})
	in := signup{Name: "Al", Tags: []string{"ok", "1"}, Code: "a b"}

	errorsFor := func(ctx context.Context) Errors {
		t.Helper()
		var verr *Error
		if err := v.ValidateContext(ctx, in); !errors.As(err, &verr) {
			t.Fatalf("error = %v", err)
		}
		return verr.Errors
	}
	english := errorsFor(context.Background())
	if english["email"][0] != "English override for email." || english["tags.1"][0] != "The tags.1 field must only contain letters." {
		t.Errorf("english = %v", english)
	}
	for _, name := range []string{"tr", "TR", "tr-TR", "tr_tr"} {
		turkishErrors := errorsFor(WithLocale(context.Background(), name))
		want := map[string]string{
			"email":  "e-posta alanı zorunludur.",
			"name":   "name çok kısa.",
			"tags.1": "etiket alanı yalnızca harf içerebilir.",
			"code":   "The code must be a slug.", // custom rule without a translation
		}
		for key, message := range want {
			if got := turkishErrors[key]; len(got) != 1 || got[0] != message {
				t.Errorf("%s: %s = %q, want %q", name, key, got, message)
			}
		}
	}
	if got := errorsFor(WithLocale(context.Background(), "fr"))["name"][0]; got != "The name field must be at least 3 characters." {
		t.Errorf("unknown locale = %q", got)
	}

	v.Register("vague", "", func(context.Context, Field) (bool, error) { return false, nil })
	vague := struct {
		X string `json:"x" validate:"vague"`
	}{"x"}
	var verr *Error
	if err := v.ValidateContext(WithLocale(context.Background(), "tr"), vague); !errors.As(err, &verr) || verr.Errors["x"][0] != "x alanı geçersiz." {
		t.Errorf("generic Turkish message = %v", err)
	}
	if got := v.Locales(); len(got) != 1 || got[0] != "tr" {
		t.Errorf("Locales = %v", got)
	}
	defer func() {
		if recover() == nil {
			t.Error("empty locale name accepted")
		}
	}()
	v.AddLocale(" ", nil, nil)
}

func TestMatchLocaleAndMiddleware(t *testing.T) {
	v := New()
	v.AddLocale("pt-br", map[string]string{"required": "O campo :attribute é obrigatório."}, nil)
	for header, want := range map[string]string{
		"":                        "",
		"tr":                      "tr",
		"tr-TR,tr;q=0.9,en;q=0.8": "tr",
		"en-US,en;q=0.9,tr;q=0.8": "",
		"fr;q=0.9, tr;q=0.5":      "tr",
		"de, *;q=0.1":             "",
		"tr;q=0":                  "",
		"pt-BR":                   "pt-br",
		"tr;q=0.4, pt-BR;q=0.6":   "pt-br",
		"tr;q=bad, pt-br":         "pt-br",
	} {
		if got := v.MatchLocale(header); got != want {
			t.Errorf("MatchLocale(%q) = %q, want %q", header, got, want)
		}
	}

	var seen string
	handler := v.LocaleMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = LocaleFrom(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("Accept-Language", "tr-TR,tr;q=0.9")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, r)
	if seen != "tr" || rec.Header().Get("Vary") != "Accept-Language" {
		t.Errorf("locale = %q, Vary = %q", seen, rec.Header().Get("Vary"))
	}
	r = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(WithLocale(context.Background(), "pt-BR"))
	r.Header.Set("Accept-Language", "tr")
	handler.ServeHTTP(httptest.NewRecorder(), r)
	if seen != "pt-br" {
		t.Errorf("explicit locale replaced: %q", seen)
	}
}
