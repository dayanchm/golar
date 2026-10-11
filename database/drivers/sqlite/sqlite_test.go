package sqlite

import (
	"github.com/dayanchm/golar/database/connection"
	"testing"
)

func TestOpen(t *testing.T) {
	config, err := (connection.Config{Driver: "sqlite", Database: ":memory:"}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	db, err := (Driver{}).Open(config)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var enabled int
	if err = db.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("foreign_keys=%d err=%v", enabled, err)
	}
}
