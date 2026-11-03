package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strconv"

	"github.com/dayanchm/golar/validation"
)

// extensions maps sniffed media types to the extension PutUpload uses.
var extensions = map[string]string{
	"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp", "image/bmp": ".bmp",
	"application/pdf": ".pdf", "application/zip": ".zip", "application/x-gzip": ".gz", "text/plain": ".txt",
	"audio/mpeg": ".mp3", "audio/wave": ".wav", "video/mp4": ".mp4", "video/webm": ".webm", "application/ogg": ".ogg",
}

// PutUpload stores an uploaded file in dir under a random name, such as
// "avatars/3f2c...9a.png", and returns its path. The extension comes from the
// sniffed content, never from the client's file name, so a request cannot
// choose where the file goes or make a script look like an image. Validate
// the upload first, for example with validate:"required,image,max_size=2MB".
func PutUpload(ctx context.Context, disk Disk, dir string, file *multipart.FileHeader) (string, error) {
	if dir != "" {
		cleaned, err := Clean(dir)
		if err != nil {
			return "", err
		}
		dir = cleaned
	}
	contentType, err := validation.SniffContentType(file)
	if err != nil {
		return "", err
	}
	random := make([]byte, 16)
	rand.Read(random)
	name := path.Join(dir, hex.EncodeToString(random)+extensions[contentType])
	src, err := file.Open()
	if err != nil {
		return "", err
	}
	defer src.Close()
	if err := disk.Put(ctx, name, src, PutOptions{ContentType: contentType, Size: file.Size}); err != nil {
		return "", err
	}
	return name, nil
}

// Stream copies a stored file to w without buffering it, with its type and
// length, nosniff and a sandboxing Content-Security-Policy. Missing files get
// 404. Local disks can also be served with Local.Handler, which adds Range
// support; for S3, redirecting to TemporaryURL avoids proxying the bytes.
func Stream(w http.ResponseWriter, r *http.Request, disk Disk, name string) error {
	info, err := disk.Stat(r.Context(), name)
	if err != nil {
		http.NotFound(w, r)
		return err
	}
	body, err := disk.Get(r.Context(), name)
	if err != nil {
		http.NotFound(w, r)
		return err
	}
	defer body.Close()
	contentType := info.ContentType
	if contentType == "" {
		contentType = mime.TypeByExtension(path.Ext(info.Path))
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	h.Set("Content-Length", strconv.FormatInt(info.Size, 10))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	if !info.ModTime.IsZero() {
		h.Set("Last-Modified", info.ModTime.UTC().Format(http.TimeFormat))
	}
	if r.Method == http.MethodHead {
		return nil
	}
	_, err = io.Copy(w, body)
	return err
}
