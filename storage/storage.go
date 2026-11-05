// Package storage stores files on disks with one interface: Local keeps them
// in a directory, S3 in an S3 or S3-compatible bucket (MinIO, Cloudflare R2,
// DigitalOcean Spaces). A disk is public, so its files have permanent URLs,
// or private, so they are only reachable through temporary signed URLs.
//
//	disk, err := storage.NewLocal(storage.LocalConfig{Root: "storage/app", URL: "/files", Key: key})
//	err = disk.Put(ctx, "avatars/ada.png", file, storage.PutOptions{ContentType: "image/png"})
//	url, err := disk.TemporaryURL(ctx, "avatars/ada.png", 15*time.Minute)
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

// Disk stores files by slash-separated path. Implementations are safe for
// concurrent use.
type Disk interface {
	// Put stores r at path, replacing any file there. Readers are streamed,
	// not buffered in memory.
	Put(ctx context.Context, path string, r io.Reader, opts PutOptions) error
	// Get opens the file for reading; the caller closes it.
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	// Stat describes the file. Missing files return an error wrapping ErrNotExist.
	Stat(ctx context.Context, path string) (FileInfo, error)
	// Delete removes the file. Deleting a missing file is not an error.
	Delete(ctx context.Context, path string) error
	// List returns the files under prefix, recursively, sorted by path.
	List(ctx context.Context, prefix string) ([]FileInfo, error)
	// URL returns the file's permanent public URL, or ErrPrivate.
	URL(path string) (string, error)
	// TemporaryURL returns a URL that works until expires has passed.
	TemporaryURL(ctx context.Context, path string, expires time.Duration) (string, error)
}

// PutOptions describe a stored file.
type PutOptions struct {
	// ContentType is sent when the file is served. It defaults to a type
	// detected from the path's extension, then application/octet-stream.
	ContentType string
	// Size is the length of the content when known, -1 or 0 when not. It lets
	// S3 stream the upload instead of spooling it to a temporary file first.
	Size int64
}

// FileInfo describes a stored file.
type FileInfo struct {
	Path        string
	Size        int64
	ModTime     time.Time
	ContentType string // empty when the disk does not record it
}

var (
	// ErrNotExist is wrapped by errors for missing files; it is fs.ErrNotExist.
	ErrNotExist = fs.ErrNotExist
	// ErrInvalidPath is returned for paths that are empty, absolute, contain
	// "..", backslashes or NUL, or would otherwise leave the disk.
	ErrInvalidPath = errors.New("storage: invalid path")
	// ErrPrivate is returned by URL on private disks; use TemporaryURL.
	ErrPrivate = errors.New("storage: disk is private; use TemporaryURL")
	// ErrNoKey is returned by TemporaryURL on local disks without a signing key.
	ErrNoKey = errors.New("storage: temporary URLs need a signing key")
)

// MaxTemporaryURL is the longest lifetime TemporaryURL accepts, matching S3's limit.
const MaxTemporaryURL = 7 * 24 * time.Hour

// Clean validates a file path and returns its canonical form, such as
// "avatars/ada.png". Paths are relative to the disk, use forward slashes and
// cannot refer to a parent directory, so they never escape the disk's root.
func Clean(p string) (string, error) {
	if p == "" || strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, p)
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%w: %q", ErrInvalidPath, p)
		}
	}
	cleaned := path.Clean(p)
	if cleaned == "." || !fs.ValidPath(cleaned) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, p)
	}
	return cleaned, nil
}

// cleanPrefix validates a List prefix; "" lists everything.
func cleanPrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", nil
	}
	return Clean(strings.TrimSuffix(prefix, "/"))
}

func checkExpiry(expires time.Duration) error {
	if expires < time.Second || expires > MaxTemporaryURL {
		return fmt.Errorf("storage: temporary URL lifetime must be between 1s and %s, got %s", MaxTemporaryURL, expires)
	}
	return nil
}

// escapePath percent-encodes each segment of a clean path for use in a URL.
func escapePath(p string) string {
	segments := strings.Split(p, "/")
	for i, segment := range segments {
		segments[i] = escapeSegment(segment)
	}
	return strings.Join(segments, "/")
}

// escapeSegment encodes everything except unreserved characters (RFC 3986),
// which is also what S3 signing requires.
func escapeSegment(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
