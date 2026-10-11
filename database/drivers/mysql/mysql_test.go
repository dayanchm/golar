package mysql

import (
	"github.com/dayanchm/golar/database/connection"
	"testing"
)

func TestOpenConfiguration(t *testing.T) {
	config, err := (connection.Config{Driver: "mysql", Host: "::1", Database: "demo", Username: "user", Password: "p@ss:/?#", SSLMode: "false"}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	db, err := (Driver{}).Open(config)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
}
