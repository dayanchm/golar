package query_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dayanchm/golar/database"
)

// connect opens SQLite by default, or the database named by GOLAR_TEST_DRIVER
// and GOLAR_TEST_DSN, and creates query_users and query_posts tables.
func connect(t *testing.T) (*database.DB, string) {
	t.Helper()
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" {
		driver = "sqlite"
	}
	config := database.Config{Driver: driver, Database: filepath.Join(t.TempDir(), "query.db")}
	if driver != "sqlite" {
		config.Database = ""
		config.DSN = os.Getenv("GOLAR_TEST_DSN")
		if config.DSN == "" {
			t.Fatal("GOLAR_TEST_DSN is required")
		}
	}
	db, err := database.Connect(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ctx := context.Background()
	id := "INTEGER PRIMARY KEY"
	switch driver {
	case "postgres":
		id = "SERIAL PRIMARY KEY"
	case "mysql":
		id = "INTEGER PRIMARY KEY AUTO_INCREMENT"
	}
	for _, statement := range []string{
		"DROP TABLE IF EXISTS query_posts",
		"DROP TABLE IF EXISTS query_users",
		"CREATE TABLE query_users (id " + id + ", name VARCHAR(100) NOT NULL, age INTEGER, email VARCHAR(100))",
		"CREATE TABLE query_posts (id " + id + ", user_id INTEGER NOT NULL, title VARCHAR(100) NOT NULL)",
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		db.ExecContext(context.Background(), "DROP TABLE IF EXISTS query_posts")
		db.ExecContext(context.Background(), "DROP TABLE IF EXISTS query_users")
	})
	return db, driver
}

// seed inserts users 1..len(names) with ages 10, 20, ... and returns them in id order.
func seed(t *testing.T, db *database.DB, names ...string) {
	t.Helper()
	for i, name := range names {
		if _, err := db.Table("query_users").Insert(map[string]any{"id": i + 1, "name": name, "age": (i + 1) * 10}); err != nil {
			t.Fatal(err)
		}
	}
}

func names(t *testing.T, db *database.DB) []string {
	t.Helper()
	rows, err := db.Table("query_users").Select("name").OrderBy("id", "asc").QueryContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		result = append(result, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
