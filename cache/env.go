package cache

import (
	"fmt"
	"os"
	"strings"

	"github.com/dayanchm/golar/database/orm"
)

// FromEnv builds a store from CACHE_DRIVER: memory (the default), file or
// database. The file driver uses CACHE_PATH, by default storage/cache. The
// database driver uses db, which may be nil for the other drivers.
func FromEnv(db orm.DB) (Store, error) {
	switch name := strings.ToLower(strings.TrimSpace(os.Getenv("CACHE_DRIVER"))); name {
	case "", "memory":
		return NewMemory(), nil
	case "file":
		dir := strings.TrimSpace(os.Getenv("CACHE_PATH"))
		if dir == "" {
			dir = "storage/cache"
		}
		return NewFile(dir)
	case "database":
		if db == nil {
			return nil, fmt.Errorf("cache: the database driver needs a database")
		}
		return &SQLStore{DB: db}, nil
	default:
		return nil, fmt.Errorf("cache: unknown CACHE_DRIVER %q", name)
	}
}
