# MCP server for coding agents

`golar mcp` runs a [Model Context Protocol](https://modelcontextprotocol.io)
server on stdin and stdout. Coding agents that speak MCP, such as Claude Code,
Cursor or VS Code, use it to read a Golar project's real routes, database
schema, models and migrations instead of guessing them from the source.

```sh
golar mcp              # in the project directory
golar mcp --dir ./shop
```

## Connecting a client

Most clients take a command to start. For Claude Code, add a `.mcp.json` to
the project:

```json
{
  "mcpServers": {
    "golar": { "command": "golar", "args": ["mcp"] }
  }
}
```

or run `claude mcp add golar -- golar mcp`. Other clients use the same
command and arguments in their MCP settings.

## Tools

| Tool | Returns |
| --- | --- |
| `project_info` | `golar.yaml` settings (template, database, frontend, auth) and the **names** of the variables in `.env` |
| `list_routes` | every registered route as method and path; builds the server first |
| `describe_schema` | tables and columns (type, nullable, primary key) from the database in `DATABASE_DSN`; `table` limits it to one table |
| `list_models` | structs in `app/models` with field types, `db`, `json`, `validate` and `form` tags, and the table from a `TableName` method |
| `list_migrations` | names and files of the migrations registered in `database/migrations` |

`describe_schema` connects with the project's `.env` and reads only the
catalog (`sqlite_master`, or `information_schema` on PostgreSQL and MySQL). For
SQLite it never creates a missing database file; run `golar migrate` first.

## What it never does

Every tool is read-only and is marked with MCP's `readOnlyHint`. `golar mcp`
does not run migrations, query table rows, write files or start the server.

It does not reveal `.env` values. `project_info` lists variable names only.
Every response is also filtered: any `.env` value, and any part of a DSN or URL
in it, such as a password or host, is replaced with `[redacted]`. This covers
driver error messages too. Only harmless settings, such as `APP_NAME`,
`DB_DRIVER` and the `*_DRIVER` choices, are shown as they are.

Tools that change a project are out of scope for this release. If they are
added, they will need an explicit flag when the server starts, and they will
carry MCP's `destructiveHint` so clients can ask before running them.
