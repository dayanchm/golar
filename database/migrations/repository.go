package migrations

import (
	"context"
	"fmt"
	"github.com/dayanchm/golar/database/schema"
	"time"
)

type Record struct {
	Name       string
	Batch      int
	ExecutedAt string
}
type Repository struct {
	Executor schema.Executor
	Dialect  string
}

func (r Repository) placeholder(n int) string {
	if r.Dialect == "postgres" {
		return fmt.Sprintf("$%d", n)
	}
	return "?"
}
func (r Repository) Ensure(ctx context.Context) error {
	s := schema.NewContext(ctx, r.Executor, r.Dialect)
	for _, table := range []string{"golar_migrations", "golar_migration_state"} {
		exists, err := s.HasTable(table)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if table == "golar_migrations" {
			err = s.Create(table, func(b *schema.Blueprint) {
				b.ID()
				b.String("migration", 200).Unique()
				b.Integer("batch")
				b.Timestamp("executed_at").Default(schema.CurrentTimestamp)
			})
		} else {
			err = s.Create(table, func(b *schema.Blueprint) { b.String("migration", 200).Primary() })
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (r Repository) Records(ctx context.Context) ([]Record, error) {
	rows, err := r.Executor.QueryContext(ctx, "SELECT migration,batch,executed_at FROM golar_migrations ORDER BY migration")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		var record Record
		var stamp any
		if err = rows.Scan(&record.Name, &record.Batch, &stamp); err != nil {
			return nil, err
		}
		switch v := stamp.(type) {
		case time.Time:
			record.ExecutedAt = v.UTC().Format(time.RFC3339)
		case []byte:
			record.ExecutedAt = string(v)
		default:
			record.ExecutedAt = fmt.Sprint(v)
		}
		records = append(records, record)
	}
	return records, rows.Err()
}
func (r Repository) Record(ctx context.Context, name string, batch int) error {
	_, err := r.Executor.ExecContext(ctx, "INSERT INTO golar_migrations (migration,batch) VALUES ("+r.placeholder(1)+","+r.placeholder(2)+")", name, batch)
	return err
}
func (r Repository) Delete(ctx context.Context, name string) error {
	_, err := r.Executor.ExecContext(ctx, "DELETE FROM golar_migrations WHERE migration="+r.placeholder(1), name)
	return err
}
func (r Repository) Dirty(ctx context.Context) (string, error) {
	rows, err := r.Executor.QueryContext(ctx, "SELECT migration FROM golar_migration_state ORDER BY migration")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return "", err
		}
		return name, nil
	}
	return "", rows.Err()
}
func (r Repository) MarkDirty(ctx context.Context, name string) error {
	_, err := r.Executor.ExecContext(ctx, "INSERT INTO golar_migration_state (migration) VALUES ("+r.placeholder(1)+")", name)
	return err
}
func (r Repository) ClearDirty(ctx context.Context, name string) error {
	_, err := r.Executor.ExecContext(ctx, "DELETE FROM golar_migration_state WHERE migration="+r.placeholder(1), name)
	return err
}
