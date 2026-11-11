package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/dayanchm/golar/database"
	"github.com/dayanchm/golar/database/connection"
	"github.com/dayanchm/golar/internal/project"
)

// MCP protocol versions golar mcp speaks, newest first.
var mcpVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// mcpTool is one read-only tool. golar mcp exposes no tool that writes:
// it never runs migrations, changes files or returns .env values.
type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
	call        func(context.Context, map[string]any) (any, error)
}

type mcpServer struct {
	c       *CLI
	root    string
	config  project.Config
	env     []string
	tools   []mcpTool
	buildMu sync.Mutex
	binary  string // built server, for list_routes
}

// mcp serves the Model Context Protocol on stdin and stdout so coding agents
// can read the project's routes, schema, models and migrations.
func (c *CLI) mcp(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("mcp", flag.ContinueOnError)
	flags.SetOutput(c.Err)
	root := flags.String("dir", c.Dir, "project directory")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if !filepath.IsAbs(*root) {
		*root = filepath.Join(c.Dir, *root)
	}
	config, err := project.Load(*root)
	if err != nil {
		return err
	}
	env, err := project.Env(*root, os.Environ())
	if err != nil {
		return err
	}
	// Stdin carries the protocol, so commands the tools run must not read it.
	in := c.In
	c.In = strings.NewReader("")
	s := &mcpServer{c: c, root: *root, config: config, env: env}
	s.tools = s.toolList()
	tmp, err := os.MkdirTemp("", "golar-mcp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	s.binary = filepath.Join(tmp, "server")
	if runtime.GOOS == "windows" {
		s.binary += ".exe"
	}
	return s.serve(ctx, in, c.Out)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// serve reads one JSON-RPC message per line and writes one response per
// request; notifications get none.
func (s *mcpServer) serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	encoder := json.NewEncoder(out)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req rpcRequest
		if err := json.Unmarshal(line, &req); err != nil || req.Method == "" {
			if err := encoder.Encode(rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{-32700, "parse error"}}); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue // a notification, such as notifications/initialized
		}
		result, rerr := s.handle(ctx, req)
		resp := rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result, Error: rerr}
		if rerr == nil && result == nil {
			resp.Result = struct{}{}
		}
		if err := encoder.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *mcpServer) handle(ctx context.Context, req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &params)
		version := mcpVersions[0]
		for _, v := range mcpVersions {
			if v == params.ProtocolVersion {
				version = v
			}
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "golar", "version": Version},
			"instructions": "Read-only tools for the Golar project " + s.config.Name +
				": its configuration, HTTP routes, database schema, ORM models and migrations. Nothing here changes the project or reveals .env values.",
		}, nil
	case "ping":
		return struct{}{}, nil
	case "tools/list":
		return map[string]any{"tools": s.tools}, nil
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return nil, &rpcError{-32602, "invalid params"}
		}
		for _, tool := range s.tools {
			if tool.Name != params.Name {
				continue
			}
			value, err := tool.call(ctx, params.Arguments)
			if err != nil {
				return map[string]any{"content": []map[string]any{{"type": "text", "text": s.redact(err.Error())}}, "isError": true}, nil
			}
			text, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				return nil, &rpcError{-32603, err.Error()}
			}
			return map[string]any{"content": []map[string]any{{"type": "text", "text": s.redact(string(text))}}}, nil
		}
		return nil, &rpcError{-32602, fmt.Sprintf("unknown tool %q", params.Name)}
	default:
		return nil, &rpcError{-32601, "method not found: " + req.Method}
	}
}

// publicSettings are .env variables whose values are not secret; every other
// value is redacted from tool output.
var publicSettings = map[string]bool{
	"APP_NAME": true, "APP_ADDR": true, "APP_URL": true, "APP_ENV": true, "DB_DRIVER": true,
	"CACHE_DRIVER": true, "QUEUE_DRIVER": true, "SESSION_DRIVER": true, "SESSION_SECURE_COOKIE": true,
	"MAIL_DRIVER": true, "MAIL_PORT": true, "MAIL_ENCRYPTION": true, "MAIL_FROM_NAME": true,
}

// redact removes .env values from text, so a tool never reveals a DSN,
// password or key, even inside an error message.
func (s *mcpServer) redact(text string) string {
	values := map[string]bool{}
	for _, entry := range s.envFile() {
		if name, value, ok := strings.Cut(entry, "="); ok && !publicSettings[name] && len(value) >= 4 {
			values[value] = true
			// Also the parts of URLs and DSNs, such as a password.
			for _, part := range regexp.MustCompile(`[:@/?&=]`).Split(value, -1) {
				if len(part) >= 6 {
					values[part] = true
				}
			}
		}
	}
	sorted := make([]string, 0, len(values))
	for value := range values {
		sorted = append(sorted, value)
	}
	sort.Slice(sorted, func(i, j int) bool { return len(sorted[i]) > len(sorted[j]) })
	for _, value := range sorted {
		text = strings.ReplaceAll(text, value, "[redacted]")
	}
	return text
}

// envFile returns the .env entries alone, without the inherited environment.
func (s *mcpServer) envFile() []string {
	entries, err := project.Env(s.root, nil)
	if err != nil {
		return nil
	}
	return entries
}

func readOnly(title string) map[string]any {
	return map[string]any{"title": title, "readOnlyHint": true, "destructiveHint": false, "openWorldHint": false}
}

func (s *mcpServer) toolList() []mcpTool {
	noArgs := map[string]any{"type": "object", "properties": map[string]any{}}
	return []mcpTool{
		{
			Name:        "project_info",
			Description: "The project's golar.yaml settings (template, database, frontend, auth) and the names, not values, of the variables in .env.",
			InputSchema: noArgs, Annotations: readOnly("Project info"),
			call: s.projectInfo,
		},
		{
			Name:        "list_routes",
			Description: "The HTTP routes the application registers, as method and path. Builds the server, which can take a few seconds the first time.",
			InputSchema: noArgs, Annotations: readOnly("Routes"),
			call: s.listRoutes,
		},
		{
			Name:        "describe_schema",
			Description: "Tables and columns of the project's database, read from DATABASE_DSN. Run golar migrate first. Pass table to describe one table.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{
				"table": map[string]any{"type": "string", "description": "only this table"},
			}},
			Annotations: readOnly("Database schema"),
			call:        s.describeSchema,
		},
		{
			Name:        "list_models",
			Description: "ORM models in app/models: struct names, fields with types and tags, and table names from TableName methods.",
			InputSchema: noArgs, Annotations: readOnly("Models"),
			call: s.listModels,
		},
		{
			Name:        "list_migrations",
			Description: "Migrations registered in database/migrations, in order, with their files.",
			InputSchema: noArgs, Annotations: readOnly("Migrations"),
			call: s.listMigrations,
		},
	}
}

func (s *mcpServer) projectInfo(context.Context, map[string]any) (any, error) {
	var names []string
	for _, entry := range s.envFile() {
		name, _, _ := strings.Cut(entry, "=")
		names = append(names, name)
	}
	sort.Strings(names)
	return map[string]any{
		"name": s.config.Name, "module": s.config.Module, "template": s.config.Template,
		"database": s.config.Database, "frontend": s.config.Frontend, "auth": s.config.Auth,
		"docker": s.config.Docker, "env_variables": names, "golar_cli": Version,
	}, nil
}

func (s *mcpServer) listRoutes(ctx context.Context, _ map[string]any) (any, error) {
	s.buildMu.Lock()
	defer s.buildMu.Unlock()
	var build bytes.Buffer
	if err := s.c.run(ctx, s.root, s.env, &build, "go", "build", "-o", s.binary, "./cmd/server"); err != nil {
		return nil, fmt.Errorf("build application: %w\n%s", err, build.String())
	}
	var out bytes.Buffer
	if err := s.c.run(ctx, s.root, s.env, &out, s.binary, "--routes"); err != nil {
		return nil, fmt.Errorf("list routes: %w", err)
	}
	type route struct {
		Method string `json:"method"`
		Path   string `json:"path"`
	}
	var routes []route
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if method, path, ok := strings.Cut(line, "\t"); ok {
			routes = append(routes, route{method, path})
		}
	}
	return routes, nil
}

func (s *mcpServer) describeSchema(ctx context.Context, args map[string]any) (any, error) {
	if s.config.Database == "none" {
		return nil, errors.New("this project has no database")
	}
	values := map[string]string{}
	for _, entry := range s.env {
		if name, value, ok := strings.Cut(entry, "="); ok {
			values[name] = value
		}
	}
	config, err := connection.FromLookup(func(name string) (string, bool) { v, ok := values[name]; return v, ok })
	if err != nil {
		return nil, err
	}
	if config.Driver == "sqlite" {
		// Resolve the file from the project, and never create it.
		path := config.DSN
		if path == "" {
			path = config.Database
		}
		if path != ":memory:" && !strings.HasPrefix(path, "file:") && !filepath.IsAbs(path) {
			path = filepath.Join(s.root, path)
		}
		if _, err := os.Stat(path); err != nil {
			return nil, errors.New("the SQLite database does not exist yet; run golar migrate")
		}
		config.DSN, config.Database = path, path
	}
	db, err := database.ConnectContext(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect to the database: %w", err)
	}
	defer db.Close()
	only, _ := args["table"].(string)
	return readSchema(ctx, db, config.Driver, only)
}

type schemaColumn struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Primary  bool   `json:"primary_key,omitempty"`
}

type schemaTable struct {
	Name    string         `json:"name"`
	Columns []schemaColumn `json:"columns"`
}

func readSchema(ctx context.Context, db *database.DB, driver, only string) ([]schemaTable, error) {
	var tables []schemaTable
	switch driver {
	case "sqlite":
		rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
		if err != nil {
			return nil, err
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return nil, err
			}
			if only == "" || name == only {
				names = append(names, name)
			}
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
		for _, name := range names {
			table := schemaTable{Name: name}
			cols, err := db.QueryContext(ctx, `SELECT name, type, "notnull", pk FROM pragma_table_info(?)`, name)
			if err != nil {
				return nil, err
			}
			for cols.Next() {
				var col schemaColumn
				var notNull, pk int
				if err := cols.Scan(&col.Name, &col.Type, &notNull, &pk); err != nil {
					cols.Close()
					return nil, err
				}
				col.Nullable, col.Primary = notNull == 0 && pk == 0, pk > 0
				table.Columns = append(table.Columns, col)
			}
			if err := errors.Join(cols.Err(), cols.Close()); err != nil {
				return nil, err
			}
			tables = append(tables, table)
		}
	case "postgres", "mysql":
		query := `SELECT c.table_name, c.column_name, c.data_type, c.is_nullable,
			EXISTS (SELECT 1 FROM information_schema.table_constraints t
				JOIN information_schema.key_column_usage k
				  ON k.constraint_name = t.constraint_name AND k.table_schema = t.table_schema AND k.table_name = t.table_name
				WHERE t.constraint_type = 'PRIMARY KEY' AND t.table_schema = c.table_schema
				  AND t.table_name = c.table_name AND k.column_name = c.column_name)
			FROM information_schema.columns c
			JOIN information_schema.tables tb ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name AND tb.table_type = 'BASE TABLE'
			WHERE c.table_schema = %s
			ORDER BY c.table_name, c.ordinal_position`
		schemaExpr := "current_schema()"
		if driver == "mysql" {
			schemaExpr = "DATABASE()"
		}
		rows, err := db.QueryContext(ctx, fmt.Sprintf(query, schemaExpr))
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var table, nullable string
			var col schemaColumn
			if err := rows.Scan(&table, &col.Name, &col.Type, &nullable, &col.Primary); err != nil {
				return nil, err
			}
			if only != "" && table != only {
				continue
			}
			col.Nullable = nullable == "YES"
			if len(tables) == 0 || tables[len(tables)-1].Name != table {
				tables = append(tables, schemaTable{Name: table})
			}
			tables[len(tables)-1].Columns = append(tables[len(tables)-1].Columns, col)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported driver %q", driver)
	}
	if only != "" && len(tables) == 0 {
		return nil, fmt.Errorf("no table %q", only)
	}
	return tables, nil
}

type modelField struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"`
	Embedded bool              `json:"embedded,omitempty"`
	Tags     map[string]string `json:"tags,omitempty"`
}

type model struct {
	Name   string       `json:"name"`
	File   string       `json:"file"`
	Table  string       `json:"table,omitempty"`
	Doc    string       `json:"doc,omitempty"`
	Fields []modelField `json:"fields"`
}

func (s *mcpServer) listModels(context.Context, map[string]any) (any, error) {
	dir := filepath.Join(s.root, "app", "models")
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	models := map[string]*model{}
	tables := map[string]string{}
	var order []string
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(s.root, file)
		for _, decl := range parsed.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					st, ok := ts.Type.(*ast.StructType)
					if !ok {
						continue
					}
					m := &model{Name: ts.Name.Name, File: filepath.ToSlash(rel), Doc: strings.TrimSpace(d.Doc.Text())}
					for _, field := range st.Fields.List {
						typ := exprString(field.Type)
						tags := map[string]string{}
						if field.Tag != nil {
							tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))
							for _, key := range []string{"db", "json", "validate", "form"} {
								if value, ok := tag.Lookup(key); ok {
									tags[key] = value
								}
							}
						}
						if len(tags) == 0 {
							tags = nil
						}
						if len(field.Names) == 0 {
							m.Fields = append(m.Fields, modelField{Name: typ, Type: typ, Embedded: true, Tags: tags})
						}
						for _, name := range field.Names {
							if name.IsExported() {
								m.Fields = append(m.Fields, modelField{Name: name.Name, Type: typ, Tags: tags})
							}
						}
					}
					models[m.Name] = m
					order = append(order, m.Name)
				}
			case *ast.FuncDecl:
				// func (T) TableName() string { return "name" }
				if d.Name.Name != "TableName" || d.Recv == nil || len(d.Recv.List) != 1 || d.Body == nil || len(d.Body.List) != 1 {
					continue
				}
				ret, ok := d.Body.List[0].(*ast.ReturnStmt)
				if !ok || len(ret.Results) != 1 {
					continue
				}
				if lit, ok := ret.Results[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if name, err := strconv.Unquote(lit.Value); err == nil {
						tables[strings.TrimPrefix(exprString(d.Recv.List[0].Type), "*")] = name
					}
				}
			}
		}
	}
	list := make([]model, 0, len(order))
	for _, name := range order {
		m := models[name]
		m.Table = tables[name]
		list = append(list, *m)
	}
	return list, nil
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		return "[]" + exprString(t.Elt)
	case *ast.MapType:
		return "map[" + exprString(t.Key) + "]" + exprString(t.Value)
	case *ast.IndexExpr:
		return exprString(t.X) + "[" + exprString(t.Index) + "]"
	case *ast.InterfaceType:
		return "interface{}"
	default:
		return fmt.Sprintf("%T", e)
	}
}

var registerPattern = regexp.MustCompile(`migrations\.MustRegister\(\s*"([^"]+)"`)

func (s *mcpServer) listMigrations(context.Context, map[string]any) (any, error) {
	files, err := filepath.Glob(filepath.Join(s.root, "database", "migrations", "*.go"))
	if err != nil {
		return nil, err
	}
	type migration struct {
		Name string `json:"name"`
		File string `json:"file"`
	}
	var list []migration
	for _, file := range files {
		body, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(s.root, file)
		for _, m := range registerPattern.FindAllSubmatch(body, -1) {
			list = append(list, migration{string(m[1]), filepath.ToSlash(rel)})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	return list, nil
}
