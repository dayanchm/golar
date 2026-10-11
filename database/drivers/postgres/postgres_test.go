package postgres

import (
	"github.com/dayanchm/golar/database/connection"
	"testing"
)

func TestOpenConfiguration(t *testing.T) {
	config, err := (connection.Config{Driver: "postgres", Host: "::1", Database: "demo", Username: "user", Password: "p@ss:/?#", SSLMode: "disable"}).Normalize()
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
