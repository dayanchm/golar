package query

import (
	"context"
	"database/sql"
	"fmt"
	"math"
)

// Pagination describes one page of results.
type Pagination struct {
	Page, PerPage int
	Total         int64
	LastPage      int // at least 1, even when Total is zero
}

// NewPagination computes the last page for total rows split into perPage-sized pages.
func NewPagination(page, perPage int, total int64) Pagination {
	lastPage := 1
	if perPage > 0 {
		if pages := (total + int64(perPage) - 1) / int64(perPage); pages > 1 {
			lastPage = int(min(pages, math.MaxInt))
		}
	}
	return Pagination{Page: page, PerPage: perPage, Total: total, LastPage: lastPage}
}

// HasMorePages reports whether a page follows this one.
func (p Pagination) HasMorePages() bool { return p.Page < p.LastPage }

// ForPage replaces Limit and Offset with the window for a 1-based page.
func (b *Builder) ForPage(page, perPage int) *Builder {
	c := b.clone()
	if c.err != nil {
		return c
	}
	if page < 1 || perPage < 1 {
		c.err = fmt.Errorf("query: page and per-page must be at least 1")
		return c
	}
	if page-1 > math.MaxInt/perPage {
		c.err = fmt.Errorf("query: page %d is out of range", page)
		return c
	}
	limit, offset := perPage, (page-1)*perPage
	c.limit, c.offset = &limit, &offset
	return c
}

// Paginate counts all matching rows, then queries one page. The caller must
// close the rows. Count and page run as two statements; use a transaction
// builder (tx.Table) when they must see the same snapshot.
func (b *Builder) Paginate(ctx context.Context, page, perPage int) (*sql.Rows, Pagination, error) {
	paged := b.ForPage(page, perPage)
	if _, _, err := paged.Compile(); err != nil {
		return nil, Pagination{}, err
	}
	total, err := b.Count(ctx)
	if err != nil {
		return nil, Pagination{}, err
	}
	rows, err := paged.QueryContext(ctx)
	if err != nil {
		return nil, Pagination{}, err
	}
	return rows, NewPagination(page, perPage, total), nil
}
