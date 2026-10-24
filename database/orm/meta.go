package orm

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode"
)

type field struct {
	name, column string
	index        []int
	typ          reflect.Type
}

type meta struct {
	typ                             reflect.Type
	table                           string
	fields                          []*field
	columns                         map[string]*field
	pk                              *field
	autoPK                          bool
	createdAt, updatedAt, deletedAt *field
	relations                       map[string]*relation
}

type cached struct {
	m   *meta
	err error
}

var (
	cache        sync.Map // reflect.Type -> cached
	tablerType   = reflect.TypeFor[Tabler]()
	timeType     = reflect.TypeFor[time.Time]()
	timePtrType  = reflect.TypeFor[*time.Time]()
	nullTimeType = reflect.TypeFor[sql.NullTime]()
)

func metaOf(t reflect.Type) (*meta, error) {
	if t == nil {
		return nil, fmt.Errorf("orm: model must be a struct, got nil")
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if v, ok := cache.Load(t); ok {
		return v.(cached).m, v.(cached).err
	}
	m, err := parse(t)
	v, _ := cache.LoadOrStore(t, cached{m, err})
	return v.(cached).m, v.(cached).err
}

func parse(t reflect.Type) (*meta, error) {
	if t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("orm: model must be a struct, got %s", t)
	}
	m := &meta{typ: t, table: plural(snake(t.Name())), columns: map[string]*field{}, relations: map[string]*relation{}}
	if t.Implements(tablerType) {
		m.table = reflect.Zero(t).Interface().(Tabler).TableName()
	} else if reflect.PointerTo(t).Implements(tablerType) {
		m.table = reflect.New(t).Interface().(Tabler).TableName()
	}
	if m.table == "" {
		return nil, fmt.Errorf("orm: %s has an empty table name", t)
	}
	for _, sf := range reflect.VisibleFields(t) {
		if sf.Anonymous {
			if sf.Type.Kind() == reflect.Pointer {
				return nil, fmt.Errorf("orm: %s embeds pointer %s; embed the struct value", t, sf.Type)
			}
			if sf.Type.Kind() == reflect.Struct {
				continue // promoted fields follow
			}
		}
		if !sf.IsExported() {
			continue
		}
		if tag, ok := sf.Tag.Lookup("orm"); ok {
			r, err := parseRelation(t, sf, tag)
			if err != nil {
				return nil, err
			}
			m.relations[r.name] = r
			continue
		}
		tag := sf.Tag.Get("db")
		if tag == "-" {
			continue
		}
		column, options, _ := strings.Cut(tag, ",")
		if column == "" {
			column = snake(sf.Name)
		}
		if prior, ok := m.columns[column]; ok {
			return nil, fmt.Errorf("orm: %s maps %s and %s to column %q", t, prior.name, sf.Name, column)
		}
		f := &field{name: sf.Name, column: column, index: sf.Index, typ: sf.Type}
		m.fields = append(m.fields, f)
		m.columns[column] = f
		for _, option := range strings.Split(options, ",") {
			switch option {
			case "":
			case "pk":
				if m.pk != nil {
					return nil, fmt.Errorf("orm: %s has more than one primary key", t)
				}
				m.pk = f
			default:
				return nil, fmt.Errorf("orm: %s.%s has unknown db option %q", t, sf.Name, option)
			}
		}
	}
	if m.pk == nil {
		m.pk = m.columns["id"]
	}
	if m.pk == nil {
		return nil, fmt.Errorf("orm: %s has no primary key; add an id column or tag one with `db:\",pk\"`", t)
	}
	switch m.pk.typ.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		m.autoPK = true
	}
	for column, target := range map[string]**field{"created_at": &m.createdAt, "updated_at": &m.updatedAt, "deleted_at": &m.deletedAt} {
		f := m.columns[column]
		if f == nil {
			continue
		}
		if f.typ != timeType && f.typ != timePtrType && f.typ != nullTimeType {
			return nil, fmt.Errorf("orm: %s.%s must be time.Time, *time.Time or sql.NullTime", t, f.name)
		}
		if column == "deleted_at" && f.typ == timeType {
			return nil, fmt.Errorf("orm: %s.%s must be *time.Time or sql.NullTime to allow soft deletes", t, f.name)
		}
		*target = f
	}
	return m, nil
}

// qualified returns table.column for use where joins may make names ambiguous.
func (m *meta) qualified(f *field) string { return m.table + "." + f.column }

// setTime stores t in a timestamp field; a nil t clears nullable fields.
func setTime(v reflect.Value, t *time.Time) {
	switch v.Type() {
	case timeType:
		if t != nil {
			v.Set(reflect.ValueOf(*t))
		}
	case timePtrType:
		if t == nil {
			v.Set(reflect.Zero(timePtrType))
		} else {
			copied := *t
			v.Set(reflect.ValueOf(&copied))
		}
	case nullTimeType:
		if t == nil {
			v.Set(reflect.ValueOf(sql.NullTime{}))
		} else {
			v.Set(reflect.ValueOf(sql.NullTime{Time: *t, Valid: true}))
		}
	}
}

// snake converts Go names to snake_case: UserID -> user_id, HTMLBody -> html_body.
func snake(name string) string {
	runes := []rune(name)
	var b strings.Builder
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) ||
				unicode.IsUpper(runes[i-1]) && i+1 < len(runes) && unicode.IsLower(runes[i+1])) {
				b.WriteByte('_')
			}
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}

// plural applies simple English rules; implement TableName for irregular nouns.
func plural(word string) string {
	switch {
	case word == "":
		return ""
	case strings.HasSuffix(word, "y") && len(word) > 1 && !strings.ContainsRune("aeiou", rune(word[len(word)-2])):
		return word[:len(word)-1] + "ies"
	case strings.HasSuffix(word, "s"), strings.HasSuffix(word, "x"), strings.HasSuffix(word, "z"),
		strings.HasSuffix(word, "ch"), strings.HasSuffix(word, "sh"):
		return word + "es"
	}
	return word + "s"
}
