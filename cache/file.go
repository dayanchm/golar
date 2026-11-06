package cache

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// headerSize is the length of the expiry, in Unix nanoseconds with 0 for
// never, written before each value.
const headerSize = 8

// File keeps each value in a file named by the SHA-256 of its key. Values
// survive restarts. Writes are atomic, so readers never see a partial value,
// but Add and Increment are only atomic within one process; use SQLStore when
// several instances share them. Files and directories are private to the
// current user. Expired files stay on disk until they are replaced or pruned.
type File struct {
	dir   string
	locks [64]sync.Mutex
}

// NewFile returns a store in dir, creating it if needed.
func NewFile(dir string) (*File, error) {
	if dir == "" {
		return nil, errors.New("cache: file store needs a directory")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &File{dir: dir}, nil
}

// entry locates a key's file and the lock that guards it.
func (f *File) entry(key string) (string, *sync.Mutex) {
	sum := sha256.Sum256([]byte(key))
	name := hex.EncodeToString(sum[:])
	return filepath.Join(f.dir, name[:2], name[2:]), &f.locks[int(sum[0])%len(f.locks)]
}

// read returns the file's value and whether it is live. Missing and malformed
// files count as absent.
func read(path string) ([]byte, time.Time, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	if len(data) < headerSize {
		return nil, time.Time{}, false, nil
	}
	var expires time.Time
	if nanos := int64(binary.BigEndian.Uint64(data)); nanos != 0 {
		expires = time.Unix(0, nanos)
	}
	if !expires.IsZero() && !time.Now().Before(expires) {
		return nil, expires, false, nil
	}
	return data[headerSize:], expires, true, nil
}

// write replaces the file through a rename, so readers see the old value or
// the new one.
func write(path string, value []byte, expires time.Time) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(temp.Name())
		}
	}()
	var header [headerSize]byte
	if !expires.IsZero() {
		binary.BigEndian.PutUint64(header[:], uint64(expires.UnixNano()))
	}
	_, err = temp.Write(append(header[:], value...))
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func (f *File) Get(_ context.Context, key string) ([]byte, bool, error) {
	path, _ := f.entry(key)
	value, _, ok, err := read(path)
	return value, ok, err
}

func (f *File) Put(_ context.Context, key string, value []byte, ttl time.Duration) error {
	path, lock := f.entry(key)
	lock.Lock()
	defer lock.Unlock()
	return write(path, value, expiry(ttl))
}

func (f *File) Add(_ context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	path, lock := f.entry(key)
	lock.Lock()
	defer lock.Unlock()
	_, _, ok, err := read(path)
	if err != nil || ok {
		return false, err
	}
	return true, write(path, value, expiry(ttl))
}

func (f *File) Increment(_ context.Context, key string, delta int64) (int64, error) {
	path, lock := f.entry(key)
	lock.Lock()
	defer lock.Unlock()
	value, expires, ok, err := read(path)
	if err != nil {
		return 0, err
	}
	if !ok {
		return delta, write(path, strconv.AppendInt(nil, delta, 10), time.Time{})
	}
	value, next, err := addInt(value, delta)
	if err != nil {
		return 0, err
	}
	return next, write(path, value, expires)
}

func (f *File) Delete(_ context.Context, key string) error {
	path, lock := f.entry(key)
	lock.Lock()
	defer lock.Unlock()
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func (f *File) Flush(ctx context.Context) error {
	_, err := f.remove(ctx, func(string) bool { return true })
	return err
}

// Prune deletes expired values and files that are not valid cache entries.
func (f *File) Prune(ctx context.Context) (int64, error) {
	return f.remove(ctx, func(path string) bool {
		_, _, ok, err := read(path)
		return err == nil && !ok
	})
}

// remove deletes the cache files that match. It only touches names the store
// creates, so a misconfigured directory does not lose unrelated files.
func (f *File) remove(ctx context.Context, match func(path string) bool) (int64, error) {
	shards, err := os.ReadDir(f.dir)
	if err != nil {
		return 0, err
	}
	var removed int64
	for _, shard := range shards {
		if !shard.IsDir() || !isHex(shard.Name(), 2) {
			continue
		}
		dir := filepath.Join(f.dir, shard.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			return removed, err
		}
		sum, err := hex.DecodeString(shard.Name())
		if err != nil {
			return removed, err
		}
		lock := &f.locks[int(sum[0])%len(f.locks)]
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			if entry.IsDir() || !isHex(entry.Name(), sha256.Size*2-2) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			lock.Lock()
			if match(path) {
				err = os.Remove(path)
				if err == nil {
					removed++
				} else if errors.Is(err, fs.ErrNotExist) {
					err = nil
				}
			}
			lock.Unlock()
			if err != nil {
				return removed, fmt.Errorf("cache: %w", err)
			}
		}
	}
	return removed, nil
}

func isHex(name string, length int) bool {
	if len(name) != length {
		return false
	}
	_, err := hex.DecodeString(name)
	return err == nil
}
