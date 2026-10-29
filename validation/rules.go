package validation

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

var defaultMessages = map[string]string{
	"required":        "The :attribute field is required.",
	"email":           "The :attribute field must be a valid email address.",
	"numeric":         "The :attribute field must be a number.",
	"url":             "The :attribute field must be a valid URL.",
	"uuid":            "The :attribute field must be a valid UUID.",
	"regex":           "The :attribute field format is invalid.",
	"in":              "The selected :attribute is invalid.",
	"confirmed":       "The :attribute field confirmation does not match.",
	"unique":          "The :attribute has already been taken.",
	"exists":          "The selected :attribute is invalid.",
	"min.string":      "The :attribute field must be at least :param characters.",
	"min.numeric":     "The :attribute field must be at least :param.",
	"min.array":       "The :attribute field must have at least :param items.",
	"max.string":      "The :attribute field must not be greater than :param characters.",
	"max.numeric":     "The :attribute field must not be greater than :param.",
	"max.array":       "The :attribute field must not have more than :param items.",
	"len.string":      "The :attribute field must be :param characters.",
	"len.numeric":     "The :attribute field must be :param.",
	"len.array":       "The :attribute field must contain :param items.",
	"between.string":  "The :attribute field must be between :min and :max characters.",
	"between.numeric": "The :attribute field must be between :min and :max.",
	"between.array":   "The :attribute field must have between :min and :max items.",
	"string":          "The :attribute field must be a string.",
	"integer":         "The :attribute field must be an integer.",
	"boolean":         "The :attribute field must be true or false.",
	"alpha":           "The :attribute field must only contain letters.",
	"alpha_num":       "The :attribute field must only contain letters and numbers.",
	"alpha_dash":      "The :attribute field must only contain letters, numbers, dashes and underscores.",
	"starts_with":     "The :attribute field must start with one of the following: :values.",
	"ends_with":       "The :attribute field must end with one of the following: :values.",
	"date":            "The :attribute field must be a valid date.",
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func builtinRules(v *Validator) map[string]Rule {
	return map[string]Rule{
		"required": func(_ context.Context, f Field) (bool, error) {
			return !f.missing(), nil
		},
		"email": stringRule(func(s string) bool {
			address, err := mail.ParseAddress(s)
			return err == nil && address.Name == "" && address.Address == s && len(s) <= 254
		}),
		"url": stringRule(func(s string) bool {
			u, err := url.Parse(s)
			return err == nil && u.Scheme != "" && u.Host != ""
		}),
		"uuid":  stringRule(uuidPattern.MatchString),
		"regex": regexRule,
		"numeric": func(_ context.Context, f Field) (bool, error) {
			_, ok := number(f.Value)
			return ok, nil
		},
		"min":     sizeRule(func(size float64, p []float64) bool { return size >= p[0] }),
		"max":     sizeRule(func(size float64, p []float64) bool { return size <= p[0] }),
		"len":     sizeRule(func(size float64, p []float64) bool { return size == p[0] }),
		"between": sizeRule(func(size float64, p []float64) bool { return size >= p[0] && size <= p[1] }),
		"in": func(_ context.Context, f Field) (bool, error) {
			text, ok := scalar(f.Value)
			if !ok {
				return false, nil
			}
			for _, allowed := range strings.Fields(f.Param) {
				if text == allowed {
					return true, nil
				}
			}
			return false, nil
		},
		"confirmed": func(_ context.Context, f Field) (bool, error) {
			other, ok := f.Sibling(f.Param)
			if f.Param == "" {
				key := f.Name[strings.LastIndex(f.Name, ".")+1:]
				if other, ok = f.Sibling(key + "_confirmation"); !ok {
					other, ok = f.Sibling(key + "Confirmation")
				}
			}
			if !ok {
				return false, nil
			}
			return reflect.DeepEqual(f.Value.Interface(), other.Interface()), nil
		},
		"string": func(_ context.Context, f Field) (bool, error) {
			return f.Value.Kind() == reflect.String, nil
		},
		"integer": func(_ context.Context, f Field) (bool, error) {
			return isInteger(f.Value), nil
		},
		"boolean": func(_ context.Context, f Field) (bool, error) {
			return isBoolean(f.Value), nil
		},
		"alpha": stringRule(func(s string) bool {
			return allRunes(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsMark(r) })
		}),
		"alpha_num": stringRule(func(s string) bool {
			return allRunes(s, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) })
		}),
		"alpha_dash": stringRule(func(s string) bool {
			return allRunes(s, func(r rune) bool {
				return unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsNumber(r) || r == '-' || r == '_'
			})
		}),
		"starts_with": affixRule(strings.HasPrefix),
		"ends_with":   affixRule(strings.HasSuffix),
		"date": func(_ context.Context, f Field) (bool, error) {
			return isDate(f.Value, f.Param), nil
		},
		"unique": func(ctx context.Context, f Field) (bool, error) {
			count, err := v.count(ctx, f)
			return count == 0, err
		},
		"exists": func(ctx context.Context, f Field) (bool, error) {
			count, err := v.count(ctx, f)
			return count > 0, err
		},
	}
}

// allRunes reports whether s is non-empty valid UTF-8 whose runes all pass ok.
func allRunes(s string, ok func(rune) bool) bool {
	if s == "" || !utf8.ValidString(s) {
		return false
	}
	for _, r := range s {
		if !ok(r) {
			return false
		}
	}
	return true
}

// affixRule passes strings that match any of the rule's space-separated values.
func affixRule(match func(s, affix string) bool) Rule {
	return func(_ context.Context, f Field) (bool, error) {
		if f.Value.Kind() != reflect.String {
			return false, nil
		}
		for _, affix := range strings.Fields(f.Param) {
			if match(f.Value.String(), affix) {
				return true, nil
			}
		}
		return false, nil
	}
}

// isInteger accepts integer kinds, whole floats and strings holding a base-10 integer.
func isInteger(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	case reflect.Float32, reflect.Float64:
		n := v.Float()
		return !math.IsInf(n, 0) && n == math.Trunc(n)
	case reflect.String:
		_, err := strconv.ParseInt(strings.TrimSpace(v.String()), 10, 64)
		return err == nil
	}
	return false
}

// isBoolean accepts bools, 0 and 1, and the strings true, false, 1 and 0.
func isBoolean(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Bool:
		return true
	case reflect.String:
		switch v.String() {
		case "true", "false", "1", "0":
			return true
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0 || v.Int() == 1
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() <= 1
	}
	return false
}

var timeType = reflect.TypeFor[time.Time]()

// isDate accepts non-zero time.Time values and strings in layout, or in
// RFC 3339 or 2006-01-02 when layout is empty.
func isDate(v reflect.Value, layout string) bool {
	if v.Type() == timeType {
		return !v.Interface().(time.Time).IsZero()
	}
	if v.Kind() != reflect.String {
		return false
	}
	layouts := []string{time.RFC3339Nano, time.DateOnly}
	if layout != "" {
		layouts = []string{layout}
	}
	for _, l := range layouts {
		if _, err := time.Parse(l, v.String()); err == nil {
			return true
		}
	}
	return false
}

func stringRule(check func(string) bool) Rule {
	return func(_ context.Context, f Field) (bool, error) {
		return f.Value.Kind() == reflect.String && check(f.Value.String()), nil
	}
}

var patterns sync.Map // string -> *regexp.Regexp

func compile(pattern string) (*regexp.Regexp, error) {
	if cached, ok := patterns.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	patterns.Store(pattern, re)
	return re, nil
}

func regexRule(_ context.Context, f Field) (bool, error) {
	re, err := compile(f.Param)
	if err != nil {
		return false, err
	}
	return f.Value.Kind() == reflect.String && re.MatchString(f.Value.String()), nil
}

// sizeRule compares characters for strings, the value for numbers (and for
// strings with the numeric rule) and the length for slices, arrays and maps.
func sizeRule(compare func(size float64, params []float64) bool) Rule {
	return func(_ context.Context, f Field) (bool, error) {
		var params []float64
		for _, field := range strings.Fields(f.Param) {
			value, err := strconv.ParseFloat(field, 64)
			if err != nil {
				return false, err
			}
			params = append(params, value)
		}
		size, ok := sizeOf(f)
		return ok && compare(size, params), nil
	}
}

func sizeOf(f Field) (float64, bool) {
	switch sizeKind(f) {
	case "string":
		return float64(utf8.RuneCountInString(f.Value.String())), true
	case "numeric":
		return number(f.Value)
	case "array":
		return float64(f.Value.Len()), true
	}
	return 0, false
}

func sizeKind(f Field) string {
	switch f.Value.Kind() {
	case reflect.String:
		if f.numeric {
			return "numeric"
		}
		return "string"
	case reflect.Slice, reflect.Array, reflect.Map:
		return "array"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64:
		return "numeric"
	}
	return ""
}

func number(v reflect.Value) (float64, bool) {
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(v.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return float64(v.Uint()), true
	case reflect.Float32, reflect.Float64:
		return v.Float(), !math.IsNaN(v.Float()) && !math.IsInf(v.Float(), 0)
	case reflect.String:
		n, err := strconv.ParseFloat(strings.TrimSpace(v.String()), 64)
		return n, err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
	}
	return 0, false
}

// scalar formats strings, numbers and booleans for the in rule.
func scalar(v reflect.Value) (string, bool) {
	switch v.Kind() {
	case reflect.String:
		return v.String(), true
	case reflect.Bool:
		return strconv.FormatBool(v.Bool()), true
	}
	if sizeKind(Field{Value: v}) == "numeric" {
		return fmt.Sprint(v.Interface()), true
	}
	return "", false
}

var errNoDB = errors.New("unique and exists need Validator.DB")

// count runs SELECT COUNT(*) FROM table WHERE column = value. The param is
// "table.column", or "table" to use the field's key as the column.
func (v *Validator) count(ctx context.Context, f Field) (int64, error) {
	if v.DB == nil {
		return 0, errNoDB
	}
	table, column, ok := strings.Cut(f.Param, ".")
	if !ok {
		column = f.Name[strings.LastIndex(f.Name, ".")+1:]
	}
	if _, ok := scalar(f.Value); !ok {
		return 0, fmt.Errorf("cannot compare %s with a column", f.Value.Type())
	}
	return v.DB.Table(table).Where(column, "=", f.Value.Interface()).Count(ctx)
}
