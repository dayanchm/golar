package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// LocalConfig configures a Local disk.
type LocalConfig struct {
	// Root is the directory that holds the files; it is created if missing.
	Root string
	// Public disks have permanent URLs; private ones only temporary URLs.
	Public bool
	// URL is where Handler is mounted, such as "/files" or
	// "https://cdn.example.com/files". URL and TemporaryURL build on it.
	URL string
	// Key signs temporary URLs. It must be at least 32 random bytes.
	Key []byte
}

// Local stores files in a directory. All access goes through os.Root, so
// paths, including symbolic links inside the directory, cannot reach files
// outside it.
type Local struct {
	config LocalConfig
	root   *os.Root
	signer *signer
}

const tempPrefix = ".golar-upload-"

// NewLocal opens or creates the directory config.Root.
func NewLocal(config LocalConfig) (*Local, error) {
	if config.Root == "" {
		return nil, errors.New("storage: LocalConfig.Root is required")
	}
	if len(config.Key) > 0 && len(config.Key) < 32 {
		return nil, errors.New("storage: LocalConfig.Key must be at least 32 bytes")
	}
	if err := os.MkdirAll(config.Root, 0o755); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(config.Root)
	if err != nil {
		return nil, err
	}
	config.URL = strings.TrimRight(config.URL, "/")
	l := &Local{config: config, root: root}
	if len(config.Key) > 0 {
		l.signer = &signer{key: config.Key}
	}
	return l, nil
}

// Close releases the directory handle.
func (l *Local) Close() error { return l.root.Close() }

func (l *Local) Put(ctx context.Context, p string, r io.Reader, _ PutOptions) (err error) {
	name, err := Clean(p)
	if err != nil {
		return err
	}
	if strings.HasPrefix(path.Base(name), tempPrefix) {
		return fmt.Errorf("%w: %q uses a reserved name", ErrInvalidPath, p)
	}
	dir := path.Dir(name)
	if err := l.root.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	suffix := make([]byte, 8)
	rand.Read(suffix)
	tmp := path.Join(dir, tempPrefix+hex.EncodeToString(suffix))
	file, err := l.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = l.root.Remove(tmp)
		}
	}()
	_, err = io.Copy(file, contextReader{ctx, r})
	if err = errors.Join(err, file.Close()); err != nil {
		return err
	}
	// Renaming makes the new file appear at once, so readers never see half of it.
	return l.root.Rename(tmp, name)
}

// contextReader stops a copy when ctx is done.
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (c contextReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func (l *Local) open(p string) (*os.File, fs.FileInfo, error) {
	name, err := Clean(p)
	if err != nil {
		return nil, nil, err
	}
	file, err := l.root.Open(name)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = &fs.PathError{Op: "open", Path: name, Err: ErrNotExist}
	}
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	return file, info, nil
}

func (l *Local) Get(ctx context.Context, p string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, _, err := l.open(p)
	return file, err
}

func (l *Local) Stat(_ context.Context, p string) (FileInfo, error) {
	name, err := Clean(p)
	if err != nil {
		return FileInfo{}, err
	}
	info, err := l.root.Stat(name)
	if err != nil {
		return FileInfo{}, err
	}
	if !info.Mode().IsRegular() {
		return FileInfo{}, &fs.PathError{Op: "stat", Path: name, Err: ErrNotExist}
	}
	return fileInfo(name, info), nil
}

func fileInfo(name string, info fs.FileInfo) FileInfo {
	return FileInfo{Path: name, Size: info.Size(), ModTime: info.ModTime(), ContentType: mime.TypeByExtension(path.Ext(name))}
}

func (l *Local) Delete(_ context.Context, p string) error {
	name, err := Clean(p)
	if err != nil {
		return err
	}
	info, err := l.root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("storage: %s is a directory", name)
	}
	if err := l.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (l *Local) List(ctx context.Context, prefix string) ([]FileInfo, error) {
	start, err := cleanPrefix(prefix)
	if err != nil {
		return nil, err
	}
	if start == "" {
		start = "."
	}
	var files []FileInfo
	err = fs.WalkDir(l.root.FS(), start, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && name == start {
				return fs.SkipAll
			}
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), tempPrefix) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		files = append(files, fileInfo(name, info))
		return nil
	})
	return files, err
}

func (l *Local) URL(p string) (string, error) {
	if !l.config.Public {
		return "", ErrPrivate
	}
	name, err := Clean(p)
	if err != nil {
		return "", err
	}
	return l.config.URL + "/" + escapePath(name), nil
}

func (l *Local) TemporaryURL(_ context.Context, p string, expires time.Duration) (string, error) {
	name, err := Clean(p)
	if err != nil {
		return "", err
	}
	if l.signer == nil {
		return "", ErrNoKey
	}
	if err := checkExpiry(expires); err != nil {
		return "", err
	}
	return l.config.URL + "/" + escapePath(name) + "?" + l.signer.sign(name, time.Now().Add(expires)), nil
}

// Handler serves the disk's files, mounted at the path of LocalConfig.URL:
//
//	mux.Handle("/files/", http.StripPrefix("/files", disk.Handler()))
//
// Public disks serve every file; private disks only serve URLs from
// TemporaryURL until they expire, and answer others with 403. It supports
// GET and HEAD, Range and conditional requests, and streams from disk.
// Files are sent with nosniff and a sandboxing Content-Security-Policy, so an
// uploaded HTML or SVG file cannot run scripts on the site's origin.
func (l *Local) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name, err := Clean(strings.TrimPrefix(r.URL.Path, "/"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if !l.config.Public {
			if l.signer == nil || !l.signer.valid(name, r.URL.Query(), time.Now()) {
				http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)
				return
			}
			w.Header().Set("Cache-Control", "private, no-store")
		}
		file, info, err := l.open(name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer file.Close()
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		} else {
			w.Header().Set("Content-Type", "application/octet-stream")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		http.ServeContent(w, r, name, info.ModTime(), file)
	})
}
