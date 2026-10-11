package connection

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
)

type Driver interface {
	Name() string
	Open(Config) (*sql.DB, error)
}

var registry = struct {
	sync.RWMutex
	drivers map[string]Driver
}{drivers: make(map[string]Driver)}

func RegisterDriver(driver Driver) error {
	registry.Lock()
	defer registry.Unlock()
	if driver == nil || driver.Name() == "" {
		return errors.New("database: invalid driver")
	}
	if _, ok := registry.drivers[driver.Name()]; ok {
		return fmt.Errorf("database: duplicate driver %q", driver.Name())
	}
	registry.drivers[driver.Name()] = driver
	return nil
}
func Open(config Config) (*DB, error) { return OpenContext(context.Background(), config) }
func OpenContext(parent context.Context, config Config) (*DB, error) {
	c, err := config.Normalize()
	if err != nil {
		return nil, err
	}
	registry.RLock()
	driver, ok := registry.drivers[c.Driver]
	registry.RUnlock()
	if !ok {
		return nil, fmt.Errorf("database: driver %q is not registered", c.Driver)
	}
	raw, err := driver.Open(c)
	if err != nil {
		return nil, fmt.Errorf("database: open %s: %w", c.Driver, err)
	}
	raw.SetMaxOpenConns(c.MaxOpenConnections)
	raw.SetMaxIdleConns(c.MaxIdleConnections)
	raw.SetConnMaxLifetime(c.ConnectionLifetime)
	ctx, cancel := context.WithTimeout(parent, c.ConnectTimeout)
	defer cancel()
	if err = raw.PingContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("database: ping %s: %w", c.Driver, err)
	}
	return &DB{DB: raw, Driver: c.Driver}, nil
}

type Connection interface {
	Exec(string, ...any) (sql.Result, error)
	Query(string, ...any) (*sql.Rows, error)
	QueryRow(string, ...any) *sql.Row
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	Ping() error
	PingContext(context.Context) error
	Close() error
	Transaction(func(*Tx) error) error
}

type DB struct {
	*sql.DB
	Driver string
}
type Tx struct {
	*sql.Tx
	Driver string
}

func (db *DB) Transaction(fn func(*Tx) error) error {
	return db.TransactionContext(context.Background(), fn)
}
func (db *DB) TransactionContext(ctx context.Context, fn func(*Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("database: begin transaction: %w", err)
	}
	defer func() {
		if value := recover(); value != nil {
			_ = tx.Rollback()
			panic(value)
		}
	}()
	if err = fn(&Tx{Tx: tx, Driver: db.Driver}); err != nil {
		rollbackErr := tx.Rollback()
		if errors.Is(rollbackErr, sql.ErrTxDone) {
			rollbackErr = nil
		}
		return errors.Join(err, rollbackErr)
	}
	return tx.Commit()
}
