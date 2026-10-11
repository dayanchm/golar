package connection_test

import (
	"context"
	"errors"
	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/connection"
	"path/filepath"
	"sync"
	"testing"
)

func config(t *testing.T) database.Config {
	return database.Config{Driver: "sqlite", Database: filepath.Join(t.TempDir(), "app.db")}
}
func TestManager(t *testing.T) {
	m := database.NewManager()
	db, err := m.Register("default", config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer m.CloseAll()
	if got, err := m.Connection(""); err != nil || got != db {
		t.Fatal(err)
	}
	if _, err = m.Register("default", config(t)); err == nil {
		t.Fatal("duplicate accepted")
	}
	if _, err = m.Connection("missing"); err == nil {
		t.Fatal("missing connection accepted")
	}
	if err = m.Close("default"); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Connection("default"); err == nil {
		t.Fatal("closed connection retained")
	}
}
func TestConcurrentManager(t *testing.T) {
	m := database.NewManager()
	defer m.CloseAll()
	var wg sync.WaitGroup
	cfg := config(t)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = m.Register("default", cfg); _, _ = m.Connection("default") }()
	}
	wg.Wait()
	if _, err := m.Connection("default"); err != nil {
		t.Fatal(err)
	}
}
func TestConfig(t *testing.T) {
	for _, cfg := range []database.Config{{Driver: "oracle"}, {Driver: "sqlite"}, {Driver: "mysql", Database: "x", Port: 70000}, {Driver: "postgres", Database: "x", SSLMode: "bad"}, {Driver: "mysql", Database: "x", MaxOpenConnections: 1, MaxIdleConnections: 2}} {
		if _, err := cfg.Normalize(); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
	cfg, err := connection.FromLookup(func(k string) (string, bool) {
		values := map[string]string{"DB_DRIVER": "postgres", "DB_DATABASE": "demo", "DB_PORT": "5433", "DB_CONNECT_TIMEOUT": "2s", "DB_SSLMODE": "disable"}
		v, ok := values[k]
		return v, ok
	})
	if err != nil || cfg.Port != 5433 || cfg.SSLMode != "disable" {
		t.Fatalf("config=%+v err=%v", cfg, err)
	}
}
func TestTransactions(t *testing.T) {
	db, err := database.Connect(config(t))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	if err = db.Transaction(func(tx *database.Tx) error { _, err := tx.Exec("INSERT INTO items VALUES (?)", 1); return err }); err != nil {
		t.Fatal(err)
	}
	want := errors.New("rollback")
	if err = db.Transaction(func(tx *database.Tx) error { _, _ = tx.Exec("INSERT INTO items VALUES (?)", 2); return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() != "panic" {
				t.Error("panic was swallowed")
			}
		}()
		_ = db.Transaction(func(tx *database.Tx) error { _, _ = tx.Exec("INSERT INTO items VALUES (?)", 3); panic("panic") })
	}()
	var count int
	if err = db.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
}

func TestPoolAndCancellation(t *testing.T) {
	cfg := config(t)
	cfg.MaxOpenConnections = 3
	cfg.MaxIdleConnections = 2
	db, err := database.Connect(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if db.Stats().MaxOpenConnections != 3 {
		t.Fatal(db.Stats())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.TransactionContext(ctx, func(*database.Tx) error { t.Error("callback ran after cancellation"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
