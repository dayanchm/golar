package orm

import (
	"context"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"github.com/dayanchm/golar/database/query"
)

type trashed int

const (
	withoutTrashed trashed = iota
	withTrashed
	onlyTrashed
)

// Builder queries models of type T. Like query.Builder it is immutable:
// every method returns a copy.
type Builder[T any] struct {
	db          DB
	m           *meta
	q           *query.Builder
	with        []string
	trashed     trashed
	constrained bool // a Where clause was added; guards bulk writes
	allRows     bool
	err         error
}

// Query starts a query for model T on a connection or transaction.
func Query[T any](db DB) *Builder[T] {
	m, err := metaOf(reflect.TypeFor[T]())
	b := &Builder[T]{db: db, m: m, err: err}
	if err == nil && db == nil {
		b.err = fmt.Errorf("orm: connection is required")
	}
	if b.err == nil {
		b.q = db.Table(m.table)
	}
	return b
}

func (b *Builder[T]) clone(edit func(*Builder[T])) *Builder[T] {
	c := *b
	c.with = slices.Clone(b.with)
	if c.err == nil {
		edit(&c)
	}
	return &c
}

func (b *Builder[T]) where(edit func(*query.Builder) *query.Builder) *Builder[T] {
	return b.clone(func(c *Builder[T]) { c.q = edit(c.q); c.constrained = true })
}

// Where appends an AND predicate. See query.Builder.Where.
func (b *Builder[T]) Where(column, operator string, value any) *Builder[T] {
	return b.where(func(q *query.Builder) *query.Builder { return q.Where(column, operator, value) })
}

func (b *Builder[T]) WhereNull(column string) *Builder[T] {
	return b.where(func(q *query.Builder) *query.Builder { return q.WhereNull(column) })
}

func (b *Builder[T]) WhereNotNull(column string) *Builder[T] {
	return b.where(func(q *query.Builder) *query.Builder { return q.WhereNotNull(column) })
}

func (b *Builder[T]) WhereIn(column string, values []any) *Builder[T] {
	return b.where(func(q *query.Builder) *query.Builder { return q.WhereIn(column, values) })
}

func (b *Builder[T]) WhereNotIn(column string, values []any) *Builder[T] {
	return b.where(func(q *query.Builder) *query.Builder { return q.WhereNotIn(column, values) })
}

func (b *Builder[T]) OrderBy(column, direction string) *Builder[T] {
	return b.clone(func(c *Builder[T]) { c.q = c.q.OrderBy(column, direction) })
}

func (b *Builder[T]) Limit(n int) *Builder[T] {
	return b.clone(func(c *Builder[T]) { c.q = c.q.Limit(n) })
}

func (b *Builder[T]) Offset(n int) *Builder[T] {
	return b.clone(func(c *Builder[T]) { c.q = c.q.Offset(n) })
}

// With eager-loads relations, e.g. With("Posts", "Posts.Comments"), using one
// query per relation and nesting level.
func (b *Builder[T]) With(relations ...string) *Builder[T] {
	return b.clone(func(c *Builder[T]) { c.with = append(c.with, relations...) })
}

// WithTrashed includes soft-deleted rows.
func (b *Builder[T]) WithTrashed() *Builder[T] {
	return b.clone(func(c *Builder[T]) { c.trashed = withTrashed })
}

// OnlyTrashed selects soft-deleted rows only.
func (b *Builder[T]) OnlyTrashed() *Builder[T] {
	return b.clone(func(c *Builder[T]) { c.trashed = onlyTrashed })
}

// AllRows allows bulk Update and Delete without a Where clause.
func (b *Builder[T]) AllRows() *Builder[T] {
	return b.clone(func(c *Builder[T]) { c.allRows = true })
}

// scoped applies the soft-delete scope.
func (b *Builder[T]) scoped() *query.Builder {
	q := b.q
	if d := b.m.deletedAt; d != nil {
		switch b.trashed {
		case withoutTrashed:
			q = q.WhereNull(b.m.qualified(d))
		case onlyTrashed:
			q = q.WhereNotNull(b.m.qualified(d))
		}
	}
	return q
}

// Get returns all matching models with requested relations loaded.
func (b *Builder[T]) Get(ctx context.Context) ([]T, error) {
	if b.err != nil {
		return nil, b.err
	}
	rows, _, err := fetch(ctx, b.m, b.scoped(), "", nil)
	if err != nil {
		return nil, fmt.Errorf("orm: query %s: %w", b.m.table, err)
	}
	models := make([]T, len(rows))
	owners := make([]reflect.Value, len(rows))
	for i, row := range rows {
		models[i] = *row.Interface().(*T)
		owners[i] = reflect.ValueOf(&models[i]).Elem()
	}
	if len(b.with) > 0 && len(models) > 0 {
		if err := load(ctx, b.db, b.m, owners, b.with); err != nil {
			return nil, err
		}
	}
	return models, nil
}

// First returns the first matching model or ErrNotFound.
func (b *Builder[T]) First(ctx context.Context) (*T, error) {
	models, err := b.Limit(1).Get(ctx)
	if err != nil {
		return nil, err
	}
	if len(models) == 0 {
		return nil, ErrNotFound
	}
	return &models[0], nil
}

// Find returns the model with primary key id or ErrNotFound.
func (b *Builder[T]) Find(ctx context.Context, id any) (*T, error) {
	if b.err != nil {
		return nil, b.err
	}
	return b.Where(b.m.qualified(b.m.pk), "=", id).First(ctx)
}

// Count returns the number of matching models.
func (b *Builder[T]) Count(ctx context.Context) (int64, error) {
	if b.err != nil {
		return 0, b.err
	}
	return b.scoped().Count(ctx)
}

// Paginate returns one 1-based page of models and its metadata.
func (b *Builder[T]) Paginate(ctx context.Context, page, perPage int) ([]T, query.Pagination, error) {
	paged := b.clone(func(c *Builder[T]) { c.q = c.q.ForPage(page, perPage) })
	if paged.err == nil {
		if _, _, err := paged.q.Compile(); err != nil {
			return nil, query.Pagination{}, err
		}
	}
	total, err := b.Count(ctx)
	if err != nil {
		return nil, query.Pagination{}, err
	}
	models, err := paged.Get(ctx)
	if err != nil {
		return nil, query.Pagination{}, err
	}
	return models, query.NewPagination(page, perPage, total), nil
}

func (b *Builder[T]) writable() (*query.Builder, error) {
	if b.err != nil {
		return nil, b.err
	}
	if !b.constrained && !b.allRows {
		return nil, fmt.Errorf("orm: bulk write on %s requires a where clause; call AllRows to affect every row", b.m.table)
	}
	return b.scoped().AllRows(), nil
}

// Update sets values on all matching models and returns the affected row
// count. updated_at is set unless values contains it.
func (b *Builder[T]) Update(ctx context.Context, values map[string]any) (int64, error) {
	q, err := b.writable()
	if err != nil {
		return 0, err
	}
	values = maps.Clone(values)
	if u := b.m.updatedAt; u != nil && len(values) > 0 {
		if _, ok := values[u.column]; !ok {
			values[u.column] = now()
		}
	}
	return affected(q.UpdateContext(ctx, values))
}

// Delete soft-deletes matching models when T supports it, otherwise removes them.
func (b *Builder[T]) Delete(ctx context.Context) (int64, error) {
	q, err := b.writable()
	if err != nil {
		return 0, err
	}
	if d := b.m.deletedAt; d != nil {
		return affected(q.UpdateContext(ctx, map[string]any{d.column: now()}))
	}
	return affected(q.DeleteContext(ctx))
}

// ForceDelete removes matching rows even when T supports soft deletes.
func (b *Builder[T]) ForceDelete(ctx context.Context) (int64, error) {
	q, err := b.writable()
	if err != nil {
		return 0, err
	}
	return affected(q.DeleteContext(ctx))
}

// Restore clears deleted_at on matching soft-deleted models.
func (b *Builder[T]) Restore(ctx context.Context) (int64, error) {
	if b.err == nil && b.m.deletedAt == nil {
		return 0, fmt.Errorf("orm: %s does not support soft deletes", b.m.typ)
	}
	q, err := b.OnlyTrashed().writable()
	if err != nil {
		return 0, err
	}
	return affected(q.UpdateContext(ctx, map[string]any{b.m.deletedAt.column: nil}))
}

func affected(result interface{ RowsAffected() (int64, error) }, err error) (int64, error) {
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
