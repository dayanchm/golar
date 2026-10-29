// Package resource turns models into JSON API responses. A resource is an
// explicit function from a model to an Object, so only the fields it names
// are ever sent; adding a column or struct field never leaks it.
//
//	func UserResource(ctx context.Context, u *models.User) resource.Object {
//		return resource.Object{
//			"id":    u.ID,
//			"name":  u.Name,
//			"email": resource.When(auth.User(ctx) != nil, u.Email),
//		}
//	}
//
//	return c.JSON(http.StatusOK, resource.Item(c.Context(), user, UserResource))
package resource

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"

	"github.com/dayanchm/golar/database/query"
)

// Object is a JSON object. Fields whose value is Missing, such as When with a
// false condition, are left out when it is encoded. Nest Objects, not plain
// maps, to drop Missing fields at every level.
type Object map[string]any

type missing struct{}

// Missing is a field value that leaves the field out of the encoded Object.
var Missing any = missing{}

// MarshalJSON encodes the fields that are not Missing.
func (o Object) MarshalJSON() ([]byte, error) {
	present := make(map[string]any, len(o))
	for key, value := range o {
		if _, skip := value.(missing); !skip {
			present[key] = value
		}
	}
	return json.Marshal(present)
}

// When returns value if condition holds and Missing otherwise.
func When(condition bool, value any) any {
	if condition {
		return value
	}
	return Missing
}

// WhenFunc is When for values that are costly to build: value runs only when
// condition holds.
func WhenFunc(condition bool, value func() any) any {
	if condition {
		return value()
	}
	return Missing
}

// WhenNotNil returns *value, or Missing for a nil pointer, such as an
// optional relation that was not loaded.
func WhenNotNil[T any](value *T) any {
	if value == nil {
		return Missing
	}
	return *value
}

// MergeWhen returns o with the fields of other added when condition holds.
// It modifies and returns o.
func (o Object) MergeWhen(condition bool, other Object) Object {
	if condition {
		for key, value := range other {
			o[key] = value
		}
	}
	return o
}

// Resource builds the public representation of one model.
type Resource[T any] func(ctx context.Context, model T) Object

// Document is a response body: {"data": ..., "meta": ..., "links": ...}.
// Meta, Links and Extra are omitted when empty; Extra adds top-level fields.
type Document struct {
	Data  any
	Meta  Object
	Links Object
	Extra Object
}

// MarshalJSON encodes the document.
func (d Document) MarshalJSON() ([]byte, error) {
	body := Object{}
	for key, value := range d.Extra {
		body[key] = value
	}
	body["data"] = d.Data
	if len(d.Meta) > 0 {
		body["meta"] = d.Meta
	}
	if len(d.Links) > 0 {
		body["links"] = d.Links
	}
	return json.Marshal(body)
}

// Item wraps one model: {"data": {...}}.
func Item[T any](ctx context.Context, model T, r Resource[T]) Document {
	return Document{Data: r(ctx, model)}
}

// Collection wraps a list: {"data": [...]}. A nil or empty list encodes as [].
func Collection[T any](ctx context.Context, models []T, r Resource[T]) Document {
	return Document{Data: transform(ctx, models, r)}
}

func transform[T any](ctx context.Context, models []T, r Resource[T]) []Object {
	data := make([]Object, len(models))
	for i, model := range models {
		data[i] = r(ctx, model)
	}
	return data
}

// Paginated wraps one page with metadata:
//
//	{"data": [...], "meta": {"current_page": 2, "per_page": 15, "total": 40,
//	 "last_page": 3, "from": 16, "to": 30}, "links": {"first": ..., "last": ...,
//	 "prev": ..., "next": ...}}
//
// from and to are the 1-based positions of the page's first and last item,
// or null for an empty page. links holds URLs from link, such as PageURL; a
// nil link leaves links out. prev and next are null at the ends.
func Paginated[T any](ctx context.Context, models []T, p query.Pagination, r Resource[T], link func(page int) string) Document {
	meta := Object{
		"current_page": p.Page,
		"per_page":     p.PerPage,
		"total":        p.Total,
		"last_page":    p.LastPage,
		"from":         nil,
		"to":           nil,
	}
	if len(models) > 0 && p.Page > 0 && p.PerPage > 0 {
		from := int64(p.Page-1)*int64(p.PerPage) + 1
		meta["from"], meta["to"] = from, from+int64(len(models))-1
	}
	doc := Document{Data: transform(ctx, models, r), Meta: meta}
	if link != nil {
		doc.Links = Object{"first": link(1), "last": link(p.LastPage), "prev": nil, "next": nil}
		if p.Page > 1 {
			doc.Links["prev"] = link(min(p.Page-1, p.LastPage))
		}
		if p.HasMorePages() {
			doc.Links["next"] = link(p.Page + 1)
		}
	}
	return doc
}

// PageURL returns a link function for Paginated that keeps r's path and
// query and sets the page parameter, such as "/posts?page=2&sort=new". The
// links are relative, so they never point at a host taken from the request.
func PageURL(r *http.Request, param string) func(page int) string {
	if param == "" {
		param = "page"
	}
	path := r.URL.EscapedPath()
	query := r.URL.Query()
	return func(page int) string {
		q := url.Values{}
		for key, values := range query {
			q[key] = values
		}
		q.Set(param, strconv.Itoa(page))
		return path + "?" + q.Encode()
	}
}
