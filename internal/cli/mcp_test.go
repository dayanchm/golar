package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/internal/scaffold"
)

// mcpSession sends requests, one JSON object per line, and returns the
// responses by request ID.
func mcpSession(t *testing.T, c *CLI, root string, requests ...string) map[string]map[string]any {
	t.Helper()
	c.In = strings.NewReader(strings.Join(requests, "\n") + "\n")
	var out strings.Builder
	c.Out = &out
	if err := c.Run(context.Background(), []string{"mcp", "--dir", root}); err != nil {
		t.Fatal(err)
	}
	responses := map[string]map[string]any{}
	scanner := bufio.NewScanner(strings.NewReader(out.String()))
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var resp map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &resp); err != nil {
			t.Fatalf("invalid response %q: %v", scanner.Text(), err)
		}
		responses[fmt.Sprint(resp["id"])] = resp
	}
	return responses
}

func toolCall(id int, tool string, args string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":%q,"arguments":%s}}`, id, tool, args)
}

// toolText returns a tool result's text and whether it is an error.
func toolText(t *testing.T, resp map[string]any) (string, bool) {
	t.Helper()
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("no result: %v", resp)
	}
	content := result["content"].([]any)[0].(map[string]any)
	isError, _ := result["isError"].(bool)
	return content["text"].(string), isError
}

func TestMCP(t *testing.T) {
	c, _ := testCLI(t)
	root, err := scaffold.Create(c.Dir, scaffold.Options{Name: "shop", Database: "sqlite", Auth: "token"})
	if err != nil {
		t.Fatal(err)
	}
	// A database with one table, as golar migrate would leave it.
	db, err := database.Connect(database.Config{Driver: "sqlite", Database: filepath.Join(root, "storage", "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT NOT NULL, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	var ran [][]string
	c.run = func(_ context.Context, _ string, _ []string, out io.Writer, args ...string) error {
		ran = append(ran, args)
		if args[len(args)-1] == "--routes" {
			fmt.Fprint(out, "GET\t/health\nPOST\t/api/auth/login\n")
		}
		return nil
	}

	responses := mcpSession(t, c, root,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"test","version":"1"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		toolCall(3, "project_info", `{}`),
		toolCall(4, "list_routes", `{}`),
		toolCall(5, "describe_schema", `{}`),
		toolCall(6, "describe_schema", `{"table":"posts"}`),
		toolCall(7, "list_models", `{}`),
		toolCall(8, "list_migrations", `{}`),
		toolCall(9, "describe_schema", `{"table":"missing"}`),
		`{"jsonrpc":"2.0","id":10,"method":"resources/list"}`,
		`not json`,
		toolCall(11, "delete_everything", `{}`),
	)

	init := responses["1"]["result"].(map[string]any)
	if init["protocolVersion"] != "2025-03-26" || init["serverInfo"].(map[string]any)["name"] != "golar" {
		t.Errorf("initialize = %v", init)
	}
	if _, ok := responses["<nil>"]; !ok {
		t.Error("parse error got no response")
	}
	tools := responses["2"]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 5 {
		t.Fatalf("tools = %v", tools)
	}
	for _, tool := range tools {
		annotations := tool.(map[string]any)["annotations"].(map[string]any)
		if annotations["readOnlyHint"] != true || annotations["destructiveHint"] != false {
			t.Errorf("tool %v is not marked read-only", tool.(map[string]any)["name"])
		}
	}

	info, _ := toolText(t, responses["3"])
	if !strings.Contains(info, `"name": "shop"`) || !strings.Contains(info, `"APP_KEY"`) {
		t.Errorf("project_info = %s", info)
	}
	routes, _ := toolText(t, responses["4"])
	if !strings.Contains(routes, `"path": "/api/auth/login"`) || len(ran) != 2 || ran[0][1] != "build" {
		t.Errorf("list_routes = %s, ran %v", routes, ran)
	}
	schema, _ := toolText(t, responses["6"])
	var tables []schemaTable
	if err := json.Unmarshal([]byte(schema), &tables); err != nil || len(tables) != 1 || len(tables[0].Columns) != 3 {
		t.Fatalf("describe_schema(posts) = %s, %v", schema, err)
	}
	if id, title, body := tables[0].Columns[0], tables[0].Columns[1], tables[0].Columns[2]; !id.Primary || title.Nullable || !body.Nullable || title.Type != "TEXT" {
		t.Errorf("columns = %+v", tables[0].Columns)
	}
	if all, _ := toolText(t, responses["5"]); !strings.Contains(all, `"posts"`) {
		t.Errorf("describe_schema = %s", all)
	}
	models, _ := toolText(t, responses["7"])
	if !strings.Contains(models, `"name": "User"`) || !strings.Contains(models, `"type": "*time.Time"`) || !strings.Contains(models, `"json": "-"`) {
		t.Errorf("list_models = %s", models)
	}
	migrations, _ := toolText(t, responses["8"])
	if !strings.Contains(migrations, "20260101_000000_create_users_table") || !strings.Contains(migrations, "20260101_000001_create_cache_table") {
		t.Errorf("list_migrations = %s", migrations)
	}
	if text, isError := toolText(t, responses["9"]); !isError || !strings.Contains(text, `no table "missing"`) {
		t.Errorf("missing table = %s, %v", text, isError)
	}
	if responses["10"]["error"].(map[string]any)["code"].(float64) != -32601 {
		t.Errorf("unknown method = %v", responses["10"])
	}
	if responses["11"]["error"] == nil {
		t.Errorf("unknown tool = %v", responses["11"])
	}

	// No .env value is ever returned.
	env, _ := os.ReadFile(filepath.Join(root, ".env"))
	for _, line := range strings.Split(string(env), "\n") {
		if _, value, ok := strings.Cut(line, "="); ok && len(value) > 8 {
			for id, resp := range responses {
				if body, _ := json.Marshal(resp); strings.Contains(string(body), value) {
					t.Errorf("response %s contains the .env value %q", id, value)
				}
			}
		}
	}
}

func TestMCPRedactsSecretsFromErrors(t *testing.T) {
	c, _ := testCLI(t)
	root, err := scaffold.Create(c.Dir, scaffold.Options{Name: "shop", Database: "postgres"})
	if err != nil {
		t.Fatal(err)
	}
	// An unreachable server whose DSN carries a password; drivers can echo
	// parts of the DSN in their errors.
	env := filepath.Join(root, ".env")
	body, _ := os.ReadFile(env)
	dsn := "postgres://golar:supersecret-password@127.0.0.1:1/shop_db?sslmode=disable&connect_timeout=1"
	var lines []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "DATABASE_DSN=") {
			line = "DATABASE_DSN=" + dsn
		}
		lines = append(lines, line)
	}
	os.WriteFile(env, []byte(strings.Join(lines, "\n")), 0o600)

	responses := mcpSession(t, c, root, toolCall(1, "describe_schema", `{}`))
	text, isError := toolText(t, responses["1"])
	if !isError {
		t.Fatalf("describe_schema succeeded: %s", text)
	}
	for _, secret := range []string{"supersecret-password", dsn} {
		if strings.Contains(text, secret) {
			t.Errorf("error reveals %q: %s", secret, text)
		}
	}
	s := &mcpServer{root: root}
	if got := s.redact("dial " + dsn + " failed for supersecret-password"); strings.Contains(got, "supersecret") {
		t.Errorf("redact = %q", got)
	}
}

// TestReadSchemaServers checks the information_schema queries when
// GOLAR_TEST_DRIVER and GOLAR_TEST_DSN name PostgreSQL or MySQL.
func TestReadSchemaServers(t *testing.T) {
	driver := os.Getenv("GOLAR_TEST_DRIVER")
	if driver == "" || driver == "sqlite" {
		t.Skip("set GOLAR_TEST_DRIVER and GOLAR_TEST_DSN to test PostgreSQL or MySQL")
	}
	db, err := database.Connect(database.Config{Driver: driver, DSN: os.Getenv("GOLAR_TEST_DSN")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.Exec(`DROP TABLE IF EXISTS mcp_posts`)
	if _, err := db.Exec(`CREATE TABLE mcp_posts (id BIGINT PRIMARY KEY, title VARCHAR(100) NOT NULL, body TEXT)`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DROP TABLE IF EXISTS mcp_posts`) })
	tables, err := readSchema(context.Background(), db, driver, "mcp_posts")
	if err != nil || len(tables) != 1 || len(tables[0].Columns) != 3 {
		t.Fatalf("readSchema = %+v, %v", tables, err)
	}
	id, title, body := tables[0].Columns[0], tables[0].Columns[1], tables[0].Columns[2]
	if id.Name != "id" || !id.Primary || title.Primary || title.Nullable || !body.Nullable {
		t.Errorf("columns = %+v", tables[0].Columns)
	}
	if all, err := readSchema(context.Background(), db, driver, ""); err != nil || len(all) == 0 {
		t.Errorf("all tables = %d, %v", len(all), err)
	}
}
