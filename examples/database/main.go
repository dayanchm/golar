package main

import (
	"context"
	"fmt"
	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/migrations"
	"github.com/dayanchm/golar/database/schema"
	"log"
	"os"
	"path/filepath"
)

type createUsers struct{}

func (createUsers) Up(s *schema.Schema) error {
	return s.Create("users", func(b *schema.Blueprint) {
		b.ID()
		b.String("name", 255)
		b.String("email", 255).Unique()
		b.Timestamps()
	})
}
func (createUsers) Down(s *schema.Schema) error { return s.DropIfExists("users") }
func run() error {
	dir, err := os.MkdirTemp("", "golar-database-example-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	db, err := database.Connect(database.Config{Driver: "sqlite", Database: filepath.Join(dir, "example.db")})
	if err != nil {
		return err
	}
	defer db.Close()
	m := migrations.New(db)
	m.Registry = migrations.NewRegistry()
	m.Out = os.Stdout
	if err = m.Registry.Register("20261011_120000_create_users_table", createUsers{}); err != nil {
		return err
	}
	if err = m.Migrate(context.Background()); err != nil {
		return err
	}
	if err = db.Transaction(func(tx *database.Tx) error {
		_, err := tx.Exec("INSERT INTO users (name,email) VALUES (?,?)", "Ali", "ali@example.com")
		return err
	}); err != nil {
		return err
	}
	var name string
	if err = db.QueryRow("SELECT name FROM users WHERE email=?", "ali@example.com").Scan(&name); err != nil {
		return err
	}
	fmt.Println("User:", name)
	return m.Rollback(context.Background())
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
