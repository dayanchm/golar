// Package database provides SQL connections and named connection management.
package database

import (
	"context"
	"github.com/dayanchm/golar/database/connection"
	_ "github.com/dayanchm/golar/database/drivers/mysql"
	_ "github.com/dayanchm/golar/database/drivers/postgres"
	_ "github.com/dayanchm/golar/database/drivers/sqlite"
)

type Config = connection.Config
type DB = connection.DB
type Tx = connection.Tx
type Manager = connection.Manager

var DefaultManager = connection.NewManager()

func Connect(config Config) (*DB, error) { return connection.Open(config) }
func ConfigFromEnv() (Config, error)     { return connection.FromEnv() }
func ConfigFromLookup(get func(string) (string, bool)) (Config, error) {
	return connection.FromLookup(get)
}
func NewManager() *Manager                             { return connection.NewManager() }
func Register(name string, config Config) (*DB, error) { return DefaultManager.Register(name, config) }
func Connection(name string) (*DB, error)              { return DefaultManager.Connection(name) }
func Close(name string) error                          { return DefaultManager.Close(name) }
func CloseAll() error                                  { return DefaultManager.CloseAll() }

func ConnectContext(ctx context.Context, config Config) (*DB, error) {
	return connection.OpenContext(ctx, config)
}
