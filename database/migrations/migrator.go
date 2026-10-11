package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/dayanchm/golar/database/connection"
	"github.com/dayanchm/golar/database/schema"
	"io"
	"sort"
	"time"
)

type Migrator struct {
	DB          *connection.DB
	Registry    *Registry
	Out         io.Writer
	LockTimeout time.Duration
}

func New(db *connection.DB) *Migrator {
	return &Migrator{DB: db, Registry: DefaultRegistry, Out: io.Discard}
}
func (m *Migrator) output() io.Writer {
	if m.Out == nil {
		return io.Discard
	}
	return m.Out
}
func (m *Migrator) prepare(ctx context.Context, conn *sql.Conn) (Repository, []Record, error) {
	r := Repository{Executor: conn, Dialect: m.DB.Driver}
	if err := r.Ensure(ctx); err != nil {
		return r, nil, err
	}
	dirty, err := r.Dirty(ctx)
	if err != nil {
		return r, nil, err
	}
	if dirty != "" {
		return r, nil, fmt.Errorf("migration: %q is dirty after partial MySQL DDL; inspect and repair before clearing golar_migration_state", dirty)
	}
	records, err := r.Records(ctx)
	return r, records, err
}
func (m *Migrator) Migrate(ctx context.Context) error {
	return m.locked(ctx, func(conn *sql.Conn) error { return m.migrate(ctx, conn) })
}
func (m *Migrator) migrate(ctx context.Context, conn *sql.Conn) error {
	_, records, err := m.prepare(ctx, conn)
	if err != nil {
		return err
	}
	applied := map[string]bool{}
	batch := 0
	for _, record := range records {
		applied[record.Name] = true
		if record.Batch > batch {
			batch = record.Batch
		}
	}
	batch++
	count := 0
	for _, entry := range m.Registry.Entries() {
		if applied[entry.Name] {
			continue
		}
		if err = m.execute(ctx, conn, entry, batch, false); err != nil {
			return err
		}
		count++
	}
	fmt.Fprintf(m.output(), "%d migrations completed.\n", count)
	return nil
}
func (m *Migrator) execute(ctx context.Context, conn *sql.Conn, entry Entry, batch int, down bool) (err error) {
	started := time.Now()
	direction := "Up"
	if down {
		direction = "Down"
	}
	fmt.Fprintf(m.output(), "RUNNING %s (%s)\n", entry.Name, direction)
	var executor schema.Executor = conn
	var tx *sql.Tx
	if m.DB.Driver == "postgres" {
		tx, err = conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		executor = tx
		defer tx.Rollback()
	}
	r := Repository{Executor: executor, Dialect: m.DB.Driver}
	if m.DB.Driver == "mysql" {
		if err = r.MarkDirty(ctx, entry.Name); err != nil {
			return err
		}
	}
	s := schema.NewContext(ctx, executor, m.DB.Driver)
	if down {
		err = entry.Migration.Down(s)
	} else {
		err = entry.Migration.Up(s)
	}
	if err != nil {
		return fmt.Errorf("migration: failed %s %q: %w", direction, entry.Name, err)
	}
	if m.DB.Driver == "mysql" {
		tx, err = conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		r.Executor = tx
	}
	if down {
		err = r.Delete(ctx, entry.Name)
	} else {
		err = r.Record(ctx, entry.Name, batch)
	}
	if err != nil {
		return fmt.Errorf("migration: record %q: %w", entry.Name, err)
	}
	if m.DB.Driver == "mysql" {
		if err = r.ClearDirty(ctx, entry.Name); err != nil {
			return err
		}
	}
	if tx != nil {
		if err = tx.Commit(); err != nil {
			return err
		}
	}
	fmt.Fprintf(m.output(), "DONE %s %s\n", entry.Name, time.Since(started))
	return nil
}
func (m *Migrator) Rollback(ctx context.Context) error {
	return m.locked(ctx, func(conn *sql.Conn) error { return m.rollback(ctx, conn, false) })
}
func (m *Migrator) Reset(ctx context.Context) error {
	return m.locked(ctx, func(conn *sql.Conn) error { return m.rollback(ctx, conn, true) })
}
func (m *Migrator) Refresh(ctx context.Context) error {
	return m.locked(ctx, func(conn *sql.Conn) error {
		if err := m.rollback(ctx, conn, true); err != nil {
			return err
		}
		return m.migrate(ctx, conn)
	})
}
func (m *Migrator) rollback(ctx context.Context, conn *sql.Conn, all bool) error {
	_, records, err := m.prepare(ctx, conn)
	if err != nil {
		return err
	}
	entries := map[string]Entry{}
	for _, entry := range m.Registry.Entries() {
		entries[entry.Name] = entry
	}
	latest := 0
	for _, r := range records {
		if r.Batch > latest {
			latest = r.Batch
		}
	}
	selected := []Record{}
	for _, r := range records {
		if all || r.Batch == latest {
			if _, ok := entries[r.Name]; !ok {
				return fmt.Errorf("migration: missing registered migration %q", r.Name)
			}
			selected = append(selected, r)
		}
	}
	sort.Slice(selected, func(i, j int) bool {
		if selected[i].Batch != selected[j].Batch {
			return selected[i].Batch > selected[j].Batch
		}
		return selected[i].Name > selected[j].Name
	})
	for _, r := range selected {
		if err = m.execute(ctx, conn, entries[r.Name], r.Batch, true); err != nil {
			return err
		}
	}
	fmt.Fprintf(m.output(), "%d migrations rolled back.\n", len(selected))
	return nil
}

type Status struct {
	Name  string
	Batch int
	Ran   bool
}

func (m *Migrator) Status(ctx context.Context) (statuses []Status, err error) {
	err = m.locked(ctx, func(conn *sql.Conn) error {
		_, records, e := m.prepare(ctx, conn)
		if e != nil {
			return e
		}
		known := map[string]Status{}
		for _, entry := range m.Registry.Entries() {
			known[entry.Name] = Status{Name: entry.Name}
		}
		for _, record := range records {
			known[record.Name] = Status{Name: record.Name, Batch: record.Batch, Ran: true}
		}
		for _, status := range known {
			statuses = append(statuses, status)
		}
		sort.Slice(statuses, func(i, j int) bool { return statuses[i].Name < statuses[j].Name })
		return nil
	})
	return
}

// Plan runs schema-only migration code without a connection or migration metadata writes.
func Plan(entries []Entry, dialect string, out io.Writer) error {
	if _, err := schema.NewCompiler(dialect); err != nil {
		return err
	}
	for _, entry := range entries {
		fmt.Fprintf(out, "-- %s (all registered migrations; no database accessed)\n", entry.Name)
		if err := entry.Migration.Up(schema.Pretend(dialect, out)); err != nil {
			return fmt.Errorf("migration: pretend %q: %w", entry.Name, err)
		}
	}
	return nil
}
