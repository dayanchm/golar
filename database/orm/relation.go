package orm

import (
	"context"
	"database/sql/driver"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"

	"github.com/dayanchm/golar/database/query"
)

const (
	hasOne        = "hasOne"
	hasMany       = "hasMany"
	belongsTo     = "belongsTo"
	belongsToMany = "belongsToMany"
)

// keyChunk bounds IN lists below every supported database's parameter limit.
const keyChunk = 500

var relationOptions = map[string][]string{
	hasOne:        {"foreignKey", "localKey"},
	hasMany:       {"foreignKey", "localKey"},
	belongsTo:     {"foreignKey", "ownerKey"},
	belongsToMany: {"pivot", "foreignPivotKey", "relatedPivotKey", "localKey", "relatedKey"},
}

type relation struct {
	name, kind string
	index      []int
	typ        reflect.Type // field type: *T or []T
	target     reflect.Type // T
	options    map[string]string

	once     sync.Once
	resolved *resolved
	err      error
}

// resolved holds keys validated against both models. For hasOne/hasMany,
// local is on the owner and foreign on the target; for belongsTo, foreign is
// on the owner and local (the owner key) on the target.
type resolved struct {
	target                      *meta
	local, foreign              *field
	pivot, pivotLocal, pivotRel string
}

func parseRelation(owner reflect.Type, sf reflect.StructField, tag string) (*relation, error) {
	parts := strings.Split(tag, ",")
	r := &relation{name: sf.Name, kind: parts[0], index: sf.Index, typ: sf.Type, options: map[string]string{}}
	allowed, ok := relationOptions[r.kind]
	if !ok {
		return nil, fmt.Errorf("orm: %s.%s has unknown relation %q", owner, sf.Name, r.kind)
	}
	for _, part := range parts[1:] {
		key, value, ok := strings.Cut(part, "=")
		if !ok || value == "" || !slices.Contains(allowed, key) {
			return nil, fmt.Errorf("orm: %s.%s has invalid %s option %q", owner, sf.Name, r.kind, part)
		}
		r.options[key] = value
	}
	switch {
	case (r.kind == hasOne || r.kind == belongsTo) && sf.Type.Kind() == reflect.Pointer && sf.Type.Elem().Kind() == reflect.Struct:
	case (r.kind == hasMany || r.kind == belongsToMany) && sf.Type.Kind() == reflect.Slice && sf.Type.Elem().Kind() == reflect.Struct:
	default:
		want := "*Model"
		if r.kind == hasMany || r.kind == belongsToMany {
			want = "[]Model"
		}
		return nil, fmt.Errorf("orm: %s.%s is a %s relation and must be a %s field, got %s", owner, sf.Name, r.kind, want, sf.Type)
	}
	r.target = sf.Type.Elem()
	return r, nil
}

func (r *relation) option(name, fallback string) string {
	if value := r.options[name]; value != "" {
		return value
	}
	return fallback
}

// resolve parses the target lazily so models may reference each other.
func (r *relation) resolve(owner *meta) (*resolved, error) {
	r.once.Do(func() { r.resolved, r.err = r.doResolve(owner) })
	return r.resolved, r.err
}

func (r *relation) doResolve(owner *meta) (*resolved, error) {
	target, err := metaOf(r.target)
	if err != nil {
		return nil, err
	}
	column := func(m *meta, name, role string) (*field, error) {
		if f := m.columns[name]; f != nil {
			return f, nil
		}
		return nil, fmt.Errorf("orm: %s.%s: %s column %q not found on %s", owner.typ, r.name, role, name, m.typ)
	}
	res := &resolved{target: target}
	ownerName, targetName := snake(owner.typ.Name()), snake(target.typ.Name())
	switch r.kind {
	case hasOne, hasMany:
		if res.local, err = column(owner, r.option("localKey", owner.pk.column), "local key"); err != nil {
			return nil, err
		}
		res.foreign, err = column(target, r.option("foreignKey", ownerName+"_"+owner.pk.column), "foreign key")
	case belongsTo:
		if res.foreign, err = column(owner, r.option("foreignKey", snake(r.name)+"_"+target.pk.column), "foreign key"); err != nil {
			return nil, err
		}
		res.local, err = column(target, r.option("ownerKey", target.pk.column), "owner key")
	case belongsToMany:
		if res.local, err = column(owner, r.option("localKey", owner.pk.column), "local key"); err != nil {
			return nil, err
		}
		if res.foreign, err = column(target, r.option("relatedKey", target.pk.column), "related key"); err != nil {
			return nil, err
		}
		names := []string{ownerName, targetName}
		slices.Sort(names)
		res.pivot = r.option("pivot", strings.Join(names, "_"))
		res.pivotLocal = r.option("foreignPivotKey", ownerName+"_"+owner.pk.column)
		res.pivotRel = r.option("relatedPivotKey", targetName+"_"+target.pk.column)
	}
	return res, err
}

// load eager-loads dotted relation paths onto addressable owner structs with
// one query per relation and level, whatever the number of owners.
func load(ctx context.Context, db DB, m *meta, owners []reflect.Value, paths []string) error {
	if db == nil {
		return fmt.Errorf("orm: connection is required")
	}
	var order []string
	nested := map[string][]string{}
	for _, path := range paths {
		name, rest, _ := strings.Cut(path, ".")
		if _, seen := nested[name]; !seen {
			order = append(order, name)
			nested[name] = nil
		}
		if rest != "" {
			nested[name] = append(nested[name], rest)
		}
	}
	for _, name := range order {
		r := m.relations[name]
		if r == nil {
			return fmt.Errorf("orm: %s has no relation %q", m.typ, name)
		}
		res, err := r.resolve(m)
		if err != nil {
			return err
		}
		children, err := r.load(ctx, db, m, res, owners)
		if err != nil {
			return err
		}
		if len(nested[name]) > 0 && len(children) > 0 {
			if err := load(ctx, db, res.target, children, nested[name]); err != nil {
				return err
			}
		}
	}
	return nil
}

// load fills relation r on owners and returns the addressable loaded children.
func (r *relation) load(ctx context.Context, db DB, owner *meta, res *resolved, owners []reflect.Value) ([]reflect.Value, error) {
	target := res.target
	ownerKey, childKey := res.local, res.foreign // hasOne, hasMany and belongsToMany
	if r.kind == belongsTo {
		ownerKey, childKey = res.foreign, res.local
	}
	keys, seen := []any{}, map[any]bool{}
	for _, o := range owners {
		if k, ok := keyOf(o.FieldByIndex(ownerKey.index)); ok && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	base := db.Table(target.table).OrderBy(target.qualified(target.pk), "asc")
	filter, extra := target.qualified(childKey), ""
	if r.kind == belongsToMany {
		base = base.Join(res.pivot, res.pivot+"."+res.pivotRel, "=", target.qualified(childKey))
		filter = res.pivot + "." + res.pivotLocal
		extra = filter
	}
	if target.deletedAt != nil {
		base = base.WhereNull(target.qualified(target.deletedAt))
	}
	groups := map[any][]reflect.Value{}
	for chunk := range slices.Chunk(keys, keyChunk) {
		rows, pivotKeys, err := fetch(ctx, target, base.WhereIn(filter, chunk), extra, ownerKey.typ)
		if err != nil {
			return nil, fmt.Errorf("orm: load %s.%s: %w", owner.typ.Name(), r.name, err)
		}
		for i, row := range rows {
			var k any
			var ok bool
			if r.kind == belongsToMany {
				k, ok = keyOf(pivotKeys[i])
			} else {
				k, ok = keyOf(row.Elem().FieldByIndex(childKey.index))
			}
			if ok {
				groups[k] = append(groups[k], row)
			}
		}
	}
	var children []reflect.Value
	added := map[uintptr]bool{}
	for _, o := range owners {
		k, _ := keyOf(o.FieldByIndex(ownerKey.index))
		matches := groups[k]
		dest := o.FieldByIndex(r.index)
		if r.kind == hasMany || r.kind == belongsToMany {
			slice := reflect.MakeSlice(r.typ, len(matches), len(matches))
			for i, match := range matches {
				slice.Index(i).Set(match.Elem())
				children = append(children, slice.Index(i))
			}
			dest.Set(slice)
			continue
		}
		dest.Set(reflect.Zero(r.typ))
		if len(matches) > 0 {
			dest.Set(matches[0])
			if p := matches[0].Pointer(); !added[p] { // belongsTo owners share one parent pointer
				added[p] = true
				children = append(children, matches[0].Elem())
			}
		}
	}
	return children, nil
}

// fetch selects a model's columns, plus an optional extra column scanned as
// extraType, and returns pointers to new structs.
func fetch(ctx context.Context, m *meta, b *query.Builder, extra string, extraType reflect.Type) ([]reflect.Value, []reflect.Value, error) {
	columns := make([]string, len(m.fields), len(m.fields)+1)
	for i, f := range m.fields {
		columns[i] = m.qualified(f)
	}
	if extra != "" {
		columns = append(columns, extra)
	}
	rows, err := b.Select(columns...).QueryContext(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var values, extras []reflect.Value
	for rows.Next() {
		v := reflect.New(m.typ)
		dest := make([]any, len(columns))
		for i, f := range m.fields {
			dest[i] = v.Elem().FieldByIndex(f.index).Addr().Interface()
		}
		if extra != "" {
			x := reflect.New(extraType)
			dest[len(dest)-1] = x.Interface()
			extras = append(extras, x.Elem())
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return values, extras, rows.Close()
}

// keyOf normalizes key values so int and int64 or []byte and string match.
// NULL keys (nil pointers, invalid sql.Null* values) report false.
func keyOf(v reflect.Value) (any, bool) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil, false
		}
		v = v.Elem()
	}
	if valuer, ok := v.Interface().(driver.Valuer); ok {
		value, err := valuer.Value()
		if err != nil || value == nil {
			return nil, false
		}
		v = reflect.ValueOf(value)
	}
	switch v.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if u := v.Uint(); u <= math.MaxInt64 {
			return int64(u), true
		}
		return v.Uint(), true
	case reflect.String:
		return v.String(), true
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return string(v.Bytes()), true
		}
		return nil, false
	}
	if v.Comparable() {
		return v.Interface(), true
	}
	return nil, false
}
