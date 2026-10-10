package context

import (
	"encoding/json"
	"io"
	"net/http"
)

type Context struct {
	Request *http.Request
	Writer  http.ResponseWriter
}

func New(w http.ResponseWriter, r *http.Request) *Context {
	return &Context{Request: r, Writer: w}
}

func (c *Context) Param(name string) string {
	return c.Request.PathValue(name)
}

func (c *Context) Query(name string) string {
	return c.Request.URL.Query().Get(name)
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

func (c *Context) BindJSON(dst any) error {
	return json.NewDecoder(c.Request.Body).Decode(dst)
}

func (c *Context) Header(name, value string) {
	c.Writer.Header().Set(name, value)
}

func (c *Context) Status(code int) {
	c.Writer.WriteHeader(code)
}
