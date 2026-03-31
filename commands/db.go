package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	neossh "github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
)

// ─── types ────────────────────────────────────────────────────────────────────

// dbConn holds the resolved database connection details for an app.
type dbConn struct {
	Type      string // "mysql", "mariadb", "postgres"
	Container string // docker exec target, e.g. "svc-mysql"
	User      string
	Password  string
	Database  string
}

// ─── command ──────────────────────────────────────────────────────────────────

func newDbCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "db <app> [shell]",
		Short: "Browse app database in an interactive TUI",
		Long: `Opens an interactive TUI database browser for the app's linked database.

Pass 'shell' as a second argument to open a raw mysql/psql shell instead:
  neo db myapp shell`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 2 && args[1] == "shell" {
				return runDbShell(args[0])
			}
			return runDbTUI(args[0])
		},
	}
}

// ─── entry points ─────────────────────────────────────────────────────────────

// runServiceDBTUI opens the DB browser for a shared service directly (no app needed).
func runServiceDBTUI(svcName string) error {
	_, _, sshExec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer sshExec.Close()

	st, err := state.Load(sshExec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	db, err := resolveServiceDB(st, svcName)
	if err != nil {
		return err
	}

	p := tea.NewProgram(newDbBrowser(db, sshExec), tea.WithAltScreen())
	_, runErr := p.Run()
	return runErr
}

// resolveServiceDB builds a dbConn for a shared service using its root credentials.
func resolveServiceDB(st *state.State, svcName string) (*dbConn, error) {
	svc, ok := st.Services[svcName]
	if !ok {
		return nil, fmt.Errorf("service %q not found", svcName)
	}
	svcType := detectServiceType(svc.Image)
	container := config.SvcContainerShared(svcName)

	switch svcType {
	case "mysql", "mariadb":
		rootPass := svc.Env["MYSQL_ROOT_PASSWORD"]
		if rootPass == "" {
			rootPass = svc.Env["MARIADB_ROOT_PASSWORD"]
		}
		// Prefer app user if available (has host=% and access to the default DB)
		user, pass := "root", rootPass
		if appUser := svc.Env["MYSQL_USER"]; appUser != "" {
			user = appUser
			pass = svc.Env["MYSQL_PASSWORD"]
		} else if appUser := svc.Env["MARIADB_USER"]; appUser != "" {
			user = appUser
			pass = svc.Env["MARIADB_PASSWORD"]
		}
		db := svc.DefaultDB
		if db == "" {
			db = "information_schema"
		}
		return &dbConn{
			Type:      svcType,
			Container: container,
			User:      user,
			Password:  pass,
			Database:  db,
		}, nil
	case "postgres":
		db := svc.DefaultDB
		if db == "" {
			db = "postgres"
		}
		return &dbConn{
			Type:      "postgres",
			Container: container,
			User:      "postgres",
			Password:  svc.Env["POSTGRES_PASSWORD"],
			Database:  db,
		}, nil
	default:
		return nil, fmt.Errorf("service %q (%s) is not a browsable database", svcName, svc.Image)
	}
}

func runDbTUI(appName string) error {
	_, _, sshExec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer sshExec.Close()

	st, err := state.Load(sshExec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	db, err := resolveAppDB(st, appName)
	if err != nil {
		return err
	}
	if db.Container == "" {
		return fmt.Errorf("database container not found — set DB_HOST/DB_USERNAME/DB_PASSWORD/DB_DATABASE env vars on the app")
	}

	p := tea.NewProgram(newDbBrowser(db, sshExec), tea.WithAltScreen())
	_, runErr := p.Run()
	return runErr
}

func runDbShell(appName string) error {
	_, srv, sshExec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer sshExec.Close()

	st, err := state.Load(sshExec)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	db, err := resolveAppDB(st, appName)
	if err != nil {
		return err
	}
	if db.Container == "" {
		return fmt.Errorf("no database container found")
	}

	var remoteCmd string
	switch db.Type {
	case "mysql", "mariadb":
		remoteCmd = fmt.Sprintf("docker exec -it %s mysql --user=%s --password=%s --database=%s",
			neossh.ShellQuote(db.Container),
			neossh.ShellQuote(db.User),
			neossh.ShellQuote(db.Password),
			neossh.ShellQuote(db.Database),
		)
	case "postgres":
		remoteCmd = fmt.Sprintf("docker exec -it -e PGPASSWORD=%s %s psql -U %s -d %s",
			neossh.ShellQuote(db.Password),
			neossh.ShellQuote(db.Container),
			neossh.ShellQuote(db.User),
			neossh.ShellQuote(db.Database),
		)
	default:
		return fmt.Errorf("unsupported database type: %s", db.Type)
	}

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found: %w", err)
	}

	sshArgs := buildSSHArgs(srv)
	sshArgs = append(sshArgs, "-t", srv.Host, remoteCmd)

	c := exec.Command(sshPath, sshArgs...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// ─── DB detection ─────────────────────────────────────────────────────────────

// resolveAppDB finds the database connection details for an app from neo state.
// Checks shared service links, bundled services, and env var fallback in that order.
func resolveAppDB(st *state.State, appName string) (*dbConn, error) {
	app, ok := st.Apps[appName]
	if !ok {
		return nil, fmt.Errorf("app %q not found", appName)
	}

	// 1. Bundled services (e.g. ghost-mysql installed via template)
	for svcName, svc := range app.Services {
		dbType := detectServiceType(svc.Image)
		if dbType == "mysql" || dbType == "mariadb" || dbType == "postgres" {
			return &dbConn{
				Type:      dbType,
				Container: config.SvcContainer(appName, svcName),
				User:      app.Env["DB_USERNAME"],
				Password:  app.Env["DB_PASSWORD"],
				Database:  app.Env["DB_DATABASE"],
			}, nil
		}
	}

	// 3. Env var fallback (external or hand-configured DB — no container available)
	if host := app.Env["DB_HOST"]; host != "" {
		dbType := "mysql"
		conn := app.Env["DB_CONNECTION"]
		port := app.Env["DB_PORT"]
		if conn == "pgsql" || conn == "postgres" || port == "5432" {
			dbType = "postgres"
		}
		return &dbConn{
			Type:     dbType,
			User:     app.Env["DB_USERNAME"],
			Password: app.Env["DB_PASSWORD"],
			Database: app.Env["DB_DATABASE"],
			// Container intentionally empty: TUI not available for external DBs
		}, nil
	}

	return nil, fmt.Errorf("no database found for app %q — set DB_HOST/DB_USERNAME/DB_PASSWORD/DB_DATABASE env vars", appName)
}

// ─── SQL helpers ──────────────────────────────────────────────────────────────

func dbTablesQuery(dbType string) string {
	if dbType == "postgres" {
		return "SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY table_name"
	}
	return "SHOW TABLES"
}

func dbDescribeQuery(dbType, tableName string) string {
	if dbType == "postgres" {
		return fmt.Sprintf(
			"SELECT column_name, data_type, character_maximum_length AS max_len, is_nullable FROM information_schema.columns WHERE table_name = '%s' ORDER BY ordinal_position",
			tableName,
		)
	}
	return "SHOW COLUMNS FROM " + tableName
}

// execRemoteQuery runs SQL via docker exec on the DB container and returns headers + rows.
func execRemoteQuery(sshExec *neossh.Executor, db *dbConn, sql string) ([]string, []table.Row, error) {
	var cmd string
	switch db.Type {
	case "mysql", "mariadb":
		// --batch: tab-separated, first row = column names, no decoration
		cmd = fmt.Sprintf(
			"docker exec %s mysql --user=%s --password=%s --database=%s --batch -e %s 2>&1",
			neossh.ShellQuote(db.Container),
			neossh.ShellQuote(db.User),
			neossh.ShellQuote(db.Password),
			neossh.ShellQuote(db.Database),
			neossh.ShellQuote(sql),
		)
	case "postgres":
		// --csv: CSV with header row
		cmd = fmt.Sprintf(
			"docker exec -e PGPASSWORD=%s %s psql -U %s -d %s --csv -c %s 2>&1",
			neossh.ShellQuote(db.Password),
			neossh.ShellQuote(db.Container),
			neossh.ShellQuote(db.User),
			neossh.ShellQuote(db.Database),
			neossh.ShellQuote(sql),
		)
	default:
		return nil, nil, fmt.Errorf("unsupported db type: %s", db.Type)
	}

	out, err := sshExec.Run(cmd)
	out = strings.TrimSpace(out)

	// Surface human-readable error from command output
	if err != nil {
		if out != "" {
			return nil, nil, fmt.Errorf("%s", sanitizeDBError(out))
		}
		return nil, nil, err
	}
	if out == "" {
		return nil, nil, nil
	}

	return parseDBOutput(db.Type, out)
}

func sanitizeDBError(out string) string {
	// Strip MySQL warning noise, keep first real error line
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "mysql: [Warning]") {
			continue
		}
		return line
	}
	return out
}

// ─── output parsers ───────────────────────────────────────────────────────────

func parseDBOutput(dbType, out string) ([]string, []table.Row, error) {
	if dbType == "postgres" {
		return parseCSVOutput(out)
	}
	return parseTSVOutput(out)
}

// parseTSVOutput parses MySQL --batch tab-separated output (first line = headers).
func parseTSVOutput(out string) ([]string, []table.Row, error) {
	var dataLines []string
	for _, l := range strings.Split(out, "\n") {
		if !strings.HasPrefix(l, "mysql: [Warning]") {
			dataLines = append(dataLines, l)
		}
	}
	if len(dataLines) == 0 {
		return nil, nil, nil
	}

	// Detect errors
	if strings.HasPrefix(dataLines[0], "ERROR ") {
		return nil, nil, fmt.Errorf("%s", dataLines[0])
	}

	headers := strings.Split(dataLines[0], "\t")
	var rows []table.Row
	for _, line := range dataLines[1:] {
		if line == "" {
			continue
		}
		cells := strings.Split(line, "\t")
		for len(cells) < len(headers) {
			cells = append(cells, "")
		}
		rows = append(rows, table.Row(cells[:len(headers)]))
	}
	return headers, rows, nil
}

// parseCSVOutput parses PostgreSQL --csv output (first line = headers).
func parseCSVOutput(out string) ([]string, []table.Row, error) {
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		return nil, nil, nil
	}

	var headers []string
	var rows []table.Row
	for i, line := range lines {
		if line == "" {
			continue
		}
		cells := splitCSV(line)
		if i == 0 {
			headers = cells
		} else {
			for len(cells) < len(headers) {
				cells = append(cells, "")
			}
			rows = append(rows, table.Row(cells[:len(headers)]))
		}
	}
	return headers, rows, nil
}

// splitCSV splits one CSV line handling double-quoted fields with escaped quotes.
func splitCSV(line string) []string {
	var fields []string
	var cur strings.Builder
	inQ := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '"' && !inQ:
			inQ = true
		case ch == '"' && inQ && i+1 < len(line) && line[i+1] == '"':
			cur.WriteByte('"')
			i++
		case ch == '"' && inQ:
			inQ = false
		case ch == ',' && !inQ:
			fields = append(fields, cur.String())
			cur.Reset()
		default:
			cur.WriteByte(ch)
		}
	}
	fields = append(fields, cur.String())
	return fields
}

// ─── column sizing ────────────────────────────────────────────────────────────

const (
	dbColMin = 4
	dbColMax = 45
)

func buildColumns(headers []string, rows []table.Row, termWidth int) []table.Column {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	for i, w := range widths {
		switch {
		case w > dbColMax:
			widths[i] = dbColMax
		case w < dbColMin:
			widths[i] = dbColMin
		}
	}

	// Shrink proportionally if wider than terminal
	available := termWidth - 6
	total := 0
	for _, w := range widths {
		total += w + 2
	}
	if total > available && total > 0 {
		ratio := float64(available) / float64(total)
		for i, w := range widths {
			widths[i] = int(float64(w) * ratio)
			if widths[i] < dbColMin {
				widths[i] = dbColMin
			}
		}
	}

	cols := make([]table.Column, len(headers))
	for i, h := range headers {
		cols[i] = table.Column{Title: h, Width: widths[i]}
	}
	return cols
}

func truncateCell(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func applyColumnWidths(rows []table.Row, cols []table.Column) []table.Row {
	out := make([]table.Row, len(rows))
	for i, row := range rows {
		cells := make([]string, len(row))
		for j, cell := range row {
			w := dbColMax
			if j < len(cols) {
				w = cols[j].Width
			}
			cells[j] = truncateCell(cell, w)
		}
		out[i] = table.Row(cells)
	}
	return out
}

// ─── bubbletea model ──────────────────────────────────────────────────────────

type dbViewState int

const (
	dbStateLoading  dbViewState = iota
	dbStateTable
	dbStateQuerying
)

type dbQueryDoneMsg struct {
	headers []string
	rows    []table.Row
	err     error
}

type dbBrowserModel struct {
	db          *dbConn
	sshExec     *neossh.Executor
	state       dbViewState
	tbl         table.Model
	input       textinput.Model
	spin        spinner.Model
	lastQuery   string
	isTableView bool   // true when showing table list (enables 'd' to describe)
	statusLine  string // shown in query bar when not editing
	errMsg      string
	width       int
	height      int
	ready       bool
}

// Lipgloss styles used in the TUI.
var (
	dbStyleTitle    = lipgloss.NewStyle().Bold(true)
	dbStyleFaint    = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	dbStyleErr      = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
	dbStyleDivider  = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	dbStyleSelected = lipgloss.NewStyle().Foreground(lipgloss.Color("229")).Background(lipgloss.Color("57"))
	dbStyleHeader   = lipgloss.NewStyle().Bold(true).BorderStyle(lipgloss.NormalBorder()).BorderForeground(lipgloss.Color("240")).BorderBottom(true)
)

func newDbBrowser(db *dbConn, sshExec *neossh.Executor) dbBrowserModel {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	ti := textinput.New()
	ti.Placeholder = "SELECT * FROM ..."
	ti.CharLimit = 1000
	ti.Prompt = "⟩ "

	tbl := table.New(table.WithFocused(true), table.WithHeight(20))
	s := table.DefaultStyles()
	s.Header = dbStyleHeader
	s.Selected = dbStyleSelected
	tbl.SetStyles(s)

	initSQL := dbTablesQuery(db.Type)
	return dbBrowserModel{
		db:          db,
		sshExec:     sshExec,
		state:       dbStateLoading,
		tbl:         tbl,
		input:       ti,
		spin:        sp,
		lastQuery:   initSQL,
		isTableView: true,
		statusLine:  "tables",
	}
}

func (m dbBrowserModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.doQuery(m.lastQuery))
}

func (m dbBrowserModel) doQuery(sql string) tea.Cmd {
	return func() tea.Msg {
		h, r, err := execRemoteQuery(m.sshExec, m.db, sql)
		return dbQueryDoneMsg{headers: h, rows: r, err: err}
	}
}

func (m dbBrowserModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.ready = true
		tableH := m.height - 9
		if tableH < 3 {
			tableH = 3
		}
		m.tbl.SetHeight(tableH)
		return m, nil

	case dbQueryDoneMsg:
		if msg.err != nil {
			m.errMsg = msg.err.Error()
		} else {
			m.errMsg = ""
			if len(msg.headers) > 0 {
				cols := buildColumns(msg.headers, msg.rows, m.width)
				m.tbl.SetColumns(cols)
				m.tbl.SetRows(applyColumnWidths(msg.rows, cols))
			} else {
				m.tbl.SetColumns([]table.Column{{Title: "result", Width: 20}})
				m.tbl.SetRows([]table.Row{{"(no rows)"}})
			}
		}
		m.state = dbStateTable
		return m, nil

	case spinner.TickMsg:
		if m.state == dbStateLoading {
			var cmd tea.Cmd
			m.spin, cmd = m.spin.Update(msg)
			return m, cmd
		}

	case tea.KeyMsg:
		switch m.state {
		case dbStateQuerying:
			return m.updateQuerying(msg)
		case dbStateTable:
			return m.updateTable(msg)
		}
	}

	return m, nil
}

func (m dbBrowserModel) updateTable(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit

	case "/":
		m.state = dbStateQuerying
		m.input.SetValue("")
		return m, m.input.Focus()

	case "t":
		sql := dbTablesQuery(m.db.Type)
		m.lastQuery = sql
		m.isTableView = true
		m.statusLine = "tables"
		m.state = dbStateLoading
		return m, tea.Batch(m.spin.Tick, m.doQuery(sql))

	case "d", "enter":
		if m.isTableView {
			row := m.tbl.SelectedRow()
			if len(row) > 0 {
				tableName := row[0]
				sql := dbDescribeQuery(m.db.Type, tableName)
				m.lastQuery = sql
				m.isTableView = false
				m.statusLine = "DESCRIBE " + tableName
				m.state = dbStateLoading
				return m, tea.Batch(m.spin.Tick, m.doQuery(sql))
			}
		}
	}

	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	return m, cmd
}

func (m dbBrowserModel) updateQuerying(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.state = dbStateTable
		m.input.Blur()
		return m, nil

	case tea.KeyEnter:
		sql := strings.TrimSpace(m.input.Value())
		if sql == "" {
			m.state = dbStateTable
			m.input.Blur()
			return m, nil
		}
		m.lastQuery = sql
		m.isTableView = false
		label := sql
		if len(label) > 60 {
			label = label[:57] + "..."
		}
		m.statusLine = label
		m.state = dbStateLoading
		m.input.Blur()
		return m, tea.Batch(m.spin.Tick, m.doQuery(sql))
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m dbBrowserModel) View() string {
	if !m.ready {
		return "\n  Connecting to database...\n"
	}

	w := m.width
	if w < 40 {
		w = 80
	}
	div := dbStyleDivider.Render(strings.Repeat("─", w-4))

	var sb strings.Builder

	// Title bar
	title := fmt.Sprintf("  neo db  ›  %s  ›  %s  (%s)", m.db.Container, m.db.Type, m.db.Database)
	sb.WriteString(dbStyleTitle.Render(title))
	sb.WriteString("\n  " + div + "\n")

	// Query / input line
	switch m.state {
	case dbStateQuerying:
		sb.WriteString("  " + m.input.View() + "\n")
	default:
		label := m.statusLine
		if len(label) > w-14 {
			label = label[:w-17] + "..."
		}
		sb.WriteString("  " + dbStyleFaint.Render("⟩ "+label) + "\n")
	}
	sb.WriteString("  " + div + "\n")

	// Table area
	switch m.state {
	case dbStateLoading:
		sb.WriteString("\n  " + m.spin.View() + "  Running query...\n")
	default:
		if m.errMsg != "" {
			sb.WriteString("\n  " + dbStyleErr.Render("✗  "+m.errMsg) + "\n")
		} else {
			sb.WriteString(m.tbl.View() + "\n")
		}
	}

	// Footer
	sb.WriteString("\n")
	if m.state == dbStateQuerying {
		sb.WriteString("  " + dbStyleFaint.Render("enter: run query  ·  esc: cancel"))
	} else {
		hint := "↑↓ scroll  ·  t tables  ·  d describe  ·  / query  ·  q quit"
		sb.WriteString("  " + dbStyleFaint.Render(hint))
	}

	return sb.String()
}
