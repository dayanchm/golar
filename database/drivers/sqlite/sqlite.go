package sqlite

import (
	"database/sql"
	"fmt"
	"github.com/dayanchm/golar/database/connection"
	_ "modernc.org/sqlite"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Driver struct{}

func (Driver) Name() string { return "sqlite" }
func (Driver) Open(c connection.Config) (*sql.DB, error) {
	dsn := c.DSN
	if dsn == "" {
		dsn = c.Database
	}
	if dsn != ":memory:" && !strings.HasPrefix(dsn, "file:") {
		if err := os.MkdirAll(filepath.Dir(dsn), 0755); err != nil {
			return nil, err
		}
	}
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	dsn += separator + "_pragma=" + url.QueryEscape("foreign_keys(1)") + "&_pragma=" + url.QueryEscape(fmt.Sprintf("busy_timeout(%d)", c.ConnectTimeout.Milliseconds()))
	return sql.Open("sqlite", dsn)
}
func init() {
	if err := connection.RegisterDriver(Driver{}); err != nil {
		panic(err)
	}
}
