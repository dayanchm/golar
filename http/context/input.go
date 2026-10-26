package context

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ErrUnsupportedMediaType is wrapped in a *BindError when Bind gets a body
// type it cannot decode. The default error handler responds with 415.
var ErrUnsupportedMediaType = errors.New("unsupported media type")

// InputError reports an invalid query parameter or other non-body input. The
// default error handler responds with 400 and names the parameter.
type InputError struct {
	Source string // such as "query"
	Name   string
	Err    error
}

func (e *InputError) Error() string {
	return fmt.Sprintf("invalid %s parameter %q: %v", e.Source, e.Name, e.Err)
}
func (e *InputError) Unwrap() error { return e.Err }

// QueryDefault returns the query value, or fallback when it is missing or empty.
func (c *Context) QueryDefault(name, fallback string) string {
	if value := c.Query(name); value != "" {
		return value
	}
	return fallback
}

// QueryInt parses the query value as an int. A missing or empty value returns
// fallback; an invalid one returns fallback and an *InputError.
func (c *Context) QueryInt(name string, fallback int) (int, error) {
	value := c.Query(name)
	if value == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback, &InputError{Source: "query", Name: name, Err: errors.New("not an integer")}
	}
	return n, nil
}

// mediaType returns the request's lower-case media type without parameters.
func (c *Context) mediaType() string {
	mediaType, _, _ := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	return mediaType
}

func (c *Context) isMultipart() bool { return c.mediaType() == "multipart/form-data" }

// body limits the request body on first use: MaxMultipartSize for multipart
// requests and MaxBodySize for everything else. Larger bodies fail with
// *http.MaxBytesError, which the default error handler renders as 413.
func (c *Context) body() io.ReadCloser {
	if !c.limited && c.Request.Body != nil {
		c.limited = true
		limit := limitOr(c.config.MaxBodySize, DefaultMaxBodySize)
		if c.isMultipart() {
			limit = limitOr(c.config.MaxMultipartSize, DefaultMaxMultipartSize)
		}
		if limit > 0 {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
	}
	return c.Request.Body
}

func limitOr(value, fallback int64) int64 {
	if value == 0 {
		return fallback
	}
	return value
}

// ParseForm parses URL-encoded and multipart bodies once, within the body
// limits. Errors are wrapped in *BindError.
func (c *Context) ParseForm() error {
	if c.formParsed {
		return c.formErr
	}
	c.formParsed = true
	c.body()
	var err error
	if c.isMultipart() {
		memory := limitOr(c.config.MaxMultipartMemory, DefaultMaxMultipartMemory)
		if memory < 0 {
			memory = DefaultMaxMultipartMemory
		}
		err = c.Request.ParseMultipartForm(memory)
	} else {
		err = c.Request.ParseForm()
	}
	if err != nil {
		c.formErr = &BindError{Err: err}
	}
	return c.formErr
}

// Form returns a value from a URL-encoded or multipart body. It returns ""
// when the field is missing or the body cannot be parsed; call ParseForm to
// see the error.
func (c *Context) Form(name string) string {
	_ = c.ParseForm()
	return c.Request.PostForm.Get(name)
}

// File returns the first uploaded file for name. A missing file returns a
// *BindError wrapping http.ErrMissingFile.
func (c *Context) File(name string) (*multipart.FileHeader, error) {
	if !c.isMultipart() {
		return nil, &BindError{Err: http.ErrNotMultipart}
	}
	if err := c.ParseForm(); err != nil {
		return nil, err
	}
	if files := c.Request.MultipartForm.File[name]; len(files) > 0 {
		return files[0], nil
	}
	return nil, &BindError{Err: http.ErrMissingFile}
}

// SaveFile writes an uploaded file to dst, creating parent directories. Never
// build dst from file.Filename, which the client controls.
func (c *Context) SaveFile(file *multipart.FileHeader, dst string) (err error) {
	src, err := file.Open()
	if err != nil {
		return err
	}
	defer src.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := out.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			_ = os.Remove(dst)
		}
	}()
	_, err = io.Copy(out, src)
	return err
}

// Bind decodes the body into dst by Content-Type: JSON (also when no type is
// sent), URL-encoded forms or multipart forms. Form fields map to struct
// fields by their `form` tag, then their `json` tag, then their name.
// Decoding errors are returned as *BindError.
func (c *Context) Bind(dst any) error {
	switch mediaType := c.mediaType(); {
	case mediaType == "" || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		decoder := json.NewDecoder(c.body())
		if err := decoder.Decode(dst); err != nil {
			return &BindError{Err: err}
		}
		if err := decoder.Decode(&json.RawMessage{}); err != io.EOF {
			if err == nil {
				err = errors.New("unexpected data after JSON value")
			}
			return &BindError{Err: err}
		}
		return nil
	case mediaType == "application/x-www-form-urlencoded" || mediaType == "multipart/form-data":
		if err := c.ParseForm(); err != nil {
			return err
		}
		var files map[string][]*multipart.FileHeader
		if c.Request.MultipartForm != nil {
			files = c.Request.MultipartForm.File
		}
		if err := decodeForm(dst, c.Request.PostForm, files); err != nil {
			return &BindError{Err: err}
		}
		return nil
	default:
		return &BindError{Err: ErrUnsupportedMediaType}
	}
}
