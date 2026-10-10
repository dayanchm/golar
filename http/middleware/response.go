package middleware

import "net/http"

// responseWriter tracks final status and bytes while allowing ResponseController
// to reach the underlying writer through Unwrap.
type responseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *responseWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.ResponseWriter.WriteHeader(code)
	if code >= 200 || code == http.StatusSwitchingProtocols {
		w.status = code
	}
}

func (w *responseWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(body)
	w.bytes += n
	return n, err
}

func (w *responseWriter) FlushError() error {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}
