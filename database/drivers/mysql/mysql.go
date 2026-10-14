package mysql

import (
	"database/sql"
	"net"
	"strconv"

	"github.com/dayanchm/golar/database/connection"
	driver "github.com/go-sql-driver/mysql"
)

type Driver struct{}

func (Driver) Name() string { return "mysql" }
func (Driver) Open(c connection.Config) (*sql.DB, error) {
	dsn := c.DSN
	if dsn == "" {
		config := driver.NewConfig()
		config.User = c.Username
		config.Passwd = c.Password
		config.Net = "tcp"
		config.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
		config.DBName = c.Database
		config.ParseTime = true
		config.Timeout = c.ConnectTimeout
		config.TLSConfig = c.SSLMode
		dsn = config.FormatDSN()
	}
	return sql.Open("mysql", dsn)
}
func init() {
	if err := connection.RegisterDriver(Driver{}); err != nil {
		panic(err)
	}
}
