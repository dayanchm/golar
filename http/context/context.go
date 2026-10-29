// Package context wraps an HTTP request and response with helpers for
// reading input and writing responses.
package context

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"

	"github.com/dayanchm/golar/validation"
)

// Default body limits, used when a Config field is zero.
const (
	DefaultMaxBodySize        = 4 << 20  // JSON and URL-encoded bodies
	DefaultMaxMultipartSize   = 32 << 20 // multipart bodies, including files
	DefaultMaxMultipartMemory = 8 << 20  // multipart data kept in memory; the rest goes to temp files
)

// Config configures request handling. Zero values use the defaults above; a
// negative size disables that limit.
type Config struct {
	MaxBodySize        int64
	MaxMultipartSize   int64
	MaxMultipartMemory int64
	// TrustedProxies lists the proxies whose X-Forwarded-For and
	// X-Forwarded-Proto headers are believed. Empty trusts no proxy.
	TrustedProxies []netip.Prefix
}

type Context struct {
	Request *http.Request
	Writer  http.ResponseWriter

	config     Config
	limited    bool
	formParsed bool
	formErr    error
	query      url.Values
}

func New(w http.ResponseWriter, r *http.Request, configs ...Config) *Context {
	c := &Context{Request: r, Writer: w}
	if len(configs) > 0 {
		c.config = configs[0]
	}
	return c
}

// Context returns the request context, which is canceled when the client
// disconnects or the request times out.
func (c *Context) Context() context.Context {
	return c.Request.Context()
}

func (c *Context) Param(name string) string {
	return c.Request.PathValue(name)
}

func (c *Context) Query(name string) string {
	if c.query == nil {
		c.query = c.Request.URL.Query()
	}
	return c.query.Get(name)
}

func (c *Context) JSON(code int, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	c.Writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	c.Writer.WriteHeader(code)

	_, err = c.Writer.Write(body)
	return err
}

func (c *Context) String(code int, value string) error {
	c.Header("Content-Type", "text/plain; charset=utf-8")
	c.Status(code)

	_, err := io.WriteString(c.Writer, value)
	return err
}

// BindJSON decodes the JSON body into dst. The body is limited to MaxBodySize.
func (c *Context) BindJSON(dst any) error {
	return json.NewDecoder(c.body()).Decode(dst)
}

// BindError reports a request body that could not be decoded. The default
// error handler responds with 400 Bad Request, or 415 when it wraps
// ErrUnsupportedMediaType.
type BindError struct{ Err error }

func (e *BindError) Error() string { return "invalid request body: " + e.Err.Error() }
func (e *BindError) Unwrap() error { return e.Err }

// ErrForbidden is returned by Validate when a request's Authorize method
// denies it. The default error handler responds with 403 Forbidden.
var ErrForbidden = errors.New("forbidden")

// Authorizer is implemented by request structs that decide whether the
// current user may make the request. Return ErrForbidden to deny it.
type Authorizer interface {
	Authorize(c *Context) error
}

// Preparer is implemented by request structs that normalize input, such as
// trimming or lower-casing, after binding and before validation.
type Preparer interface {
	Prepare(c *Context) error
}

// Validate is a form request: it binds the body into dst with Bind, calls
// dst's Authorize and Prepare methods when it has them, and checks its
// `validate` tags and AfterValidate method with validation.Default. It
// returns *BindError for bodies that cannot be decoded, ErrForbidden (403)
// when Authorize denies the request and *validation.Error, which the default
// error handler renders as 422, for invalid input.
func (c *Context) Validate(dst any) error {
	if err := c.Bind(dst); err != nil {
		return err
	}
	if authorizer, ok := dst.(Authorizer); ok {
		if err := authorizer.Authorize(c); err != nil {
			return err
		}
	}
	if preparer, ok := dst.(Preparer); ok {
		if err := preparer.Prepare(c); err != nil {
			return err
		}
	}
	return validation.ValidateContext(c.Request.Context(), dst)
}

func (c *Context) Header(name, value string) {
	c.Writer.Header().Set(name, value)
}

func (c *Context) Status(code int) {
	c.Writer.WriteHeader(code)
}
