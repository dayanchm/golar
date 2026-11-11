package cache_test

import (
	"bytes"
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dayanchm/golar/cache"
	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/schema"
)

// connect opens SQLite by default, or the database named by GOLAR_TEST_DRIVER
// and GOLAR_TEST_DSN, with a fresh cache table.
func connect(t *testing.T) *database.DB {
	t.Helper()
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(t.TempDir(), "cache.db")}
	if driver != "sqlite" {
		config.Database = ""
		config.DSN = os.Getenv("GOLAR_TEST_DSN")
		if config.DSN == "" {
			t.Fatal("GOLAR_TEST_DSN is required")
		}
	}
	db, err := database.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := schema.New(db.DB, driver)
	if err := s.DropIfExists(cache.Table); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.DropIfExists(cache.Table) })
	for range 2 { // CreateTables is idempotent
		if err := cache.CreateTables(s); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// stores runs a test against every driver.
func stores(t *testing.T, test func(t *testing.T, store cache.Store)) {
	t.Run("memory", func(t *testing.T) { test(t, cache.NewMemory()) })
	t.Run("file", func(t *testing.T) {
		store, err := cache.NewFile(filepath.Join(t.TempDir(), "cache"))
		if err != nil {
			t.Fatal(err)
		}
		test(t, store)
	})
	t.Run("sql", func(t *testing.T) { test(t, &cache.SQLStore{DB: connect(t)}) })
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestGetPutDelete(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		ctx := context.Background()
		if _, ok, err := store.Get(ctx, "missing"); ok || err != nil {
			t.Fatalf("Get(missing) = %v, %v", ok, err)
		}
		value := []byte{0, 1, 2, 0xff, 'a'}
		must(t, store.Put(ctx, "key", value, time.Hour))
		got, ok, err := store.Get(ctx, "key")
		if err != nil || !ok || !bytes.Equal(got, value) {
			t.Fatalf("Get = %v, %v, %v", got, ok, err)
		}
		must(t, store.Put(ctx, "key", []byte("new"), 0))
		must(t, store.Put(ctx, "key", []byte("new"), 0)) // unchanged row on MySQL
		if got, _, _ := store.Get(ctx, "key"); string(got) != "new" {
			t.Fatalf("after replace: %q", got)
		}
		must(t, store.Put(ctx, "empty", nil, 0))
		if got, ok, err := store.Get(ctx, "empty"); err != nil || !ok || len(got) != 0 {
			t.Fatalf("empty value = %q, %v, %v", got, ok, err)
		}
		must(t, store.Delete(ctx, "key"))
		must(t, store.Delete(ctx, "key"))
		if _, ok, _ := store.Get(ctx, "key"); ok {
			t.Fatal("deleted key loaded")
		}
	})
}

func TestKeysAreExact(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		ctx := context.Background()
		long := strings.Repeat("k", 5000)
		for _, key := range []string{"Key", "key", "../escape", long, "ключ", ""} {
			must(t, store.Put(ctx, key, []byte(key), 0))
		}
		for _, key := range []string{"Key", "key", "../escape", long, "ключ", ""} {
			if got, ok, err := store.Get(ctx, key); err != nil || !ok || string(got) != key {
				t.Errorf("Get(%.20q) = %.20q, %v, %v", key, got, ok, err)
			}
		}
	})
}

func TestExpiry(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		ctx := context.Background()
		must(t, store.Put(ctx, "past", []byte("v"), -time.Second))
		if _, ok, _ := store.Get(ctx, "past"); ok {
			t.Fatal("expired value loaded")
		}
		// Read the short value back straight away, with room for slow
		// databases under -race, then wait until it has certainly expired.
		const short = 300 * time.Millisecond
		must(t, store.Put(ctx, "short", []byte("v"), short))
		written := time.Now() // the expiry is no later than written + short
		if _, ok, _ := store.Get(ctx, "short"); !ok {
			t.Fatal("live value missing")
		}
		must(t, store.Put(ctx, "forever", []byte("v"), 0))
		must(t, store.Put(ctx, "long", []byte("v"), time.Hour))
		time.Sleep(time.Until(written.Add(short + 20*time.Millisecond)))
		if _, ok, _ := store.Get(ctx, "short"); ok {
			t.Fatal("expired value loaded")
		}
		pruner, ok := store.(cache.Pruner)
		if !ok {
			t.Fatal("store does not prune")
		}
		must(t, store.Delete(ctx, "past"))
		must(t, store.Delete(ctx, "short"))
		must(t, store.Put(ctx, "stale-1", []byte("v"), -time.Second))
		must(t, store.Put(ctx, "stale-2", []byte("v"), -time.Second))
		n, err := pruner.Prune(ctx)
		if err != nil || n != 2 {
			t.Fatalf("Prune = %d, %v; want 2", n, err)
		}
		for _, key := range []string{"forever", "long"} {
			if _, ok, _ := store.Get(ctx, key); !ok {
				t.Errorf("Prune removed %s", key)
			}
		}
	})
}

func TestAdd(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		ctx := context.Background()
		if ok, err := store.Add(ctx, "lock", []byte("a"), time.Hour); !ok || err != nil {
			t.Fatalf("first Add = %v, %v", ok, err)
		}
		if ok, err := store.Add(ctx, "lock", []byte("b"), time.Hour); ok || err != nil {
			t.Fatalf("second Add = %v, %v", ok, err)
		}
		if got, _, _ := store.Get(ctx, "lock"); string(got) != "a" {
			t.Fatalf("value = %q", got)
		}
		must(t, store.Put(ctx, "old", []byte("a"), -time.Second))
		if ok, err := store.Add(ctx, "old", []byte("b"), time.Hour); !ok || err != nil {
			t.Fatalf("Add over expired = %v, %v", ok, err)
		}
		if got, _, _ := store.Get(ctx, "old"); string(got) != "b" {
			t.Fatalf("value = %q", got)
		}
	})
}

func TestAddHasOneWinner(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		var wins atomic.Int64
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				ok, err := store.Add(context.Background(), "lock", []byte(strconv.Itoa(i)), time.Hour)
				if err != nil {
					t.Error(err)
				}
				if ok {
					wins.Add(1)
				}
			})
		}
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatalf("winners = %d, want 1", wins.Load())
		}
	})
}

func TestIncrement(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		ctx := context.Background()
		if n, err := store.Increment(ctx, "n", 5); n != 5 || err != nil {
			t.Fatalf("first Increment = %d, %v", n, err)
		}
		if n, err := store.Increment(ctx, "n", 2); n != 7 || err != nil {
			t.Fatalf("second Increment = %d, %v", n, err)
		}
		if n, err := cache.Decrement(ctx, store, "n", 10); n != -3 || err != nil {
			t.Fatalf("Decrement = %d, %v", n, err)
		}
		if n, err := store.Increment(ctx, "n", 0); n != -3 || err != nil {
			t.Fatalf("Increment(0) = %d, %v", n, err)
		}
		if n, ok, err := cache.Get[int64](ctx, store, "n"); n != -3 || !ok || err != nil {
			t.Fatalf("Get = %d, %v, %v", n, ok, err)
		}

		must(t, store.Put(ctx, "text", []byte("abc"), 0))
		if _, err := store.Increment(ctx, "text", 1); !errors.Is(err, cache.ErrNotInteger) {
			t.Fatalf("Increment(text) error = %v", err)
		}
		must(t, store.Put(ctx, "max", []byte(strconv.FormatInt(math.MaxInt64, 10)), 0))
		if _, err := store.Increment(ctx, "max", 1); !errors.Is(err, cache.ErrNotInteger) {
			t.Fatalf("overflow error = %v", err)
		}
		if _, err := cache.Decrement(ctx, store, "n", math.MinInt64); !errors.Is(err, cache.ErrNotInteger) {
			t.Fatalf("Decrement(MinInt64) error = %v", err)
		}

		// Existing expiry is kept; an expired value starts again without one.
		// As in TestExpiry, leave room for slow databases under -race and
		// wait from after Put, when the expiry has been set.
		const window = 300 * time.Millisecond
		must(t, store.Put(ctx, "window", []byte("1"), window))
		written := time.Now()
		if n, err := store.Increment(ctx, "window", 1); n != 2 || err != nil {
			t.Fatalf("Increment(window) = %d, %v", n, err)
		}
		time.Sleep(time.Until(written.Add(window + 20*time.Millisecond)))
		if _, ok, _ := store.Get(ctx, "window"); ok {
			t.Fatal("Increment extended the expiry")
		}
		if n, err := store.Increment(ctx, "window", 3); n != 3 || err != nil {
			t.Fatalf("Increment over expired = %d, %v", n, err)
		}
	})
}

func TestConcurrentIncrements(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		const workers, each = 8, 10
		var wg sync.WaitGroup
		for range workers {
			wg.Go(func() {
				for range each {
					if _, err := store.Increment(context.Background(), "n", 1); err != nil {
						t.Error(err)
						return
					}
				}
			})
		}
		wg.Wait()
		if n, _, err := cache.Get[int](context.Background(), store, "n"); n != workers*each || err != nil {
			t.Fatalf("n = %d, %v; want %d", n, err, workers*each)
		}
	})
}

func TestFlush(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		ctx := context.Background()
		must(t, store.Put(ctx, "a", []byte("1"), 0))
		must(t, store.Put(ctx, "b", []byte("2"), time.Hour))
		must(t, store.Flush(ctx))
		for _, key := range []string{"a", "b"} {
			if _, ok, _ := store.Get(ctx, key); ok {
				t.Errorf("%s survived Flush", key)
			}
		}
		must(t, store.Flush(ctx))
	})
}

type user struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func TestTypedHelpers(t *testing.T) {
	stores(t, func(t *testing.T, store cache.Store) {
		ctx := context.Background()
		must(t, cache.Put(ctx, store, "user", user{"Ayşe", []string{"a"}}, time.Hour))
		got, ok, err := cache.Get[user](ctx, store, "user")
		if err != nil || !ok || got.Name != "Ayşe" || got.Tags[0] != "a" {
			t.Fatalf("Get = %+v, %v, %v", got, ok, err)
		}
		if _, ok, err := cache.Get[user](ctx, store, "missing"); ok || err != nil {
			t.Fatalf("Get(missing) = %v, %v", ok, err)
		}
		if _, _, err := cache.Get[int](ctx, store, "user"); !errors.Is(err, cache.ErrDecode) {
			t.Fatalf("decode error = %v", err)
		}
		if err := cache.Put(ctx, store, "bad", make(chan int), 0); err == nil {
			t.Fatal("Put accepted a value JSON cannot encode")
		}

		calls := 0
		compute := func(context.Context) (int, error) { calls++; return 42, nil }
		for range 2 {
			if n, err := cache.Remember(ctx, store, "answer", time.Hour, compute); n != 42 || err != nil {
				t.Fatalf("Remember = %d, %v", n, err)
			}
		}
		if calls != 1 {
			t.Fatalf("compute ran %d times", calls)
		}

		boom := errors.New("boom")
		if _, err := cache.Remember(ctx, store, "fails", time.Hour, func(context.Context) (int, error) { return 0, boom }); !errors.Is(err, boom) {
			t.Fatalf("Remember error = %v", err)
		}
		if _, ok, _ := store.Get(ctx, "fails"); ok {
			t.Fatal("a failed compute was cached")
		}

		must(t, store.Put(ctx, "stale", []byte(`"text"`), 0))
		if n, err := cache.Remember(ctx, store, "stale", time.Hour, compute); n != 42 || err != nil {
			t.Fatalf("Remember over an undecodable value = %d, %v", n, err)
		}
		if n, _, err := cache.Get[int](ctx, store, "stale"); n != 42 || err != nil {
			t.Fatalf("recomputed value = %d, %v", n, err)
		}
	})
}

type countingStore struct {
	*cache.Memory
	calls atomic.Int64
}

func (s *countingStore) Prune(ctx context.Context) (int64, error) {
	s.calls.Add(1)
	return s.Memory.Prune(ctx)
}

func TestPruneEvery(t *testing.T) {
	store := &countingStore{Memory: cache.NewMemory()}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { cache.PruneEvery(ctx, store, time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for store.calls.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("PruneEvery did not prune")
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PruneEvery did not stop")
	}
}

type plainStore struct{ cache.Store }

func TestPruneEverySkipsOtherStores(t *testing.T) {
	cache.PruneEvery(context.Background(), plainStore{cache.NewMemory()}, time.Millisecond)
}

func TestFileLeavesOtherFilesAlone(t *testing.T) {
	dir := t.TempDir()
	store, err := cache.NewFile(dir)
	must(t, err)
	ctx := context.Background()
	must(t, store.Put(ctx, "a", []byte("1"), 0))
	unrelated := []string{filepath.Join(dir, "notes.txt"), filepath.Join(dir, "ab", "notes.txt")}
	must(t, os.MkdirAll(filepath.Join(dir, "ab"), 0o755))
	for _, path := range unrelated {
		must(t, os.WriteFile(path, []byte("keep"), 0o644))
	}
	must(t, store.Flush(ctx))
	for _, path := range unrelated {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("Flush removed %s", path)
		}
	}
}

func TestFileSurvivesRestartAndIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	first, err := cache.NewFile(dir)
	must(t, err)
	ctx := context.Background()
	must(t, first.Put(ctx, "secret-key", []byte("v"), time.Hour))
	second, err := cache.NewFile(dir)
	must(t, err)
	if got, ok, err := second.Get(ctx, "secret-key"); err != nil || !ok || string(got) != "v" {
		t.Fatalf("Get = %q, %v, %v", got, ok, err)
	}
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(path, "secret-key") {
			t.Errorf("key in path %s", path)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v", path, info.Mode().Perm())
		}
		return nil
	})
	must(t, err)
	if _, err := cache.NewFile(""); err == nil {
		t.Fatal("NewFile accepted an empty directory")
	}
}

func TestFileIgnoresMalformedEntries(t *testing.T) {
	dir := t.TempDir()
	store, err := cache.NewFile(dir)
	must(t, err)
	ctx := context.Background()
	must(t, store.Put(ctx, "a", []byte("1"), 0))
	var entry string
	must(t, filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			entry = path
		}
		return err
	}))
	must(t, os.WriteFile(entry, []byte("x"), 0o600))
	if _, ok, err := store.Get(ctx, "a"); ok || err != nil {
		t.Fatalf("Get(malformed) = %v, %v", ok, err)
	}
	if n, err := store.Prune(ctx); n != 1 || err != nil {
		t.Fatalf("Prune = %d, %v", n, err)
	}
}

func TestSQLStoresShareValuesAndTableName(t *testing.T) {
	db := connect(t)
	ctx := context.Background()
	a, b := &cache.SQLStore{DB: db}, &cache.SQLStore{DB: db, Table: cache.Table}
	must(t, a.Put(ctx, "shared", []byte("v"), 0))
	if got, ok, err := b.Get(ctx, "shared"); err != nil || !ok || string(got) != "v" {
		t.Fatalf("Get = %q, %v, %v", got, ok, err)
	}
	if n, err := db.Table(cache.Table).Where("id", "=", "shared").Count(ctx); n != 0 || err != nil {
		t.Fatalf("plain key stored: %d, %v", n, err)
	}
	if err := (&cache.SQLStore{DB: db, Table: "missing_cache_table"}).Put(ctx, "k", []byte("v"), 0); err == nil {
		t.Fatal("Put to a missing table succeeded")
	}
}

func TestFromEnv(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "c")
	t.Setenv("CACHE_PATH", dir)
	db := connect(t)
	for driver, want := range map[string]string{"": "*cache.Memory", "memory": "*cache.Memory", "FILE": "*cache.File", "database": "*cache.SQLStore"} {
		t.Setenv("CACHE_DRIVER", driver)
		store, err := cache.FromEnv(db)
		if err != nil {
			t.Fatalf("%q: %v", driver, err)
		}
		if got := typeName(store); got != want {
			t.Errorf("%q = %s, want %s", driver, got, want)
		}
	}
	t.Setenv("CACHE_DRIVER", "redis")
	if _, err := cache.FromEnv(nil); err == nil {
		t.Fatal("accepted an unknown driver")
	}
	t.Setenv("CACHE_DRIVER", "database")
	if _, err := cache.FromEnv(nil); err == nil {
		t.Fatal("database driver accepted a nil database")
	}
}

func typeName(store cache.Store) string {
	switch store.(type) {
	case *cache.Memory:
		return "*cache.Memory"
	case *cache.File:
		return "*cache.File"
	case *cache.SQLStore:
		return "*cache.SQLStore"
	}
	return "unknown"
}
