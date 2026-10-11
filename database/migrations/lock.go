package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"time"
)

func (m *Migrator) locked(ctx context.Context, run func(*sql.Conn) error) (err error) {
	timeout := m.LockTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	lockCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := m.DB.Conn(lockCtx)
	if err != nil {
		return fmt.Errorf("migration: acquire connection: %w", err)
	}
	defer conn.Close()
	release := func(success bool) error { return nil }
	switch m.DB.Driver {
	case "sqlite":
		if _, err = conn.ExecContext(lockCtx, "PRAGMA foreign_keys=OFF"); err != nil {
			return err
		}
		if _, err = conn.ExecContext(lockCtx, fmt.Sprintf("PRAGMA busy_timeout(%d)", timeout.Milliseconds())); err != nil {
			return err
		}
		if _, err = conn.ExecContext(lockCtx, "BEGIN IMMEDIATE"); err != nil {
			_, _ = conn.ExecContext(context.Background(), "PRAGMA foreign_keys=ON")
			return fmt.Errorf("migration: acquire sqlite lock: %w", err)
		}
		release = func(success bool) error {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			statement := "ROLLBACK"
			if success {
				statement = "COMMIT"
			}
			_, e := conn.ExecContext(cleanup, statement)
			if e != nil && success {
				_, _ = conn.ExecContext(cleanup, "ROLLBACK")
			}
			_, restore := conn.ExecContext(cleanup, "PRAGMA foreign_keys=ON")
			return errors.Join(e, restore)
		}
	case "postgres":
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			var acquired bool
			err = conn.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock(742819003)").Scan(&acquired)
			if err != nil {
				return err
			}
			if acquired {
				break
			}
			select {
			case <-lockCtx.Done():
				return fmt.Errorf("migration: lock timeout: %w", lockCtx.Err())
			case <-ticker.C:
			}
		}
		release = func(bool) error {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var result bool
			e := conn.QueryRowContext(cleanup, "SELECT pg_advisory_unlock(742819003)").Scan(&result)
			if e == nil && !result {
				e = fmt.Errorf("migration: postgres lock was lost")
			}
			return e
		}
	case "mysql":
		var database string
		if err = conn.QueryRowContext(lockCtx, "SELECT DATABASE()").Scan(&database); err != nil {
			return err
		}
		digest := fmt.Sprintf("%x", sha256.Sum256([]byte(database)))
		key := "golar_migrations_" + digest[:32]
		var acquired int
		if err = conn.QueryRowContext(lockCtx, "SELECT GET_LOCK(?, ?)", key, int(timeout.Seconds())+1).Scan(&acquired); err != nil || acquired != 1 {
			cause := errors.Join(err, lockCtx.Err())
			if cause == nil {
				cause = fmt.Errorf("another runner holds the lock")
			}
			return fmt.Errorf("migration: mysql lock unavailable: %w", cause)
		}
		release = func(bool) error {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var result int
			e := conn.QueryRowContext(cleanup, "SELECT RELEASE_LOCK(?)", key).Scan(&result)
			if e == nil && result != 1 {
				e = fmt.Errorf("migration: mysql lock was lost")
			}
			return e
		}
	default:
		return fmt.Errorf("migration: unsupported dialect %q", m.DB.Driver)
	}
	defer func() {
		value := recover()
		cleanupErr := release(err == nil && value == nil)
		if cleanupErr != nil {
			_ = conn.Raw(func(any) error { return sqldriver.ErrBadConn })
		}
		err = errors.Join(err, cleanupErr)
		if value != nil {
			panic(value)
		}
	}()
	err = run(conn)
	if err == nil && m.DB.Driver == "sqlite" {
		rows, checkErr := conn.QueryContext(ctx, "PRAGMA foreign_key_check")
		if checkErr != nil {
			err = checkErr
		} else {
			violation := rows.Next()
			err = errors.Join(rows.Err(), rows.Close())
			if violation {
				err = errors.Join(err, fmt.Errorf("migration: foreign key violation; batch rolled back"))
			}
		}
	}
	return err
}
