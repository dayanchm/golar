package validation

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

type structPlan struct {
	fields []fieldPlan
	names  map[string]int // JSON and Go names of every field, for Sibling
}

type fieldPlan struct {
	index    int
	key      string // json or form name, or the Go name without either tag
	embedded bool
	numeric  bool // the field has the numeric or integer rule, so sizes compare values
	rules    []boundRule
	// each holds the rules after "each", applied to every element of a slice,
	// array or map; eachNumeric is numeric for those rules.
	each        []boundRule
	eachNumeric bool
	// elements is set for slices, arrays and maps whose elements are structs.
	elements bool
}

type boundRule struct {
	name, param string
	rule        registeredRule
}

// plan parses t's tags once. Nested struct types are planned when first reached.
func (v *Validator) plan(t reflect.Type) (*structPlan, error) {
	if cached, ok := v.plans.Load(t); ok {
		return cached.(*structPlan), nil
	}
	plan := &structPlan{names: map[string]int{}}
	for i := range t.NumField() {
		sf := t.Field(i)
		if !sf.IsExported() && !sf.Anonymous {
			continue
		}
		name, _, _ := strings.Cut(sf.Tag.Get("json"), ",")
		if name == "" {
			name, _, _ = strings.Cut(sf.Tag.Get("form"), ",")
		}
		field := fieldPlan{index: i, key: name}
		if name == "" || name == "-" {
			field.key = sf.Name
		}
		plan.names[sf.Name] = i
		plan.names[field.key] = i
		if sf.Anonymous && name == "" {
			if !sf.IsExported() && indirectType(sf.Type).Kind() != reflect.Struct {
				continue
			}
			field.embedded = true
		}
		tag := sf.Tag.Get("validate")
		if tag == "-" {
			continue
		}
		own, each, hasEach := splitEach(tag)
		rules, err := v.parseTag(own)
		if err != nil {
			return nil, fmt.Errorf("validation: %s.%s: %w", t, sf.Name, err)
		}
		elementRules, err := v.parseTag(each)
		if err != nil {
			return nil, fmt.Errorf("validation: %s.%s: each: %w", t, sf.Name, err)
		}
		if field.embedded && (len(rules) > 0 || hasEach) {
			return nil, fmt.Errorf("validation: %s.%s: embedded structs cannot have rules", t, sf.Name)
		}
		switch indirectType(sf.Type).Kind() {
		case reflect.Slice, reflect.Array, reflect.Map:
			field.elements = indirectType(indirectType(sf.Type).Elem()).Kind() == reflect.Struct
		default:
			if hasEach {
				return nil, fmt.Errorf("validation: %s.%s: each needs a slice, array or map", t, sf.Name)
			}
		}
		for _, rule := range rules {
			field.numeric = field.numeric || rule.name == "numeric" || rule.name == "integer"
		}
		for _, rule := range elementRules {
			field.eachNumeric = field.eachNumeric || rule.name == "numeric" || rule.name == "integer"
		}
		field.rules, field.each = rules, elementRules
		if len(rules) > 0 || len(elementRules) > 0 || field.embedded || field.elements || indirectType(sf.Type).Kind() == reflect.Struct {
			plan.fields = append(plan.fields, field)
		}
	}
	actual, _ := v.plans.LoadOrStore(t, plan)
	return actual.(*structPlan), nil
}

// splitEach splits a tag at the "each" rule into the field's own rules and the
// element rules. A regex rule takes the rest of the tag, so each must come first.
func splitEach(tag string) (own, each string, found bool) {
	offset, rest := 0, tag
	for rest != "" {
		if strings.HasPrefix(strings.TrimSpace(rest), "regex=") {
			break
		}
		part, after, more := strings.Cut(rest, ",")
		if strings.TrimSpace(part) == "each" {
			return tag[:offset], after, true
		}
		if !more {
			break
		}
		offset += len(part) + 1
		rest = after
	}
	return tag, "", false
}

func indirectType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

// parseTag splits rules on commas. regex takes the rest of the tag, so its
// pattern may contain commas, and must therefore come last.
func (v *Validator) parseTag(tag string) ([]boundRule, error) {
	var rules []boundRule
	for tag != "" {
		var part string
		if strings.HasPrefix(strings.TrimSpace(tag), "regex=") {
			part, tag = strings.TrimSpace(tag), ""
		} else {
			part, tag, _ = strings.Cut(tag, ",")
			part = strings.TrimSpace(part)
		}
		if part == "" {
			continue
		}
		name, param, _ := strings.Cut(part, "=")
		v.mu.RLock()
		rule, ok := v.rules[name]
		v.mu.RUnlock()
		if !ok {
			return nil, fmt.Errorf("unknown rule %q", name)
		}
		if err := checkParam(name, param); err != nil {
			return nil, fmt.Errorf("rule %q: %w", name, err)
		}
		rules = append(rules, boundRule{name: name, param: param, rule: rule})
	}
	return rules, nil
}

// checkParam rejects malformed parameters of built-in rules when the type is first used.
func checkParam(name, param string) error {
	numbers := func(want int) error {
		fields := strings.Fields(param)
		if len(fields) != want {
			return fmt.Errorf("want %d number(s), got %q", want, param)
		}
		for _, field := range fields {
			if _, err := strconv.ParseFloat(field, 64); err != nil {
				return fmt.Errorf("invalid number %q", field)
			}
		}
		return nil
	}
	switch name {
	case "min", "max", "len":
		return numbers(1)
	case "between":
		return numbers(2)
	case "in", "starts_with", "ends_with":
		if len(strings.Fields(param)) == 0 {
			return fmt.Errorf("needs space-separated values")
		}
	case "regex":
		_, err := compile(param)
		return err
	case "unique", "exists":
		if param == "" {
			return fmt.Errorf("needs a table, as in %s=users.email", name)
		}
	case "required", "email", "numeric", "url", "uuid", "string", "integer", "boolean", "alpha", "alpha_num", "alpha_dash":
		if param != "" {
			return fmt.Errorf("takes no parameter")
		}
	}
	return nil
}
