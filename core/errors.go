package core

import (
	"errors"
	"fmt"
	"log"
	"net/http"

	httpcontext "github.com/dayanchm/golar/http/context"
	"github.com/dayanchm/golar/validation"
)

// HTTPError is an intentional client-facing error. Message must be safe to expose.
type HTTPError struct {
	Code    int
	Message string
	Err     error
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return http.StatusText(e.Code)
}
func (e *HTTPError) Unwrap() error { return e.Err }

// DefaultErrorHandler logs errors and hides unexpected implementation details.
// Validation errors become 422 JSON, oversized bodies 413, unsupported body
// types 415, denied form requests 403, and undecodable bodies and invalid
// input parameters 400.
// If the response has started, it logs the error without appending a second body.
func DefaultErrorHandler(c *httpcontext.Context, err error) {
	var invalid *validation.Error
	if errors.As(err, &invalid) {
		if w, ok := c.Writer.(*responseWriter); !ok || !w.committed {
			_ = c.JSON(http.StatusUnprocessableEntity, map[string]validation.Errors{"errors": invalid.Errors})
		}
		return
	}
	log.Printf("handler error method=%s path=%q: %v", c.Request.Method, c.Request.URL.Path, err)
	if w, ok := c.Writer.(*responseWriter); ok && w.committed {
		return
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		http.Error(c.Writer, http.StatusText(http.StatusRequestEntityTooLarge), http.StatusRequestEntityTooLarge)
		return
	}
	if errors.Is(err, httpcontext.ErrForbidden) {
		http.Error(c.Writer, http.StatusText(http.StatusForbidden), http.StatusForbidden)
		return
	}
	if errors.Is(err, httpcontext.ErrUnsupportedMediaType) {
		http.Error(c.Writer, http.StatusText(http.StatusUnsupportedMediaType), http.StatusUnsupportedMediaType)
		return
	}
	var bindErr *httpcontext.BindError
	if errors.As(err, &bindErr) {
		http.Error(c.Writer, "Invalid request body", http.StatusBadRequest)
		return
	}
	var inputErr *httpcontext.InputError
	if errors.As(err, &inputErr) {
		http.Error(c.Writer, fmt.Sprintf("Invalid %s parameter %q", inputErr.Source, inputErr.Name), http.StatusBadRequest)
		return
	}
	code, message := http.StatusInternalServerError, http.StatusText(http.StatusInternalServerError)
	var httpErr *HTTPError
	if errors.As(err, &httpErr) && httpErr != nil && httpErr.Code >= 400 && httpErr.Code <= 599 {
		code = httpErr.Code
		message = httpErr.Error()
	}
	http.Error(c.Writer, message, code)
}

type responseWriter struct {
	http.ResponseWriter
	committed bool
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseWriter) WriteHeader(code int) {
	if w.committed {
		return
	}
	w.ResponseWriter.WriteHeader(code)
	if code >= 200 || code == http.StatusSwitchingProtocols {
		w.committed = true
	}
}
func (w *responseWriter) Write(body []byte) (int, error) {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *responseWriter) FlushError() error {
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
