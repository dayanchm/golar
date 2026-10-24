// Package orm maps Go structs to tables on top of the query builder.
//
// A model is a struct whose exported fields are columns. Columns default to the
// snake_case field name and can be renamed with a `db:"name"` tag; `db:"-"`
// skips a field. The primary key is the field tagged `db:",pk"` or the "id"
// column. Tables default to the plural snake_case type name; implement
// TableName to override it. Relationship fields carry an `orm` tag.
package orm

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/dayanchm/golar/database/query"
)

// DB is implemented by *database.DB and *database.Tx.
type DB interface {
	Table(name string) *query.Builder
}

// Tabler overrides a model's table name.
type Tabler interface {
	TableName() string
}

// ErrNotFound is returned when Find or First matches no row. It wraps sql.ErrNoRows.
var ErrNotFound = fmt.Errorf("orm: record not found: %w", sql.ErrNoRows)

// ErrMissingPrimaryKey is returned when a model operation needs a primary key value.
var ErrMissingPrimaryKey = errors.New("orm: model has no primary key value")

// Model provides an auto-increment ID and timestamps that Create and Save maintain.
type Model struct {
	ID        int64     `db:"id,pk"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// SoftDeletes makes Delete set deleted_at instead of removing the row.
type SoftDeletes struct {
	DeletedAt *time.Time `db:"deleted_at"`
}

// Info describes a model's mapping.
type Info struct {
	Table, PrimaryKey string
	Columns           []string
	Relations         []string
	SoftDeletes       bool
}

// Register validates models and their relationships up front, so mapping
// errors surface at startup instead of on the first query. Models are also
// parsed lazily on first use; registration is optional.
func Register(models ...any) error {
	for _, model := range models {
		m, err := metaOf(reflect.TypeOf(model))
		if err != nil {
			return err
		}
		for _, r := range m.relations {
			if _, err := r.resolve(m); err != nil {
				return err
			}
		}
	}
	return nil
}

// Describe returns the mapping for model, which may be a struct or a pointer to one.
func Describe(model any) (Info, error) {
	m, err := metaOf(reflect.TypeOf(model))
	if err != nil {
		return Info{}, err
	}
	info := Info{Table: m.table, PrimaryKey: m.pk.column, SoftDeletes: m.deletedAt != nil}
	for _, f := range m.fields {
		info.Columns = append(info.Columns, f.column)
	}
	for name := range m.relations {
		info.Relations = append(info.Relations, name)
	}
	slices.Sort(info.Relations)
	return info, nil
}

// now is the clock used for timestamps; Postgres stores microseconds.
var now = func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
