# Plan: `neo db` — Interactive TUI Database Browser

## Context

The user asked whether neo could support "plugins", with a concrete example: a Laravel-aware feature to browse a connected database from the CLI — list tables, run queries, scroll through results, view and edit records.

Neo is a compiled Go binary so true runtime plugins are impractical. The right approach is **app-aware built-in commands**: neo already knows which database is linked to each app (credentials in `state.json`), so it can auto-detect everything and open the right tool. `neo db` is the first "app-aware" feature.

The key addition over plain text output: a **full TUI mode** using `charmbracelet/bubbletea` + `charmbracelet/bubbles/table` — already in go.mod as indirect deps via huh. This gives a scrollable, keyboard-navigable table UI that lives inside the terminal.

---

## UX

```
╭───────────────────────────────────────────────────────────╮
│  neo db › app-neo-cms › mysql                             │
├───────────────────────────────────────────────────────────┤
│  Query: SELECT * FROM users LIMIT 50                   ↵  │
├───────────────────────────────────────────────────────────┤
│  id    name           email                 created_at    │
│  ──    ────           ─────                 ──────────    │
│ ► 1    Alan Tam       alan@vxero.dev         2024-01-01   │
│   2    John Doe       john@example.com       2024-01-02   │
│   ...                                                     │
├───────────────────────────────────────────────────────────┤
│  ↑↓ scroll · t tables · / new query · q quit             │
╰───────────────────────────────────────────────────────────╯
```

Keyboard map:
| Key | Action |
|---|---|
| `↑ / ↓` | Scroll rows |
| `t` | Show all tables (runs `SHOW TABLES` / pg equivalent) |
| `d` | Describe selected table (if in table list) |
| `/` | Focus query input to type new SQL |
| `Enter` (in query input) | Run query, refresh table |
| `Esc` | Cancel query input, back to table |
| `q` | Quit |

---

## CLI Entry Points

| Command | Behaviour |
|---|---|
| `neo db <app>` | Opens TUI browser (starts with `SHOW TABLES`) |
| `neo db <app> shell` | Opens raw interactive mysql/psql shell (PTY via system SSH) |

The default mode (no subcommand) is the TUI browser. `shell` is the escape hatch for direct CLI access.

---

## Critical Files

| File | Change |
|---|---|
| `commands/db.go` | **Create** — command definition + TUI model + DB detection |
| `commands/root.go` | Add `newDbCmd()` to `AddCommand` block |
| `commands/dashboard.go` | Add "Browse DB" app action → calls `runDbTUI` |

---

## DB Detection (`resolveAppDB`)

Neo stores credentials in three places — check in order:

**1. Shared service links** (most common)
```
st.Services[svcName].LinkedApps[appName] → Link.User, Link.Database, Link.EnvVars["DB_PASSWORD"]
container = config.SvcContainerShared(svcName)   // "svc-mysql"
```

**2. Bundled services** (legacy)
```
app.Services[svcName] → image tells type
container = config.SvcContainer(appName, svcName) // "svc-app-mysql"
credentials from app.Env: DB_USERNAME, DB_PASSWORD, DB_DATABASE
```

**3. Raw env vars fallback**
```
app.Env["DATABASE_URL"] or app.Env["DB_HOST"] / DB_CONNECTION / DB_PORT
```

Reuse `detectServiceType(image string)` from `commands/service.go` to identify mysql/mariadb/postgres from image name.

```go
type dbConn struct {
    Type      string // "mysql", "mariadb", "postgres"
    Container string // docker exec target
    Host      string // --host / -h for the CLI client
    User      string
    Password  string
    Database  string
}
```

---

## TUI Implementation (bubbletea model)

`charmbracelet/bubbletea v1.3.6` and `charmbracelet/bubbles v0.21.1` are already in go.mod (indirect). Just add direct imports; `go mod tidy` will promote them.

### Model states

```go
type viewState int
const (
    stateLoading viewState = iota  // spinner while query runs
    stateTable                     // results visible, table focused
    stateQuerying                  // query input focused
)

type model struct {
    state     viewState
    db        dbConn
    srv       *config.Server
    sshExec   *ssh.Executor
    table     table.Model          // bubbles/table
    input     textinput.Model      // bubbles/textinput
    headers   []string
    rows      []table.Row
    lastQuery string
    err       error
    width, height int
}
```

### Async query execution

Use `tea.Cmd` to run the SSH query in a goroutine so the TUI stays responsive:

```go
type queryResultMsg struct {
    headers []string
    rows    []table.Row
    err     error
}

func (m model) runQuery(sql string) tea.Cmd {
    return func() tea.Msg {
        headers, rows, err := execQuery(m.sshExec, m.db, sql)
        return queryResultMsg{headers, rows, err}
    }
}
```

### Query execution (remote, non-interactive)

Run via `sshExec.Run(...)` — no PTY needed for query output.

**MySQL:**
```bash
docker exec svc-mysql mysql -u USER -pPASS DATABASE \
  --batch --silent --skip-column-names -e "SQL"
```
First call gets column names: `--column-names` instead of `--skip-column-names`.
Parse tab-separated output into `[]table.Row`.

**PostgreSQL:**
```bash
docker exec svc-postgres env PGPASSWORD=PASS \
  psql -U USER -d DATABASE -t -A -F$'\t' -c "SQL"
```
Get headers via `\d` or a separate `SELECT column_name FROM information_schema.columns WHERE table_name='...' ORDER BY ordinal_position`.

### Default query on open

```go
"SHOW TABLES"                                    // MySQL/MariaDB
"SELECT tablename FROM pg_stat_user_tables ..."  // PostgreSQL
```

### `t` shortcut

Re-runs the "show tables" query and replaces table contents.

### `d` shortcut (on a table row in tables view)

Runs:
- MySQL: `SHOW COLUMNS FROM <selected_table>`
- Postgres: query `information_schema.columns WHERE table_name='...'`

---

## Interactive Shell (`neo db <app> shell`)

Reuse `runExecInteractive` pattern from `commands/run.go` + `buildSSHArgs` from `commands/dashboard.go`:

```go
// MySQL
"docker exec -it svc-mysql mysql -uUSER -pPASS DATABASE"

// PostgreSQL
"docker exec -it svc-postgres env PGPASSWORD=PASS psql -U USER -d DATABASE"
```

---

## Dashboard Integration

In `tuiAppActions` (`commands/dashboard.go`):
1. After loading state, call `resolveAppDB(st, appName)`
2. If it returns a `*dbConn`, append `"Browse DB"` to the actions list
3. On select: call `runDbTUI(srv, db)` — same entry as `neo db <app>`

---

## Command Registration

`commands/root.go` — one line added to `AddCommand` block:
```go
newDbCmd(),
```

---

## Verification

```bash
make build

./bin/neo db <app>           # TUI opens, shows tables list, ↑↓ scroll works
./bin/neo db <app> shell     # raw mysql/psql shell opens via SSH PTY

# From dashboard: select an app with linked DB → "Browse DB" appears
```

Test queries to verify in TUI:
- `SHOW TABLES` / tables view on open
- Type `/` → enter `SELECT * FROM users LIMIT 10` → Enter → table populates
- Press `t` → returns to tables list
- Press `d` on a table row → column structure shown
