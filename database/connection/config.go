package connection

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	Driver, DSN, Host, Database, Username, Password, SSLMode string
	Port                                                     int
	ConnectTimeout                                           time.Duration
	MaxOpenConnections, MaxIdleConnections                   int
	ConnectionLifetime                                       time.Duration
}

func (c Config) Normalize() (Config, error) {
	switch c.Driver {
	case "sqlite", "postgres", "mysql":
	default:
		return c, fmt.Errorf("database: unsupported driver %q", c.Driver)
	}
	if c.Database == "" && c.DSN == "" {
		return c, fmt.Errorf("database: database name or DSN is required")
	}
	if c.Host == "" {
		c.Host = "localhost"
	}
	if c.Port == 0 {
		if c.Driver == "postgres" {
			c.Port = 5432
		}
		if c.Driver == "mysql" {
			c.Port = 3306
		}
	}
	if c.Port < 0 || c.Port > 65535 {
		return c, fmt.Errorf("database: invalid port %d", c.Port)
	}
	if c.ConnectTimeout == 0 {
		c.ConnectTimeout = 5 * time.Second
	}
	if c.ConnectTimeout < 0 || c.ConnectionLifetime < 0 || c.MaxOpenConnections < 0 || c.MaxIdleConnections < 0 {
		return c, fmt.Errorf("database: timeouts and pool limits must not be negative")
	}
	if c.MaxOpenConnections == 0 {
		c.MaxOpenConnections = 10
		if c.Driver == "sqlite" {
			c.MaxOpenConnections = 1
		}
	}
	if c.MaxIdleConnections == 0 {
		c.MaxIdleConnections = 1
	}
	if c.MaxIdleConnections > c.MaxOpenConnections {
		return c, fmt.Errorf("database: idle connections exceed open connection limit")
	}
	if c.ConnectionLifetime == 0 {
		c.ConnectionLifetime = 30 * time.Minute
	}
	if c.SSLMode == "" {
		if c.Driver == "postgres" {
			c.SSLMode = "require"
		} else {
			c.SSLMode = "false"
		}
	}
	if c.Driver == "postgres" {
		switch c.SSLMode {
		case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
		default:
			return c, fmt.Errorf("database: invalid postgres SSL mode %q", c.SSLMode)
		}
	}
	if c.Driver == "mysql" {
		switch c.SSLMode {
		case "false", "true", "skip-verify", "preferred":
		default:
			return c, fmt.Errorf("database: invalid mysql TLS mode %q", c.SSLMode)
		}
	}
	return c, nil
}

// FromEnv reads DB_* settings. DSN is optional; structured settings are the default.
func FromEnv() (Config, error) { return FromLookup(os.LookupEnv) }
func FromLookup(get func(string) (string, bool)) (Config, error) {
	value := func(key string) string { v, _ := get(key); return v }
	c := Config{Driver: value("DB_DRIVER"), DSN: value("DATABASE_DSN"), Host: value("DB_HOST"), Database: value("DB_DATABASE"), Username: value("DB_USERNAME"), Password: value("DB_PASSWORD"), SSLMode: value("DB_SSLMODE")}
	if c.Driver == "" {
		c.Driver = "sqlite"
	}
	if c.Database == "" && c.DSN == "" && c.Driver == "sqlite" {
		c.Database = "storage/app.db"
	}
	for _, field := range []struct {
		key    string
		target *int
	}{{"DB_PORT", &c.Port}, {"DB_MAX_OPEN_CONNECTIONS", &c.MaxOpenConnections}, {"DB_MAX_IDLE_CONNECTIONS", &c.MaxIdleConnections}} {
		if s := value(field.key); s != "" {
			v, e := strconv.Atoi(s)
			if e != nil {
				return c, fmt.Errorf("database: invalid %s: %w", field.key, e)
			}
			*field.target = v
		}
	}
	for _, field := range []struct {
		key    string
		target *time.Duration
	}{{"DB_CONNECT_TIMEOUT", &c.ConnectTimeout}, {"DB_CONNECTION_LIFETIME", &c.ConnectionLifetime}} {
		if s := value(field.key); s != "" {
			v, e := time.ParseDuration(s)
			if e != nil {
				return c, fmt.Errorf("database: invalid %s: %w", field.key, e)
			}
			*field.target = v
		}
	}
	return c.Normalize()
}
