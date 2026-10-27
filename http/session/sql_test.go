package session_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/schema"
	"github.com/dayanchm/golar/http/session"
)

// connect opens SQLite in dir by default, or the database named by
// GOLAR_TEST_DRIVER and GOLAR_TEST_DSN, with a fresh sessions table.
func connect(t *testing.T, dir string, fresh bool) *database.DB {
	t.Helper()
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(dir, "sessions.db")}
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
	s := schema.New(db.DB, driver)
	if fresh {
		if err := s.DropIfExists(session.Table); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if db := connectQuiet(config); db != nil {
				_ = schema.New(db.DB, driver).DropIfExists(session.Table)
				db.Close()
			}
		})
	}
	if err := session.CreateTables(s); err != nil {
		t.Fatal(err)
	}
	if err := session.CreateTables(s); err != nil {
		t.Fatalf("CreateTables is not idempotent: %v", err)
	}
	return db
}

func connectQuiet(config database.Config) *database.DB {
	db, err := database.Connect(config)
	if err != nil {
		return nil
	}
	return db
}

func TestSQLStore(t *testing.T) {
	db := connect(t, t.TempDir(), true)
	defer db.Close()
	ctx := context.Background()
	store := &session.SQLStore{DB: db}

	if _, ok, err := store.Load(ctx, "missing"); ok || err != nil {
		t.Fatalf("Load(missing) = %v, %v", ok, err)
	}
	expires := time.Now().Add(time.Hour)
	if err := store.Save(ctx, "id-1", map[string]string{"user": "1", "name": "Ayşe"}, expires); err != nil {
		t.Fatal(err)
	}
	data, ok, err := store.Load(ctx, "id-1")
	if err != nil || !ok || data["user"] != "1" || data["name"] != "Ayşe" {
		t.Fatalf("Load = %v, %v, %v", data, ok, err)
	}

	// Saving again with the same data and expiry updates in place.
	for range 2 {
		if err := store.Save(ctx, "id-1", map[string]string{"user": "2"}, expires); err != nil {
			t.Fatal(err)
		}
	}
	if data, _, _ := store.Load(ctx, "id-1"); data["user"] != "2" || len(data) != 1 {
		t.Fatalf("after update: %v", data)
	}
	if n, err := db.Table(session.Table).Count(ctx); n != 1 || err != nil {
		t.Fatalf("rows = %d, %v", n, err)
	}

	// The raw ID is never stored.
	if n, _ := db.Table(session.Table).Where("id", "=", "id-1").Count(ctx); n != 0 {
		t.Fatal("session ID stored in plain text")
	}

	if err := store.Delete(ctx, "id-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Load(ctx, "id-1"); ok {
		t.Fatal("deleted session loaded")
	}
	if err := store.Delete(ctx, "id-1"); err != nil {
		t.Fatalf("deleting a missing session: %v", err)
	}
}

func TestSQLStoreExpiryAndPrune(t *testing.T) {
	db := connect(t, t.TempDir(), true)
	defer db.Close()
	ctx := context.Background()
	store := &session.SQLStore{DB: db}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(store.Save(ctx, "old-1", map[string]string{"k": "v"}, time.Now().Add(-time.Hour)))
	must(store.Save(ctx, "old-2", nil, time.Now().Add(-time.Minute)))
	must(store.Save(ctx, "live", map[string]string{"k": "v"}, time.Now().Add(time.Hour)))
	if _, ok, err := store.Load(ctx, "old-1"); ok || err != nil {
		t.Fatalf("expired Load = %v, %v", ok, err)
	}
	n, err := store.Prune(ctx)
	if err != nil || n != 2 {
		t.Fatalf("Prune = %d, %v; want 2", n, err)
	}
	if _, ok, _ := store.Load(ctx, "live"); !ok {
		t.Fatal("Prune removed a live session")
	}

	must(store.Save(ctx, "old-3", nil, time.Now().Add(-time.Minute)))
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { store.PruneEvery(ctx, 10*time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if count, _ := db.Table(session.Table).Count(context.Background()); count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PruneEvery did not prune")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("PruneEvery did not stop")
	}
}

func TestSQLStoreConcurrentSaves(t *testing.T) {
	db := connect(t, t.TempDir(), true)
	defer db.Close()
	store := &session.SQLStore{DB: db}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			errs <- store.Save(context.Background(), "shared", map[string]string{"n": string(rune('a' + i))}, time.Now().Add(time.Hour))
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n, _ := db.Table(session.Table).Count(context.Background()); n != 1 {
		t.Fatalf("rows = %d, want 1", n)
	}
}

func TestSQLStoreAcrossInstancesAndRestarts(t *testing.T) {
	dir := t.TempDir()
	first := connect(t, dir, true)
	second := connect(t, dir, false)
	defer second.Close()
	instanceA := &session.Manager{Store: &session.SQLStore{DB: first}, Insecure: true}
	instanceB := &session.Manager{Store: &session.SQLStore{DB: second}, Insecure: true}

	put := httptest.NewRecorder()
	instanceA.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session.FromContext(r.Context()).Put("user", "42")
	})).ServeHTTP(put, httptest.NewRequest(http.MethodGet, "/", nil))
	cookies := put.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v", cookies)
	}

	read := func(m *session.Manager) string {
		var got string
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(cookies[0])
		m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = session.FromContext(r.Context()).Get("user")
		})).ServeHTTP(httptest.NewRecorder(), r)
		return got
	}
	if got := read(instanceB); got != "42" {
		t.Fatalf("second instance read %q", got)
	}

	// Restart: close the first connection and open a new one.
	first.Close()
	restarted := connect(t, dir, false)
	defer restarted.Close()
	if got := read(&session.Manager{Store: &session.SQLStore{DB: restarted}}); got != "42" {
		t.Fatalf("after restart read %q", got)
	}
}
