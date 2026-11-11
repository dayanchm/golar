package queue

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/dayanchm/golar/database/orm"
	"github.com/dayanchm/golar/database/schema"
)

// Default table names for SQLStore.
const (
	JobsTable   = "jobs"
	FailedTable = "failed_jobs"
)

// JobColumns defines the jobs table. Times are Unix milliseconds;
// reserved_until is 0 for a job no worker holds. A migration can create it
// with s.Create(queue.JobsTable, queue.JobColumns), which also works with
// migrate --pretend.
func JobColumns(t *schema.Blueprint) {
	t.String("id", 32).Primary()
	t.String("queue", 64)
	t.String("name", 191)
	t.Text("payload")
	t.Integer("attempts")
	t.Integer("max_tries")
	t.BigInteger("available_at")
	t.BigInteger("reserved_until")
	t.String("token", 32)
	t.BigInteger("created_at")
	t.Index("queue", "available_at")
}

// FailedColumns defines the failed jobs table.
func FailedColumns(t *schema.Blueprint) {
	t.String("id", 32).Primary()
	t.String("queue", 64)
	t.String("name", 191)
	t.Text("payload")
	t.Text("error")
	t.BigInteger("failed_at")
}

// CreateTables creates the jobs and failed jobs tables if they are missing.
// It checks the database, so it cannot run under migrate --pretend.
func CreateTables(s *schema.Schema) error {
	for _, table := range []struct {
		name    string
		columns func(*schema.Blueprint)
	}{{JobsTable, JobColumns}, {FailedTable, FailedColumns}} {
		ok, err := s.HasTable(table.name)
		if err != nil {
			return err
		}
		if !ok {
			if err := s.Create(table.name, table.columns); err != nil {
				return err
			}
		}
	}
	return nil
}

// SQLStore keeps jobs in database tables, so they survive restarts and are
// shared by every worker. A job is reserved by compare-and-swap on a random
// token, so two workers never hold it at once. Payloads are limited by the
// TEXT column: 64 KiB on MySQL.
type SQLStore struct {
	DB orm.DB
	// Table and FailedTable default to JobsTable and FailedTable.
	Table, FailedTable string
	now                func() time.Time
}

func (s *SQLStore) jobs() string {
	if s.Table != "" {
		return s.Table
	}
	return JobsTable
}

func (s *SQLStore) failed() string {
	if s.FailedTable != "" {
		return s.FailedTable
	}
	return FailedTable
}

func (s *SQLStore) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *SQLStore) Push(ctx context.Context, job Record) error {
	_, err := s.DB.Table(s.jobs()).InsertContext(ctx, map[string]any{
		"id": job.ID, "queue": job.Queue, "name": job.Name, "payload": string(job.Payload),
		"attempts": job.Attempts, "max_tries": job.MaxTries, "available_at": job.AvailableAt.UnixMilli(),
		"reserved_until": int64(0), "token": "", "created_at": s.clock().UnixMilli(),
	})
	return err
}

// reserveBatch is how many candidates Reserve reads at once; several
// workers racing for the first job fall through to the next ones.
const reserveBatch = 10

func (s *SQLStore) Reserve(ctx context.Context, queues []string, lease time.Duration) (Record, bool, error) {
	for _, name := range queues { // in priority order
		now := s.clock().UnixMilli()
		rows, err := s.DB.Table(s.jobs()).Select("id", "token", "attempts").
			Where("queue", "=", name).Where("available_at", "<=", now).Where("reserved_until", "<", now).
			OrderBy("available_at", "asc").Limit(reserveBatch).QueryContext(ctx)
		if err != nil {
			return Record{}, false, err
		}
		type candidate struct {
			id, token string
			attempts  int
		}
		var candidates []candidate
		for rows.Next() {
			var c candidate
			if err := rows.Scan(&c.id, &c.token, &c.attempts); err != nil {
				rows.Close()
				return Record{}, false, err
			}
			candidates = append(candidates, c)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return Record{}, false, err
		}
		for _, c := range candidates {
			token := newID()
			result, err := s.DB.Table(s.jobs()).Where("id", "=", c.id).Where("token", "=", c.token).
				Where("reserved_until", "<", now).
				UpdateContext(ctx, map[string]any{"token": token, "reserved_until": now + lease.Milliseconds(), "attempts": c.attempts + 1})
			if err != nil {
				return Record{}, false, err
			}
			if n, err := result.RowsAffected(); err != nil {
				return Record{}, false, err
			} else if n == 0 {
				continue // another worker took it
			}
			job, err := s.read(ctx, c.id)
			if err != nil {
				return Record{}, false, err
			}
			return job, true, nil
		}
	}
	return Record{}, false, nil
}

func (s *SQLStore) read(ctx context.Context, id string) (Record, error) {
	rows, err := s.DB.Table(s.jobs()).Select("id", "queue", "name", "payload", "attempts", "max_tries", "available_at", "token").
		Where("id", "=", id).Limit(1).QueryContext(ctx)
	if err != nil {
		return Record{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Record{}, errors.Join(rows.Err(), sql.ErrNoRows)
	}
	var job Record
	var payload string
	var available int64
	if err := rows.Scan(&job.ID, &job.Queue, &job.Name, &payload, &job.Attempts, &job.MaxTries, &available, &job.Token); err != nil {
		return Record{}, err
	}
	job.Payload, job.AvailableAt = []byte(payload), time.UnixMilli(available)
	return job, rows.Close()
}

// held reports whether a write that matched the job's token changed a row.
func held(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLeaseLost
	}
	return nil
}

func (s *SQLStore) Delete(ctx context.Context, job Record) error {
	return held(s.DB.Table(s.jobs()).Where("id", "=", job.ID).Where("token", "=", job.Token).DeleteContext(ctx))
}

func (s *SQLStore) Release(ctx context.Context, job Record, at time.Time) error {
	return held(s.DB.Table(s.jobs()).Where("id", "=", job.ID).Where("token", "=", job.Token).
		UpdateContext(ctx, map[string]any{"available_at": at.UnixMilli(), "reserved_until": int64(0), "token": ""}))
}

// Fail deletes the job while its reservation is still held, then records it
// as failed. If recording fails after the delete, the error says so; the
// job is not run again.
func (s *SQLStore) Fail(ctx context.Context, job Record, reason string) error {
	if err := s.Delete(ctx, job); err != nil {
		return err
	}
	_, err := s.DB.Table(s.failed()).InsertContext(ctx, map[string]any{
		"id": job.ID, "queue": job.Queue, "name": job.Name, "payload": string(job.Payload),
		"error": reason, "failed_at": s.clock().UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("queue: job %s was removed but not recorded as failed: %w", job.ID, err)
	}
	return nil
}

func (s *SQLStore) Failed(ctx context.Context) ([]FailedJob, error) {
	rows, err := s.DB.Table(s.failed()).Select("id", "queue", "name", "payload", "error", "failed_at").
		OrderBy("failed_at", "asc").QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var jobs []FailedJob
	for rows.Next() {
		var job FailedJob
		var payload string
		var failed int64
		if err := rows.Scan(&job.ID, &job.Queue, &job.Name, &payload, &job.Error, &failed); err != nil {
			return nil, err
		}
		job.Payload, job.FailedAt = []byte(payload), time.UnixMilli(failed)
		jobs = append(jobs, job)
	}
	return jobs, errors.Join(rows.Err(), rows.Close())
}

func (s *SQLStore) Retry(ctx context.Context, id string) error {
	jobs, err := s.Failed(ctx)
	if err != nil {
		return err
	}
	for _, failed := range jobs {
		if failed.ID != id {
			continue
		}
		if rows, err := s.DB.Table(s.failed()).Where("id", "=", id).DeleteContext(ctx); err != nil {
			return err
		} else if n, _ := rows.RowsAffected(); n == 0 {
			return nil // retried by someone else
		}
		// MaxTries 0 gives the job the handler's current Tries.
		if err := s.Push(ctx, Record{ID: newID(), Queue: failed.Queue, Name: failed.Name, Payload: failed.Payload, AvailableAt: s.clock()}); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("queue: no failed job %q", id)
}

func (s *SQLStore) Forget(ctx context.Context, id string) error {
	_, err := s.DB.Table(s.failed()).Where("id", "=", id).DeleteContext(ctx)
	return err
}
