package migrations_test

import (
	"bytes"
	"context"
	"errors"
	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/migrations"
	"github.com/dayanchm/golar/database/schema"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type migration struct {
	table string
	fail  bool
}

func (m migration) Up(s *schema.Schema) error {
	if err := s.Create(m.table, func(b *schema.Blueprint) {
		b.ID()
		b.String("name", 255)
		b.String("email", 255).Unique()
		b.Timestamps()
	}); err != nil {
		return err
	}
	if m.fail {
		return errTest
	}
	return nil
}
func (m migration) Down(s *schema.Schema) error { return s.DropIfExists(m.table) }

var errTest = errors.New("expected migration error")

func connect(t *testing.T) *database.DB {
	t.Helper()
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(t.TempDir(), "test.db")}
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
	for _, table := range []string{"children", "parents", "posts", "users", "failed", "golar_migration_state", "golar_migrations"} {
		if err = s.DropIfExists(table); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func runner(t *testing.T) (*migrations.Migrator, *database.DB) {
	db := connect(t)
	m := migrations.New(db)
	m.Registry = migrations.NewRegistry()
	if err := m.Registry.Register("20261010_120000_create_users_table", migration{table: "users"}); err != nil {
		t.Fatal(err)
	}
	return m, db
}
func TestMigrationLifecycle(t *testing.T) {
	m, db := runner(t)
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	statuses, err := m.Status(ctx)
	if err != nil || len(statuses) != 1 || statuses[0].Batch != 1 || !statuses[0].Ran {
		t.Fatalf("statuses=%v err=%v", statuses, err)
	}
	if err = m.Registry.Register("20261011_120000_create_posts_table", migration{table: "posts"}); err != nil {
		t.Fatal(err)
	}
	if err = m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err = m.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s := schema.New(db.DB, db.Driver)
	if found, err := s.HasTable("users"); err != nil || !found {
		t.Fatal(err)
	}
	if found, err := s.HasTable("posts"); err != nil || found {
		t.Fatal(err)
	}
	if err = m.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err = m.Reset(ctx); err != nil {
		t.Fatal(err)
	}
	statuses, err = m.Status(ctx)
	if err != nil || statuses[0].Ran || statuses[1].Ran {
		t.Fatalf("statuses=%v err=%v", statuses, err)
	}
}
func TestFailureSafety(t *testing.T) {
	m, db := runner(t)
	if err := m.Registry.Register("20261011_120000_create_failed_table", migration{table: "failed", fail: true}); err != nil {
		t.Fatal(err)
	}
	err := m.Migrate(context.Background())
	if !errors.Is(err, errTest) {
		t.Fatal(err)
	}
	var count int
	if db.Driver == "sqlite" {
		exists, _ := schema.New(db.DB, db.Driver).HasTable("golar_migrations")
		if exists {
			t.Fatal("sqlite batch did not roll back")
		}
	} else {
		if err = db.QueryRow("SELECT COUNT(*) FROM golar_migrations WHERE migration LIKE '%failed%'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("failed migration recorded: %d %v", count, err)
		}
	}
	if db.Driver == "mysql" {
		if err = m.Migrate(context.Background()); err == nil || !strings.Contains(err.Error(), "dirty") {
			t.Fatalf("dirty run accepted: %v", err)
		}
	}
}
func TestRegistryRace(t *testing.T) {
	r := migrations.NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.Register("20261010_120000_create_users_table", migration{table: "users"})
			_ = r.Entries()
		}()
	}
	wg.Wait()
	if len(r.Entries()) != 1 {
		t.Fatal(r.Entries())
	}
	for _, name := range []string{"invalid", "20261340_create_users_table", "../escape"} {
		if err := r.Register(name, migration{}); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
}
func TestPlanWithoutDatabase(t *testing.T) {
	r := migrations.NewRegistry()
	_ = r.Register("20261010_120000_create_users_table", migration{table: "users"})
	var out bytes.Buffer
	if err := migrations.Plan(r.Entries(), "sqlite", &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "CREATE TABLE") {
		t.Fatal(out.String())
	}
}

type blocking struct {
	started chan struct{}
	release chan struct{}
}

func (m blocking) Up(*schema.Schema) error   { close(m.started); <-m.release; return nil }
func (m blocking) Down(*schema.Schema) error { return nil }
func TestMigrationLock(t *testing.T) {
	db := connect(t)
	m := migrations.New(db)
	m.Registry = migrations.NewRegistry()
	block := blocking{make(chan struct{}), make(chan struct{})}
	_ = m.Registry.Register("20261010_120000_blocking", block)
	done := make(chan error, 1)
	go func() { done <- m.Migrate(context.Background()) }()
	select {
	case <-block.started:
	case <-time.After(5 * time.Second):
		t.Fatal("did not start")
	}
	other := migrations.New(db)
	other.Registry = migrations.NewRegistry()
	other.LockTimeout = 100 * time.Millisecond
	err := other.Migrate(context.Background())
	close(block.release)
	if err == nil {
		t.Error("concurrent runner acquired lock")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseTransactionsIndexesAndForeignKeys(t *testing.T) {
	db := connect(t)
	s := schema.New(db.DB, db.Driver)
	if err := s.Create("parents", func(b *schema.Blueprint) { b.ID(); b.String("name", 100).Unique() }); err != nil {
		t.Fatal(err)
	}
	if err := s.Create("children", func(b *schema.Blueprint) {
		b.ID()
		b.ForeignID("parent_id").Constrained("parents").CascadeOnDelete()
		b.String("name", 100)
		b.Index("parent_id", "name")
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO children (parent_id,name) VALUES (999,'invalid')"); err == nil {
		t.Fatal("foreign key not enforced")
	}
	if err := db.Transaction(func(tx *database.Tx) error {
		_, err := tx.Exec("INSERT INTO parents (name) VALUES ('parent')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO parents (name) VALUES ('parent')"); err == nil {
		t.Fatal("unique index not enforced")
	}
	var id int64
	if err := db.QueryRow("SELECT id FROM parents WHERE name='parent'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	query := "INSERT INTO children (parent_id,name) VALUES (?,'child')"
	if db.Driver == "postgres" {
		query = "INSERT INTO children (parent_id,name) VALUES ($1,'child')"
	}
	if _, err := db.Exec(query, id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("DELETE FROM parents"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM children").Scan(&count); err != nil || count != 0 {
		t.Fatalf("cascade count=%d error=%v", count, err)
	}
}

type failDown struct{}

func (failDown) Up(s *schema.Schema) error {
	return s.Create("users", func(b *schema.Blueprint) { b.ID() })
}
func (failDown) Down(*schema.Schema) error { return errTest }
func TestRollbackFailure(t *testing.T) {
	db := connect(t)
	m := migrations.New(db)
	m.Registry = migrations.NewRegistry()
	_ = m.Registry.Register("20261010_120000_create_users_table", failDown{})
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(ctx); !errors.Is(err, errTest) {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM golar_migrations").Scan(&count); err != nil || count != 1 {
		t.Fatalf("ledger removed on failed rollback: %d %v", count, err)
	}
}
func TestMissingRollbackSource(t *testing.T) {
	m, db := runner(t)
	ctx := context.Background()
	if err := m.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	m.Registry = migrations.NewRegistry()
	if err := m.Rollback(ctx); err == nil || !strings.Contains(err.Error(), "missing registered") {
		t.Fatalf("missing source rollback error: %v", err)
	}
	exists, err := schema.New(db.DB, db.Driver).HasTable("users")
	if err != nil || !exists {
		t.Fatalf("table changed: %v %v", exists, err)
	}
}

func TestSQLiteLockAcrossConnections(t *testing.T) {
	if driver := os.Getenv("GOLAR_TEST_DRIVER"); driver != "" && driver != "sqlite" {
		t.Skip("SQLite-specific lock test")
	}
	config := database.Config{Driver: "sqlite", Database: filepath.Join(t.TempDir(), "shared.db")}
	db, err := database.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	second, err := database.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	first := migrations.New(db)
	first.Registry = migrations.NewRegistry()
	block := blocking{make(chan struct{}), make(chan struct{})}
	_ = first.Registry.Register("20261010_120000_blocking", block)
	done := make(chan error, 1)
	go func() { done <- first.Migrate(context.Background()) }()
	select {
	case <-block.started:
	case <-time.After(5 * time.Second):
		t.Fatal("first runner did not start")
	}
	other := migrations.New(second)
	other.Registry = migrations.NewRegistry()
	other.LockTimeout = 100 * time.Millisecond
	err = other.Migrate(context.Background())
	close(block.release)
	if err == nil {
		t.Error("separate SQLite connection acquired write lock")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestRepository(t *testing.T) {
	db := connect(t)
	r := migrations.Repository{Executor: db.DB, Dialect: db.Driver}
	ctx := context.Background()
	if err := r.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Record(ctx, "20261010_120000_create_users_table", 3); err != nil {
		t.Fatal(err)
	}
	records, err := r.Records(ctx)
	if err != nil || len(records) != 1 || records[0].Batch != 3 || records[0].ExecutedAt == "" {
		t.Fatalf("records=%v err=%v", records, err)
	}
	if err = r.MarkDirty(ctx, "20261010_120000_create_users_table"); err != nil {
		t.Fatal(err)
	}
	if name, err := r.Dirty(ctx); err != nil || name == "" {
		t.Fatal(err)
	}
	if err = r.ClearDirty(ctx, "20261010_120000_create_users_table"); err != nil {
		t.Fatal(err)
	}
	if err = r.Delete(ctx, "20261010_120000_create_users_table"); err != nil {
		t.Fatal(err)
	}
	records, err = r.Records(ctx)
	if err != nil || len(records) != 0 {
		t.Fatal(err)
	}
}
