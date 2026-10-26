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
	numeric  bool // the field has the numeric rule, so sizes compare values
	rules    []boundRule
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
		rules, err := v.parseTag(tag)
		if err != nil {
			return nil, fmt.Errorf("validation: %s.%s: %w", t, sf.Name, err)
		}
		if field.embedded && len(rules) > 0 {
			return nil, fmt.Errorf("validation: %s.%s: embedded structs cannot have rules", t, sf.Name)
		}
		for _, rule := range rules {
			field.numeric = field.numeric || rule.name == "numeric"
		}
		field.rules = rules
		if len(rules) > 0 || field.embedded || indirectType(sf.Type).Kind() == reflect.Struct {
			plan.fields = append(plan.fields, field)
		}
	}
	actual, _ := v.plans.LoadOrStore(t, plan)
	return actual.(*structPlan), nil
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
	case "in":
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
	case "required", "email", "numeric", "url", "uuid":
		if param != "" {
			return fmt.Errorf("takes no parameter")
		}
	}
	return nil
}
