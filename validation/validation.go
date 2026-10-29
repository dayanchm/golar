// Package validation checks structs against rules declared in `validate` tags.
//
//	type RegisterRequest struct {
//		Name     string `json:"name" validate:"required,max=100"`
//		Email    string `json:"email" validate:"required,email,unique=users.email"`
//		Password string `json:"password" validate:"required,min=8,confirmed"`
//	}
//
// Rules are parsed once per struct type and cached.
package validation

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dayanchm/golar/database/query"
)

// Errors maps field names, as they appear in JSON, to their failure messages.
type Errors map[string][]string

// Error reports input that failed validation. Its messages are safe to show to clients.
type Error struct{ Errors Errors }

func (e *Error) Error() string {
	fields := make([]string, 0, len(e.Errors))
	for field := range e.Errors {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, field+": "+strings.Join(e.Errors[field], " "))
	}
	return "validation failed: " + strings.Join(parts, "; ")
}

// DB runs the queries of the unique and exists rules. *database.DB and *database.Tx implement it.
type DB interface {
	Table(name string) *query.Builder
}

// Field is the value a rule checks.
type Field struct {
	// Name is the error key, such as "email" or "address.city".
	Name string
	// Attribute is the human-readable name used in messages.
	Attribute string
	// Value is the field's value with pointers dereferenced. It is invalid for nil pointers.
	Value reflect.Value
	// Param is the text after "=" in the tag, such as "8" in "min=8".
	Param string
	// Parent is the struct that contains the field.
	Parent reflect.Value

	plan    *structPlan
	numeric bool
	pointer bool // Value was reached through a pointer, so zero numbers count as present
}

// Sibling returns another field of the same struct by its JSON or Go name.
func (f Field) Sibling(name string) (reflect.Value, bool) {
	index, ok := f.plan.names[name]
	if !ok {
		return reflect.Value{}, false
	}
	return indirect(f.Parent.Field(index))
}

// Rule reports whether field passes. A non-nil error aborts validation, for
// example when a database query fails.
type Rule func(ctx context.Context, field Field) (bool, error)

// Validator holds rules and messages. Configure it before first use; it is
// safe for concurrent validation afterwards.
type Validator struct {
	// DB backs the unique and exists rules.
	DB DB
	// Messages overrides messages by "field.rule", "rule.kind" or "rule" keys,
	// for example "email.unique", "min.string" or "required". Messages may use
	// :attribute, :param, :min, :max and :values.
	Messages map[string]string
	// Attributes replaces field names in messages, keyed by error key.
	Attributes map[string]string

	mu      sync.RWMutex
	rules   map[string]registeredRule
	plans   sync.Map // reflect.Type -> *structPlan
	locales map[string]*locale
}

// pass is the state of one validation run.
type pass struct {
	errs   Errors
	locale *locale // nil for English
}

type registeredRule struct {
	rule    Rule
	message string
}

// New returns a validator with the built-in rules.
func New() *Validator {
	v := &Validator{Messages: map[string]string{}, Attributes: map[string]string{}, rules: map[string]registeredRule{}, locales: map[string]*locale{}}
	v.AddLocale("tr", turkish, nil)
	for name, rule := range builtinRules(v) {
		v.rules[name] = registeredRule{rule: rule}
	}
	return v
}

// Default is used by the package-level functions.
var Default = New()

// Validate checks v, a struct or a pointer to one, with Default.
func Validate(v any) error { return Default.ValidateContext(context.Background(), v) }

// ValidateContext checks v with Default. ctx is passed to rules such as unique.
func ValidateContext(ctx context.Context, v any) error { return Default.ValidateContext(ctx, v) }

// Register adds a rule to Default. See Validator.Register.
func Register(name, message string, rule Rule) { Default.Register(name, message, rule) }

var ruleName = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

// Register adds or replaces a rule. message is used when no entry in Messages matches.
func (v *Validator) Register(name, message string, rule Rule) {
	if !ruleName.MatchString(name) || rule == nil {
		panic(fmt.Sprintf("validation: invalid rule %q", name))
	}
	v.mu.Lock()
	v.rules[name] = registeredRule{rule: rule, message: message}
	v.mu.Unlock()
	v.plans.Clear()
}

// Validate checks v without a request context.
func (v *Validator) Validate(value any) error { return v.ValidateContext(context.Background(), value) }

// ValidateContext returns nil when value passes, *Error when it fails, and any
// other error for invalid tags or failing rules.
func (v *Validator) ValidateContext(ctx context.Context, value any) error {
	rv := reflect.ValueOf(value)
	for rv.Kind() == reflect.Pointer && !rv.IsNil() {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return fmt.Errorf("validation: expected a struct, got %T", value)
	}
	_, l := v.lookupLocale(LocaleFrom(ctx))
	p := &pass{errs: Errors{}, locale: l}
	if err := v.check(ctx, rv, "", p, 0); err != nil {
		return err
	}
	if len(p.errs) > 0 {
		return &Error{Errors: p.errs}
	}
	return nil
}

const maxDepth = 32

func (v *Validator) check(ctx context.Context, rv reflect.Value, prefix string, p *pass, depth int) error {
	if depth > maxDepth {
		return fmt.Errorf("validation: %s nests more than %d structs", rv.Type(), maxDepth)
	}
	plan, err := v.plan(rv.Type())
	if err != nil {
		return err
	}
	for _, field := range plan.fields {
		raw := rv.Field(field.index)
		value, present := indirect(raw)
		if field.embedded {
			// Embedded structs share the outer struct's keys, as in encoding/json.
			if present && value.Kind() == reflect.Struct {
				if err := v.check(ctx, value, prefix, p, depth+1); err != nil {
					return err
				}
			}
			continue
		}
		key := prefix + field.key
		f := Field{Name: key, Attribute: v.attribute(key, p.locale), Value: value, Parent: rv, plan: plan, numeric: field.numeric,
			pointer: raw.Kind() == reflect.Pointer || raw.Kind() == reflect.Interface}
		// Like Laravel, only required runs on missing or empty values.
		missing := f.missing()
		for _, bound := range field.rules {
			if missing && bound.name != "required" {
				continue
			}
			f.Param = bound.param
			ok, err := bound.rule.rule(ctx, f)
			if err != nil {
				return fmt.Errorf("validation: %s %s: %w", key, bound.name, err)
			}
			if !ok {
				p.errs[key] = append(p.errs[key], v.message(f, bound, p.locale))
			}
		}
		if !present || len(p.errs[key]) > 0 {
			continue
		}
		switch value.Kind() {
		case reflect.Struct:
			if err := v.check(ctx, value, key+".", p, depth+1); err != nil {
				return err
			}
		case reflect.Slice, reflect.Array, reflect.Map:
			if len(field.each) > 0 || field.elements {
				if err := v.checkElements(ctx, value, key, field, rv, plan, p, depth); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// checkElements validates each element of a slice, array or map under keys
// such as "items.0" or "labels.en": first the each rules, then, for structs,
// their own fields.
func (v *Validator) checkElements(ctx context.Context, collection reflect.Value, key string, field fieldPlan, parent reflect.Value, plan *structPlan, p *pass, depth int) error {
	type element struct {
		key   string
		value reflect.Value
	}
	var elements []element
	if collection.Kind() == reflect.Map {
		keys := collection.MapKeys()
		sort.Slice(keys, func(i, j int) bool { return fmt.Sprint(keys[i].Interface()) < fmt.Sprint(keys[j].Interface()) })
		for _, k := range keys {
			elements = append(elements, element{key + "." + fmt.Sprint(k.Interface()), collection.MapIndex(k)})
		}
	} else {
		for i := range collection.Len() {
			elements = append(elements, element{key + "." + strconv.Itoa(i), collection.Index(i)})
		}
	}
	for _, e := range elements {
		value, present := indirect(e.value)
		f := Field{Name: e.key, Attribute: v.elementAttribute(e.key, p.locale), Value: value, Parent: parent, plan: plan, numeric: field.eachNumeric,
			pointer: e.value.Kind() == reflect.Pointer || e.value.Kind() == reflect.Interface}
		missing := f.missing()
		for _, bound := range field.each {
			if missing && bound.name != "required" {
				continue
			}
			f.Param = bound.param
			ok, err := bound.rule.rule(ctx, f)
			if err != nil {
				return fmt.Errorf("validation: %s %s: %w", e.key, bound.name, err)
			}
			if !ok {
				p.errs[e.key] = append(p.errs[e.key], v.message(f, bound, p.locale))
			}
		}
		if present && value.Kind() == reflect.Struct && len(p.errs[e.key]) == 0 {
			if err := v.check(ctx, value, e.key+".", p, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// wildcard replaces numeric key segments with "*", so "items.0.name" can be
// configured as "items.*.name".
func wildcard(key string) string {
	parts := strings.Split(key, ".")
	for i, part := range parts {
		if _, err := strconv.Atoi(part); err == nil {
			parts[i] = "*"
		}
	}
	return strings.Join(parts, ".")
}

// attribute names a field in messages: Attributes by key or wildcard key,
// else the last key segment.
func (v *Validator) attribute(key string, l *locale) string {
	if name, ok := v.configuredAttribute(key, l); ok {
		return name
	}
	return strings.ReplaceAll(key[strings.LastIndex(key, ".")+1:], "_", " ")
}

// elementAttribute names a collection element with its parent, as in "tags.0".
func (v *Validator) elementAttribute(key string, l *locale) string {
	if name, ok := v.configuredAttribute(key, l); ok {
		return name
	}
	segments := strings.Split(key, ".")
	if len(segments) > 1 {
		segments = segments[len(segments)-2:]
	}
	return strings.ReplaceAll(strings.Join(segments, "."), "_", " ")
}

// configuredAttribute looks up Attributes, preferring the locale's names.
func (v *Validator) configuredAttribute(key string, l *locale) (string, bool) {
	for _, k := range []string{key, wildcard(key)} {
		if l != nil {
			if name, ok := l.attributes[k]; ok {
				return name, true
			}
		}
		if name, ok := v.Attributes[k]; ok {
			return name, true
		}
	}
	return "", false
}

// message finds the text for a failed rule: the locale's entries, then
// Messages, the rule's registered message and the English defaults. Each
// source is searched from the most specific key ("email.unique") to the rule
// name ("unique"). The locale's "invalid" entry replaces the generic message.
func (v *Validator) message(f Field, bound boundRule, l *locale) string {
	kind := sizeKind(f)
	keys := []string{f.Name + "." + bound.name, wildcard(f.Name) + "." + bound.name, bound.name + "." + kind, bound.name}
	text := ""
	if l != nil {
		for _, key := range keys {
			if message, ok := l.messages[key]; ok {
				text = message
				break
			}
		}
	}
	for _, key := range keys {
		if text != "" {
			break
		}
		if message, ok := v.Messages[key]; ok {
			text = message
		}
	}
	if text == "" {
		text = bound.rule.message
	}
	if text == "" {
		if message, ok := defaultMessages[bound.name+"."+kind]; ok {
			text = message
		} else if message, ok := defaultMessages[bound.name]; ok {
			text = message
		} else if message, ok := l.lookup("invalid"); ok {
			text = message
		} else {
			text = "The :attribute field is invalid."
		}
	}
	low, high, _ := strings.Cut(f.Param, " ")
	return strings.NewReplacer(
		":attribute", f.Attribute,
		":values", strings.Join(strings.Fields(f.Param), ", "),
		":param", f.Param,
		":min", low,
		":max", high,
	).Replace(text)
}

// indirect follows pointers and interfaces, reporting false for nil.
func indirect(v reflect.Value) (reflect.Value, bool) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return reflect.Value{}, false
		}
		v = v.Elem()
	}
	return v, v.IsValid()
}

// missing reports whether the field fails required: nil, blank strings, empty
// collections, and zero values not behind a pointer (use *int to accept 0).
func (f Field) missing() bool {
	v := f.Value
	switch {
	case !v.IsValid():
		return true
	case v.Kind() == reflect.String:
		return strings.TrimSpace(v.String()) == ""
	case v.Kind() == reflect.Slice || v.Kind() == reflect.Map || v.Kind() == reflect.Array:
		return v.Len() == 0
	default:
		return !f.pointer && v.IsZero()
	}
}
