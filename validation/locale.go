package validation

import (
	"context"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// locale holds the messages and attribute names of one language.
type locale struct {
	messages   map[string]string
	attributes map[string]string
}

func (l *locale) lookup(key string) (string, bool) {
	if l == nil {
		return "", false
	}
	message, ok := l.messages[key]
	return message, ok
}

type localeKey struct{}

// WithLocale returns ctx with a message locale, such as "tr". Validation with
// that context uses the locale's messages and attribute names, falling back to
// English for anything the locale does not define.
func WithLocale(ctx context.Context, name string) context.Context {
	return context.WithValue(ctx, localeKey{}, normalizeLocale(name))
}

// LocaleFrom returns the locale set by WithLocale, or "".
func LocaleFrom(ctx context.Context) string {
	name, _ := ctx.Value(localeKey{}).(string)
	return name
}

func normalizeLocale(name string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(name), "_", "-"))
}

// AddLocale adds messages and attribute names for a locale, merging with any
// already registered. Keys are those of Messages and Attributes. Locale names
// are case-insensitive; "pt-BR" falls back to "pt" when only that exists.
func (v *Validator) AddLocale(name string, messages, attributes map[string]string) {
	name = normalizeLocale(name)
	if name == "" {
		panic("validation: empty locale name")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	l, ok := v.locales[name]
	if !ok {
		l = &locale{messages: map[string]string{}, attributes: map[string]string{}}
		v.locales[name] = l
	}
	maps.Copy(l.messages, messages)
	maps.Copy(l.attributes, attributes)
}

// AddLocale adds a locale to Default. See Validator.AddLocale.
func AddLocale(name string, messages, attributes map[string]string) {
	Default.AddLocale(name, messages, attributes)
}

// Locales returns the registered locale names, sorted. English is built in
// and not listed.
func (v *Validator) Locales() []string {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return slices.Sorted(maps.Keys(v.locales))
}

// lookupLocale returns the registered locale for name, or for its language
// when only that is registered, with the name it was found under.
func (v *Validator) lookupLocale(name string) (string, *locale) {
	if name == "" {
		return "", nil
	}
	v.mu.RLock()
	defer v.mu.RUnlock()
	if l, ok := v.locales[name]; ok {
		return name, l
	}
	if language, _, ok := strings.Cut(name, "-"); ok {
		if l, ok := v.locales[language]; ok {
			return language, l
		}
	}
	return "", nil
}

// MatchLocale picks the registered locale that best fits an Accept-Language
// header, or "" for English.
func (v *Validator) MatchLocale(acceptLanguage string) string {
	type choice struct {
		tag string
		q   float64
	}
	var choices []choice
	for part := range strings.SplitSeq(acceptLanguage, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		if value, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				continue
			}
			q = parsed
		}
		if tag = normalizeLocale(tag); tag != "" && tag != "*" && q > 0 {
			choices = append(choices, choice{tag, q})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool { return choices[i].q > choices[j].q })
	for _, c := range choices {
		if c.tag == "en" || strings.HasPrefix(c.tag, "en-") {
			return ""
		}
		if name, l := v.lookupLocale(c.tag); l != nil {
			return name
		}
	}
	return ""
}

// LocaleMiddleware sets the request's validation locale from Accept-Language,
// choosing among the locales registered on Default. Requests that already
// carry a locale keep it.
func LocaleMiddleware(next http.Handler) http.Handler {
	return Default.LocaleMiddleware(next)
}

// LocaleMiddleware is LocaleMiddleware for v's locales.
func (v *Validator) LocaleMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if LocaleFrom(r.Context()) == "" {
			if name := v.MatchLocale(r.Header.Get("Accept-Language")); name != "" {
				r = r.WithContext(WithLocale(r.Context(), name))
			}
		}
		w.Header().Add("Vary", "Accept-Language")
		next.ServeHTTP(w, r)
	})
}

// turkish is the built-in "tr" catalogue.
var turkish = map[string]string{
	"invalid":         ":attribute alanı geçersiz.",
	"required":        ":attribute alanı zorunludur.",
	"email":           ":attribute alanı geçerli bir e-posta adresi olmalıdır.",
	"numeric":         ":attribute alanı bir sayı olmalıdır.",
	"url":             ":attribute alanı geçerli bir URL olmalıdır.",
	"uuid":            ":attribute alanı geçerli bir UUID olmalıdır.",
	"regex":           ":attribute alanının biçimi geçersiz.",
	"in":              "Seçilen :attribute geçersiz.",
	"confirmed":       ":attribute alanı onayı eşleşmiyor.",
	"unique":          ":attribute zaten kullanılıyor.",
	"exists":          "Seçilen :attribute geçersiz.",
	"min.string":      ":attribute alanı en az :param karakter olmalıdır.",
	"min.numeric":     ":attribute alanı en az :param olmalıdır.",
	"min.array":       ":attribute alanı en az :param öğe içermelidir.",
	"max.string":      ":attribute alanı en fazla :param karakter olabilir.",
	"max.numeric":     ":attribute alanı :param değerinden büyük olamaz.",
	"max.array":       ":attribute alanı en fazla :param öğe içerebilir.",
	"len.string":      ":attribute alanı :param karakter olmalıdır.",
	"len.numeric":     ":attribute alanı :param olmalıdır.",
	"len.array":       ":attribute alanı :param öğe içermelidir.",
	"between.string":  ":attribute alanı :min ile :max karakter arasında olmalıdır.",
	"between.numeric": ":attribute alanı :min ile :max arasında olmalıdır.",
	"between.array":   ":attribute alanı :min ile :max arasında öğe içermelidir.",
	"string":          ":attribute alanı metin olmalıdır.",
	"integer":         ":attribute alanı tam sayı olmalıdır.",
	"boolean":         ":attribute alanı doğru ya da yanlış olmalıdır.",
	"alpha":           ":attribute alanı yalnızca harf içerebilir.",
	"alpha_num":       ":attribute alanı yalnızca harf ve rakam içerebilir.",
	"alpha_dash":      ":attribute alanı yalnızca harf, rakam, tire ve alt çizgi içerebilir.",
	"starts_with":     ":attribute alanı şunlardan biriyle başlamalıdır: :values.",
	"ends_with":       ":attribute alanı şunlardan biriyle bitmelidir: :values.",
	"date":            ":attribute alanı geçerli bir tarih olmalıdır.",
}
