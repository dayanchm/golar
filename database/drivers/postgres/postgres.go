package postgres

import (
	"database/sql"
	"github.com/dayanchm/golar/database/connection"
	_ "github.com/jackc/pgx/v5/stdlib"
	"net"
	"net/url"
	"strconv"
)

type Driver struct{}

func (Driver) Name() string { return "postgres" }
func (Driver) Open(c connection.Config) (*sql.DB, error) {
	dsn := c.DSN
	if dsn == "" {
		u := url.URL{Scheme: "postgres", Host: net.JoinHostPort(c.Host, strconv.Itoa(c.Port)), Path: "/" + c.Database, User: url.UserPassword(c.Username, c.Password)}
		q := u.Query()
		q.Set("sslmode", c.SSLMode)
		seconds := int(c.ConnectTimeout.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		q.Set("connect_timeout", strconv.Itoa(seconds))
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	return sql.Open("pgx", dsn)
}
func init() {
	if err := connection.RegisterDriver(Driver{}); err != nil {
		panic(err)
	}
}
