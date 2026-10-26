package context

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
)

// Template is implemented by *html/template.Template and *text/template.Template.
type Template interface {
	ExecuteTemplate(w io.Writer, name string, data any) error
}

// HTML writes an HTML string. The caller is responsible for escaping it; use
// Render with html/template for pages built from user data.
func (c *Context) HTML(code int, html string) error {
	return c.Blob(code, "text/html; charset=utf-8", []byte(html))
}

// Render executes the named template into a buffer and writes it as HTML, so
// a template error leaves the response unwritten for the error handler.
func (c *Context) Render(code int, t Template, name string, data any) error {
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, name, data); err != nil {
		return err
	}
	return c.Blob(code, "text/html; charset=utf-8", buf.Bytes())
}

// Blob writes data with the given Content-Type.
func (c *Context) Blob(code int, contentType string, data []byte) error {
	c.Header("Content-Type", contentType)
	c.Status(code)
	_, err := c.Writer.Write(data)
	return err
}

// NoContent writes a status without a body, such as 204 No Content.
func (c *Context) NoContent(code int) error {
	c.Status(code)
	return nil
}

// Redirect sends a redirect with a 3xx status. Redirecting to a URL taken from
// the request, such as ?next=, allows open redirects; check it first.
func (c *Context) Redirect(code int, url string) error {
	if code < 300 || code > 308 {
		return fmt.Errorf("context: invalid redirect status %d", code)
	}
	http.Redirect(c.Writer, c.Request, url, code)
	return nil
}

// Download sends the file at path as an attachment named filename, or the
// path's base name when filename is empty. It supports Range and conditional
// requests. Never build path from request input without checking it.
func (c *Context) Download(path, filename string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("context: %s is a directory", path)
	}
	if filename == "" {
		filename = filepath.Base(path)
	}
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": filename})
	if disposition == "" {
		disposition = "attachment"
	}
	c.Header("Content-Disposition", disposition)
	http.ServeContent(c.Writer, c.Request, filename, info.ModTime(), file)
	return nil
}
