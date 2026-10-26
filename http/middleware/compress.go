package middleware

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// CompressConfig configures Compress.
type CompressConfig struct {
	// Level is a compress/gzip level; zero uses gzip.DefaultCompression.
	Level int
	// MinSize is the smallest body, in bytes, worth compressing; zero uses 1024.
	MinSize int
}

var gzipPools sync.Map // level -> *sync.Pool

// Compress gzips responses for clients that accept it. It skips HEAD requests,
// bodies under MinSize, responses that already set Content-Encoding, partial
// content, event streams and already-compressed types such as images, video,
// audio, archives and fonts. It panics on an invalid Level.
func Compress(configs ...CompressConfig) Middleware {
	var config CompressConfig
	if len(configs) > 0 {
		config = configs[0]
	}
	if config.Level == 0 {
		config.Level = gzip.DefaultCompression
	}
	if config.MinSize <= 0 {
		config.MinSize = 1024
	}
	if _, err := gzip.NewWriterLevel(nil, config.Level); err != nil {
		panic("middleware: " + err.Error())
	}
	pool, _ := gzipPools.LoadOrStore(config.Level, &sync.Pool{New: func() any {
		w, _ := gzip.NewWriterLevel(nil, config.Level)
		return w
	}})
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Vary", "Accept-Encoding")
			if r.Method == http.MethodHead || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
				next.ServeHTTP(w, r)
				return
			}
			cw := &compressWriter{ResponseWriter: w, pool: pool.(*sync.Pool), minSize: config.MinSize}
			// On panic, leave buffered output unsent so Recovery can still respond with 500.
			completed := false
			defer func() {
				if !completed {
					cw.release()
				}
			}()
			next.ServeHTTP(cw, r)
			completed = true
			cw.close()
		})
	}
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip.
func acceptsGzip(header string) bool {
	for part := range strings.SplitSeq(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "*" {
			continue
		}
		if q, ok := strings.CutPrefix(strings.ReplaceAll(params, " ", ""), "q="); ok {
			if value, err := strconv.ParseFloat(q, 64); err != nil || value == 0 {
				continue
			}
		}
		return true
	}
	return false
}

// compressWriter buffers up to minSize bytes before deciding whether to compress.
type compressWriter struct {
	http.ResponseWriter
	pool    *sync.Pool
	minSize int
	status  int
	buf     []byte
	decided bool
	gz      *gzip.Writer
}

func (w *compressWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *compressWriter) WriteHeader(code int) {
	switch {
	case w.decided || w.status != 0:
	case code == http.StatusSwitchingProtocols:
		w.decided = true
		w.ResponseWriter.WriteHeader(code)
	case code < 200:
		w.ResponseWriter.WriteHeader(code) // informational responses pass through
	default:
		w.status = code
	}
}

func (w *compressWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if !w.decided {
		w.buf = append(w.buf, body...)
		if len(w.buf) < w.minSize {
			return len(body), nil
		}
		if err := w.decide(); err != nil {
			return 0, err
		}
		return len(body), nil
	}
	if w.gz != nil {
		return w.gz.Write(body)
	}
	return w.ResponseWriter.Write(body)
}

// decide writes the header and the buffered body, compressed or not.
func (w *compressWriter) decide() error {
	w.decided = true
	if w.status == 0 {
		w.status = http.StatusOK
	}
	h := w.Header()
	if h.Get("Content-Type") == "" && len(w.buf) > 0 {
		h.Set("Content-Type", http.DetectContentType(w.buf))
	}
	if len(w.buf) >= w.minSize && w.compressible() {
		h.Del("Content-Length")
		h.Set("Content-Encoding", "gzip")
		w.gz = w.pool.Get().(*gzip.Writer)
		w.gz.Reset(w.ResponseWriter)
		w.ResponseWriter.WriteHeader(w.status)
		_, err := w.gz.Write(w.buf)
		w.buf = nil
		return err
	}
	w.ResponseWriter.WriteHeader(w.status)
	if len(w.buf) == 0 {
		return nil
	}
	_, err := w.ResponseWriter.Write(w.buf)
	w.buf = nil
	return err
}

func (w *compressWriter) compressible() bool {
	h := w.Header()
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" ||
		w.status < 200 || w.status == http.StatusNoContent || w.status == http.StatusNotModified || w.status == http.StatusPartialContent {
		return false
	}
	mediaType, _, _ := strings.Cut(strings.ToLower(h.Get("Content-Type")), ";")
	mediaType = strings.TrimSpace(mediaType)
	for _, prefix := range []string{"image/", "video/", "audio/", "font/"} {
		if strings.HasPrefix(mediaType, prefix) && mediaType != "image/svg+xml" {
			return false
		}
	}
	switch mediaType {
	case "text/event-stream", "application/octet-stream", "application/zip", "application/gzip",
		"application/x-gzip", "application/zstd", "application/x-7z-compressed", "application/x-rar-compressed",
		"application/pdf", "application/wasm":
		return false
	}
	return true
}

// FlushError sends buffered output; a flush before MinSize bytes disables compression.
func (w *compressWriter) FlushError() error {
	if !w.decided {
		if err := w.decide(); err != nil {
			return err
		}
	}
	if w.gz != nil {
		if err := w.gz.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(w.ResponseWriter).Flush()
}

func (w *compressWriter) close() {
	if !w.decided && (w.status != 0 || len(w.buf) > 0) {
		_ = w.decide()
	}
	if w.gz != nil {
		_ = w.gz.Close()
	}
	w.release()
}

func (w *compressWriter) release() {
	if w.gz != nil {
		w.gz.Reset(nil)
		w.pool.Put(w.gz)
		w.gz = nil
	}
}
