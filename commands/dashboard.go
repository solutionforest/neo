package commands

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/license"
	"github.com/vxero/neo/internal/remote"
	neossh "github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

// runDashboard is the interactive TUI entry point for `neo` with no arguments.
func runDashboard(cmd *cobra.Command, args []string) error {
	ui.SetVersion(cliVersion)
	defer ui.ShowCursor() // restore cursor on exit (Ctrl+C handled in ReadKey)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// No servers? Print quick-start instructions instead of launching a blank TUI
	if len(cfg.Servers) == 0 {
		ui.PrintBanner(cliVersion)
		fmt.Println("  No servers configured yet.")
		fmt.Println()
		fmt.Printf("  To get started, run:\n")
		fmt.Printf("    %s\n", ui.Bold.Render("neo init root@<your-server-ip>"))
		fmt.Println()
		fmt.Printf("  Example:\n")
		fmt.Printf("    %s\n", ui.Faint.Render("neo init root@159.65.100.42"))
		fmt.Println()
		status := license.Check(cfg.LicenseKey)
		if !status.Valid && !status.Expired {
			ui.PrintUpgradeHint()
		}
		return nil
	}

	// Parallel background refresh with concurrency limit (max 10 simultaneous SSH connections).
	// Goroutines are non-blocking — menu renders from in-memory cache instantly.
	go func() {
		sem := make(chan struct{}, 10) // limit to 10 concurrent SSH connections
		var wg sync.WaitGroup
		for name, srv := range cfg.Servers {
			wg.Add(1)
			go func(n string, s config.Server) {
				defer wg.Done()
				sem <- struct{}{}        // acquire slot
				defer func() { <-sem }() // release slot
				refreshServerCache(n, &s)
			}(name, srv)
		}
		wg.Wait()
	}()

	// Main interactive loop — re-reads config + cache before each render
	for {
		// Reload config each iteration so new servers/changes from submenus are visible
		if freshCfg, loadErr := config.Load(); loadErr == nil {
			cfg = freshCfg
		}
		appSummary, serviceSummary := cachedDashboardSummaries(cfg)
		action := tuiMainMenu(cfg, appSummary, serviceSummary)
		if action == "quit" {
			return nil
		}
		if action == "" {
			continue // Enter=refresh: re-render menu with latest cache
		}

		switch action {
		case "servers":
			if err := tuiServersMenu(cfg); err != nil {
				return err
			}
		case "apps":
			if err := tuiAppsMenu(cfg); err != nil {
				return err
			}
		case "services":
			if err := tuiServicesMenu(cfg); err != nil {
				return err
			}
		case "metrics":
			if err := tuiLiveMetrics(cfg); err != nil {
				ui.Error(err.Error())
			}
		case "deploy":
			if err := tuiDeployProject(); err != nil {
				ui.Error(err.Error())
			}
		case "connect":
			if err := runConnect(); err != nil {
				ui.Error(err.Error())
			}
		case "plus":
			if err := runPlus(); err != nil {
				ui.Error(err.Error())
			}
		}

		// Refresh cache after every action so the menu shows updated counts
		refreshCurrentServer(cfg)
	}
}

// tuiDeployProject prompts for project directory, environment, and server, then deploys.
func tuiDeployProject() error {
	cwd, _ := os.Getwd()
	projectPath := cwd

	huh.NewInput().
		Title("Project directory to deploy").
		Description("Press Enter to use current directory, or type a path").
		Placeholder(cwd).
		Value(&projectPath).
		Run() //nolint:errcheck

	if projectPath == "" {
		projectPath = cwd
	}

	// Load .neo.yml to check for environments with server groups.
	neoConfig, _ := loadNeoConfig(projectPath)

	flags := deployFlags{}

	if neoConfig != nil && len(neoConfig.Environments) > 0 {
		// Let user pick an environment (or deploy all).
		opts := []ui.SelectOption{{"All environments", "__all__"}}
		for k := range neoConfig.Environments {
			opts = append(opts, ui.SelectOption{k, k})
		}
		envChoice := ui.Select("Deploy which environment?", opts)
		if envChoice == "" {
			return nil
		}

		if envChoice == "__all__" {
			flags.all = true
			return runDeploy(projectPath, flags)
		}

		flags.target = envChoice
		envCfg := neoConfig.Environments[envChoice]
		servers := envCfg.EffectiveServers()

		if len(servers) > 1 {
			// Server group — let user pick one or all.
			opts := []ui.SelectOption{{"All servers in group", "__all__"}}
			for _, s := range servers {
				opts = append(opts, ui.SelectOption{s, s})
			}
			srvChoice := ui.Select(fmt.Sprintf("Deploy %s to which server?", envChoice), opts)
			if srvChoice == "" {
				return nil
			}
			if srvChoice != "__all__" {
				// Single server within the group — override serverFlag for this deploy only.
				oldServer := serverFlag
				serverFlag = srvChoice
				defer func() { serverFlag = oldServer }()
			}
			// __all__ → serverFlag stays empty, runDeploy will use the full servers: list via runDeployAll
		} else if len(servers) == 1 {
			// Single server env — set serverFlag as before.
			oldServer := serverFlag
			serverFlag = servers[0]
			defer func() { serverFlag = oldServer }()
		}
	} else {
		// No environments — fall back to server picker (original behaviour).
		cfg, err := config.Load()
		if err != nil {
			return err
		}
		if len(cfg.Servers) > 1 {
			srvOpts := make([]ui.SelectOption, 0, len(cfg.Servers))
			for _, srv := range cfg.Servers {
				active := ""
				if srv.Name == cfg.Current {
					active = "  " + ui.Faint.Render("(active)")
				}
				label := fmt.Sprintf("%-18s%s%s", srv.Name, ui.Faint.Render(srv.Host), active)
				srvOpts = append(srvOpts, ui.SelectOption{label, srv.Name})
			}
			target := ui.Select("Deploy to which server?", srvOpts)
			if target == "" {
				return nil
			}
			oldServer := serverFlag
			serverFlag = target
			defer func() { serverFlag = oldServer }()
		}
	}

	return runDeploy(projectPath, flags)
}

// tuiDeployApp is called from the app actions menu. It handles server-group selection
// when the project's .neo.yml defines a multi-server environment, then runs deploy.
func tuiDeployApp(appName string, envOnly bool) error {
	neoConfig, _ := loadNeoConfig(".")

	flags := deployFlags{appName: appName, envOnly: envOnly}

	if neoConfig != nil && len(neoConfig.Environments) > 0 {
		// Prompt for environment.
		opts := make([]ui.SelectOption, 0, len(neoConfig.Environments))
		for k := range neoConfig.Environments {
			opts = append(opts, ui.SelectOption{k, k})
		}
		var envChoice string
		if len(opts) == 1 {
			envChoice = opts[0].Value
		} else {
			envChoice = ui.Select("Deploy which environment?", opts)
			if envChoice == "" {
				return nil
			}
		}
		flags.target = envChoice

		envCfg := neoConfig.Environments[envChoice]
		servers := envCfg.EffectiveServers()

		if len(servers) > 1 {
			// Server group — offer single-server filter or all.
			srvOpts := []ui.SelectOption{{"All servers in group", "__all__"}}
			for _, s := range servers {
				srvOpts = append(srvOpts, ui.SelectOption{s, s})
			}
			srvChoice := ui.Select(fmt.Sprintf("Deploy %s to which server?", envChoice), srvOpts)
			if srvChoice == "" {
				return nil
			}
			if srvChoice != "__all__" {
				oldServer := serverFlag
				serverFlag = srvChoice
				defer func() { serverFlag = oldServer }()
			}
		} else if len(servers) == 1 {
			oldServer := serverFlag
			serverFlag = servers[0]
			defer func() { serverFlag = oldServer }()
		}
	}

	return runDeploy(".", flags)
}

// cachedDashboardSummaries returns app and service summary strings from the local cache
// for the current server. Shows "connecting...", "unreachable", or actual counts.
func cachedDashboardSummaries(cfg *config.Config) (string, string) {
	srv, err := cfg.CurrentServer()
	if err != nil || srv == nil {
		return ui.Faint.Render("—"), ui.Faint.Render("—")
	}
	c := config.LoadCache()
	if c == nil {
		return ui.Faint.Render("connecting..."), ui.Faint.Render("connecting...")
	}
	sc := c.Get(srv.Name)
	if sc == nil {
		return ui.Faint.Render("connecting..."), ui.Faint.Render("connecting...")
	}
	if !sc.Reachable {
		age := formatCacheAge(time.Since(sc.UpdatedAt))
		return ui.Red.Render("unreachable · " + age), ui.Red.Render("unreachable")
	}
	age := formatCacheAge(time.Since(sc.UpdatedAt))
	app := ui.Faint.Render(fmt.Sprintf("%d apps, %d running · %s", sc.AppCount, sc.RunningApps, age))
	svc := ui.Faint.Render(fmt.Sprintf("%d services, %d running · %s", sc.ServiceCount, sc.RunningServices, age))
	return app, svc
}

// plusSummary returns a short status string for the Neo+ menu item.
func plusSummary(cfg *config.Config) string {
	status := license.Check(cfg.LicenseKey)
	switch {
	case status.Valid && status.Plan == license.PlanPlus:
		return ui.Green.Render("Plus · unlimited servers")
	case status.Expired:
		return ui.Yellow.Render("Plus · expired — renew at neo.vxero.dev")
	default:
		return ui.Yellow.Render("★ Upgrade to Neo+") + ui.Faint.Render(" · neo.vxero.dev")
	}
}

// formatCacheAge returns a human-readable duration string for cache age display.
func formatCacheAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// refreshServerCache connects to one server, reads its state, and updates the local cache entry.
// On SSH failure, caches the server as unreachable so the dashboard shows it immediately.
func refreshServerCache(serverName string, srv *config.Server) {
	exec, err := connectSSHNonInteractive(srv)
	if err != nil {
		config.UpdateServerCache(serverName, config.ServerCache{
			Reachable: false,
			Error:     err.Error(),
			UpdatedAt: time.Now(),
		})
		return
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		config.UpdateServerCache(serverName, config.ServerCache{
			Reachable: false,
			Error:     err.Error(),
			UpdatedAt: time.Now(),
		})
		return
	}

	runningApps, runningSvcs := 0, 0
	for _, a := range st.Apps {
		if a.Status == "running" {
			runningApps++
		}
	}
	for _, s := range st.Services {
		if s.Status == "running" {
			runningSvcs++
		}
	}

	config.UpdateServerCache(serverName, config.ServerCache{
		AppCount:        len(st.Apps),
		RunningApps:     runningApps,
		ServiceCount:    len(st.Services),
		RunningServices: runningSvcs,
		Reachable:       true,
		UpdatedAt:       time.Now(),
	})
}

// refreshCurrentServer synchronously refreshes the cache for the active server.
// Called after user actions (deploy, app management, etc.) to keep counts fresh.
func refreshCurrentServer(cfg *config.Config) {
	srv, err := cfg.CurrentServer()
	if err != nil || srv == nil {
		return
	}
	refreshServerCache(srv.Name, srv)
}

// tuiMainMenu shows the top-level interactive menu with arrow-key navigation.
func tuiMainMenu(cfg *config.Config, appSummary, serviceSummary string) string {
	srv, _ := cfg.CurrentServer()
	var title string
	if srv != nil {
		c := config.LoadCache()
		if c != nil {
			if sc := c.Get(srv.Name); sc != nil && !sc.Reachable {
				reason := "unreachable"
				if sc.Error != "" {
					// Show a short reason (e.g., "auth failed" instead of full SSH error)
					if strings.Contains(sc.Error, "unable to authenticate") {
						reason = "auth failed — run: neo ssh"
					} else if strings.Contains(sc.Error, "connection refused") {
						reason = "connection refused"
					} else if strings.Contains(sc.Error, "timed out") || strings.Contains(sc.Error, "timeout") {
						reason = "timed out"
					}
				}
				title = fmt.Sprintf("  %s %s  %s  %s",
					ui.Red.Render("●"), srv.Name, ui.Faint.Render(srv.Host), ui.Red.Render(reason))
			} else {
				title = fmt.Sprintf("  %s %s  %s",
					ui.Green.Render("●"), srv.Name, ui.Faint.Render(srv.Host))
			}
		} else {
			title = fmt.Sprintf("  %s %s  %s",
				ui.Faint.Render("●"), srv.Name, ui.Faint.Render(srv.Host))
		}
	} else {
		title = "  " + ui.Faint.Render("No server selected")
	}

	opts := []ui.SelectOption{
		{fmt.Sprintf("%-22s%s", "Servers", ui.Faint.Render(fmt.Sprintf("%d configured", len(cfg.Servers)))), "servers"},
		{fmt.Sprintf("%-22s%s", "Applications", appSummary), "apps"},
		{fmt.Sprintf("%-22s%s", "Services", serviceSummary), "services"},
		{fmt.Sprintf("%-22s%s", "Live Metrics", ui.Faint.Render("CPU, RAM, containers")), "metrics"},
		{"Deploy Project", "deploy"},
		{fmt.Sprintf("%-22s%s", "Neo+", plusSummary(cfg)), "plus"},
	}

	action := ui.Select(title, opts)
	if action == "" {
		return "quit" // q/Esc at top level = quit
	}
	return action
}

// tuiServersMenu shows the servers submenu.
func tuiServersMenu(cfg *config.Config) error {
	for {
		var sb strings.Builder
		sb.WriteString("  " + ui.Bold.Render("Servers") + "\n")
		sb.WriteString("  " + ui.Faint.Render("─────────────────────────────────────────────────") + "\n")
		for _, srv := range cfg.Servers {
			marker := "  "
			suffix := ""
			if srv.Name == cfg.Current {
				marker = ui.Green.Render("● ")
				suffix = ui.Faint.Render("  (active)")
			}
			sb.WriteString(fmt.Sprintf("  %s%-15s %s%s\n", marker, srv.Name, srv.Host, suffix))
		}

		opts := []ui.SelectOption{
			{"Add New Server", "add"},
		}
		if len(cfg.Servers) > 1 {
			opts = append(opts, ui.SelectOption{"Switch Server", "switch"})
		}
		if len(cfg.Servers) > 0 {
			opts = append(opts, ui.SelectOption{"SSH into Server", "ssh"})
			opts = append(opts, ui.SelectOption{ui.Red.Render("Remove Server"), "remove"})
		}
		opts = append(opts, ui.SelectOption{"Back", "back"})

		action := ui.Select(sb.String(), opts)

		switch action {
		case "add":
			if err := tuiAddServer(cfg); err != nil {
				return err
			}
		case "switch":
			if err := tuiSwitchServer(cfg); err != nil {
				return err
			}
		case "ssh":
			if err := tuiSSHServer(); err != nil {
				ui.Error(err.Error())
			}
		case "remove":
			if err := tuiRemoveServer(cfg); err != nil {
				return err
			}
		case "back", "":
			return nil
		}
	}
}

// tuiAddServer prompts for host and runs the init flow.
func tuiAddServer(cfg *config.Config) error {
	var host, name, keyPath string
	huh.NewInput().Title("Server SSH address (e.g. root@159.65.100.42)").Value(&host).Run() //nolint:errcheck
	if host == "" {
		return nil
	}
	huh.NewInput().Title("Server name (leave empty to auto-detect)").Value(&name).Run()                       //nolint:errcheck
	huh.NewInput().Title("SSH key path (leave empty to use default ~/.ssh/id_ed25519)").Value(&keyPath).Run() //nolint:errcheck
	return runInitWithKey(host, name, keyPath)
}

// tuiSwitchServer prompts to switch the active server.
func tuiSwitchServer(cfg *config.Config) error {
	opts := make([]ui.SelectOption, 0, len(cfg.Servers))
	for _, srv := range cfg.Servers {
		label := srv.Name
		if srv.Name == cfg.Current {
			label += " " + ui.Faint.Render("(active)")
		}
		opts = append(opts, ui.SelectOption{label, srv.Name})
	}

	selected := ui.Select("Switch to which server?", opts)

	if selected == "" {
		return nil
	}

	cfg.Current = selected
	config.Save(cfg)
	ui.Success(fmt.Sprintf("Switched to %q", selected))
	return nil
}

// tuiRemoveServer prompts to select and remove a server.
func tuiRemoveServer(cfg *config.Config) error {
	opts := make([]ui.SelectOption, 0, len(cfg.Servers)+1)
	for _, srv := range cfg.Servers {
		label := fmt.Sprintf("%s (%s)", srv.Name, srv.Host)
		opts = append(opts, ui.SelectOption{label, srv.Name})
	}
	opts = append(opts, ui.SelectOption{"Cancel", ""})

	selected := ui.Select("Remove which server?", opts)

	if selected == "" {
		return nil
	}

	var confirm bool
	huh.NewConfirm().
		Title(fmt.Sprintf("Remove server %q? This only removes it from neo config.", selected)).
		Value(&confirm).
		Run() //nolint:errcheck

	if !confirm {
		return nil
	}

	cfg.RemoveServer(selected)
	if err := config.Save(cfg); err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Removed server %q", selected))
	return nil
}

// tuiAppsMenu shows the applications submenu for the current server.
func tuiAppsMenu(cfg *config.Config) error {
	srv, err := cfg.CurrentServer()
	if err != nil {
		ui.Error("No server selected. Add one first.")
		return nil
	}

	// Connect + load state with a 10-second timeout — the server may be unreachable.
	type connResult struct {
		exec *neossh.Executor
		st   *state.State
		err  error
	}
	ch := make(chan connResult, 1)
	go func() {
		e, err := connectSSH(srv)
		if err != nil {
			ch <- connResult{err: err}
			return
		}
		s, err := state.Load(e)
		if err != nil {
			e.Close()
			ch <- connResult{err: fmt.Errorf("cannot read server state: %w", err)}
			return
		}
		ch <- connResult{exec: e, st: s}
	}()

	stopLoading := ui.ShowLoading(fmt.Sprintf("Connecting to %s...", srv.Name))

	var srvExec *neossh.Executor
	var st *state.State
	select {
	case res := <-ch:
		stopLoading()
		if res.err != nil {
			ui.Error(fmt.Sprintf("Cannot reach %s: %s", srv.Name, res.err))
			return nil
		}
		srvExec = res.exec
		st = res.st
		// Update the server cache immediately so main menu counts reflect the
		// just-deployed app without waiting for the background refresh goroutine.
		runningApps, runningSvcs := 0, 0
		for _, a := range st.Apps {
			if a.Status == "running" {
				runningApps++
			}
		}
		for _, s := range st.Services {
			if s.Status == "running" {
				runningSvcs++
			}
		}
		config.UpdateServerCache(srv.Name, config.ServerCache{
			AppCount:        len(st.Apps),
			RunningApps:     runningApps,
			ServiceCount:    len(st.Services),
			RunningServices: runningSvcs,
			Reachable:       true,
			UpdatedAt:       time.Now(),
		})
	case <-time.After(10 * time.Second):
		stopLoading()
		// Drain the channel in background so the goroutine can exit cleanly.
		go func() {
			if res, ok := <-ch; ok && res.exec != nil {
				res.exec.Close()
			}
		}()
		ui.Error(fmt.Sprintf("Cannot reach %s (timed out after 10s)", srv.Name))
		return nil
	}
	defer srvExec.Close()

	if len(st.Apps) == 0 {
		ui.ClearScreen()
		fmt.Print(ui.RenderBanner())
		fmt.Print("\r\n  No apps installed.\r\n\r\n")
		ui.Info("Install an app from the main menu")
		fmt.Print("\r\n")
		ui.ReadKey() //nolint:errcheck
		return nil
	}

	for {
		// Reload state on each iteration so statuses stay current after any action
		// (deploy, start, stop, restart) without re-entering the menu.
		if fresh, loadErr := state.Load(srvExec); loadErr == nil {
			st = fresh
		}

		running, stopped := 0, 0
		appNames := make([]string, 0, len(st.Apps))
		for _, a := range st.Apps {
			if a.Status == "running" {
				running++
			} else {
				stopped++
			}
			appNames = append(appNames, a.Name)
		}

		title := fmt.Sprintf("  Apps on %s  %s",
			ui.Bold.Render(srv.Name),
			ui.Faint.Render(fmt.Sprintf("%d apps · %d running · %d stopped", len(st.Apps), running, stopped)))

		opts := make([]ui.SelectOption, 0, len(appNames)+1)
		for _, name := range appNames {
			a := st.Apps[name]
			bullet := ui.StatusBullet(a.Status)
			domain := a.Domain
			if domain == "" {
				domain = "—"
			}
			label := fmt.Sprintf("%s %-18s %s", bullet, a.Name, ui.Faint.Render(domain))
			opts = append(opts, ui.SelectOption{label, a.Name})
		}
		opts = append(opts, ui.SelectOption{"Back", "back"})

		selected := ui.Select(title, opts)

		if selected == "" || selected == "back" {
			return nil
		}

		done, err := tuiAppActions(selected, st, srvExec)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// tuiAppActions shows actions for a selected app. Returns true to exit to main menu.
// Loops so that after any action (or returning from a submenu) the same app's
// actions menu is shown again — Back returns to the app list.
func tuiAppActions(appName string, st *state.State, exec *neossh.Executor) (bool, error) {
	for {
		// Reload state so domain, status, and env changes appear immediately after any action.
		if fresh, loadErr := state.Load(exec); loadErr == nil {
			st = fresh
		}

		a, ok := st.Apps[appName]
		if !ok {
			return false, nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("  %s  %s\n", ui.Bold.Render(a.Name), ui.Faint.Render(a.Image)))
		if domains := a.AllDomains(); len(domains) > 0 {
			scheme := "https"
			if a.HTTPOnly {
				scheme = "http"
			}
			for _, d := range domains {
				sb.WriteString(fmt.Sprintf("  %s\n", ui.Cyan.Render(scheme+"://"+d)))
			}
		}
		sb.WriteString(fmt.Sprintf("  Status: %s %s", ui.StatusBullet(a.Status), a.Status))
		for name := range a.Services {
			sb.WriteString(fmt.Sprintf("\n  %s %s", ui.Faint.Render("└"), name))
		}

		opts := []ui.SelectOption{
			{"View Logs", "logs"},
		}

		if len(a.Workers) > 0 {
			label := fmt.Sprintf("%-22s%s", "Workers", ui.Faint.Render(fmt.Sprintf("%d", len(a.Workers))))
			opts = append(opts, ui.SelectOption{label, "workers"})
		}
		if len(a.Sidecars) > 0 {
			label := fmt.Sprintf("%-22s%s", "Sidecars", ui.Faint.Render(fmt.Sprintf("%d", len(a.Sidecars))))
			opts = append(opts, ui.SelectOption{label, "sidecars"})
		}

		// Show Browse DB if the app has a linked database container
		if db, dbErr := resolveAppDB(st, appName); dbErr == nil && db.Container != "" {
			dbLabel := fmt.Sprintf("%-22s%s", "Browse DB", ui.Faint.Render(db.Type+" · "+db.Database))
			opts = append(opts, ui.SelectOption{dbLabel, "browse-db"})
		}

		if a.Status == "running" {
			opts = append(opts,
				ui.SelectOption{"Restart", "restart"},
				ui.SelectOption{"Stop", "stop"},
				ui.SelectOption{"Docker Terminal", "terminal"},
			)
		} else {
			opts = append(opts, ui.SelectOption{"Start", "start"})
		}

		domainCount := len(a.AllDomains())
		domainHint := ui.Faint.Render("none")
		if domainCount == 1 {
			domainHint = ui.Faint.Render(a.Domain)
		} else if domainCount > 1 {
			domainHint = ui.Faint.Render(fmt.Sprintf("%d domains", domainCount))
		}
		opts = append(opts, ui.SelectOption{fmt.Sprintf("%-22s%s", "Manage Domains", domainHint), "domain"})
		if a.Domain != "" {
			if a.HTTPOnly {
				opts = append(opts, ui.SelectOption{"Enable HTTPS", "https-on"})
			} else {
				opts = append(opts, ui.SelectOption{"Switch to HTTP only", "https-off"})
			}
		}

		opts = append(opts,
			ui.SelectOption{"Update Image", "update"},
			ui.SelectOption{"Deploy New Version", "deploy"},
			ui.SelectOption{"Restart with New Env", "env-only"},
			ui.SelectOption{ui.Red.Render("Remove"), "remove"},
			ui.SelectOption{"Back", "back"},
		)

		action := ui.Select(sb.String(), opts)
		if action == "" || action == "back" {
			return false, nil
		}

		// terminal, browse-db, and remove exit the loop; everything else loops back.
		switch action {
		case "browse-db":
			return true, runDbTUI(appName)
		case "terminal":
			return true, tuiDockerTerminal(appName)
		case "run-cmd":
			if err := tuiRunCommand(appName); err != nil {
				return false, err
			}
		case "remove":
			return false, runRemove(appName, false)
		case "logs":
			if err := runLogs(appName, 50, false, "", "", ""); err != nil {
				return false, err
			}
			fmt.Print("\n  " + ui.Faint.Render("Press any key to return..."))
			ui.ReadKey()
			fmt.Println()
		case "workers":
			if err := tuiWorkersMenu(appName, a.Workers); err != nil {
				return false, err
			}
		case "sidecars":
			if err := tuiSidecarsMenu(appName, a.Sidecars); err != nil {
				return false, err
			}
		case "start", "stop", "restart":
			if err := runManage(appName, action); err != nil {
				return false, err
			}
		case "domain":
			if err := tuiManageDomains(appName); err != nil {
				return false, err
			}
		case "https-on":
			if err := runSetHTTPS(appName, true); err != nil {
				return false, err
			}
		case "https-off":
			if err := runSetHTTPS(appName, false); err != nil {
				return false, err
			}
		case "deploy":
			if err := tuiDeployApp(appName, false); err != nil {
				return false, err
			}
		case "env-only":
			if err := tuiDeployApp(appName, true); err != nil {
				return false, err
			}
		case "update":
			if err := runUpdate(appName); err != nil {
				return false, err
			}
		}
	}
}

// tuiWorkersMenu lists all workers for an app and shows per-worker actions.
func tuiWorkersMenu(appName string, workers map[string]state.AppWorker) error {
	// Single worker → skip picker, go straight to actions
	if len(workers) == 1 {
		for wName, w := range workers {
			return tuiWorkerActions(appName, wName, w)
		}
	}

	for {
		opts := make([]ui.SelectOption, 0, len(workers)+1)
		for wName, w := range workers {
			bullet := ui.StatusBullet(w.Status)
			label := fmt.Sprintf("%s %-20s%s", bullet, wName, ui.Faint.Render(w.Status))
			opts = append(opts, ui.SelectOption{label, wName})
		}
		opts = append(opts, ui.SelectOption{"Back", "back"})

		selected := ui.Select(fmt.Sprintf("  Workers — %s", ui.Bold.Render(appName)), opts)
		if selected == "" || selected == "back" {
			return nil
		}
		if err := tuiWorkerActions(appName, selected, workers[selected]); err != nil {
			return err
		}
	}
}

// tuiWorkerActions shows actions for a single worker container.
// Loops so that after an action the same worker's menu is shown again.
func tuiWorkerActions(appName, workerName string, w state.AppWorker) error {
	for {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("  Worker: %s\n", ui.Bold.Render(workerName)))
		sb.WriteString(fmt.Sprintf("  App:     %s\n", ui.Faint.Render(appName)))
		sb.WriteString(fmt.Sprintf("  Command: %s\n", ui.Faint.Render(w.Command)))
		sb.WriteString(fmt.Sprintf("  Status:  %s %s", ui.StatusBullet(w.Status), w.Status))

		opts := []ui.SelectOption{{"View Logs", "logs"}}
		if w.Status == "running" {
			opts = append(opts,
				ui.SelectOption{"Restart", "restart"},
				ui.SelectOption{"Stop", "stop"},
				ui.SelectOption{"Terminal", "terminal"},
			)
		} else {
			opts = append(opts, ui.SelectOption{"Start", "start"})
		}
		opts = append(opts,
			ui.SelectOption{"Redeploy (stop → remove → re-run)", "redeploy"},
			ui.SelectOption{"Back", "back"},
		)

		action := ui.Select(sb.String(), opts)
		switch action {
		case "", "back":
			return nil
		case "logs":
			if err := runLogs(appName, 50, false, workerName, "", ""); err != nil {
				return err
			}
			fmt.Print("\n  " + ui.Faint.Render("Press any key to return..."))
			ui.ReadKey()
			fmt.Println()
		case "start", "stop", "restart":
			if err := runWorkerManage(appName, workerName, action); err != nil {
				return err
			}
			// Update status for next render.
			w.Status = action
			if action == "stop" {
				w.Status = "stopped"
			}
		case "terminal":
			return runContainerTerminal(config.WorkerContainer(appName, workerName))
		case "redeploy":
			if err := runWorkerRedeploy(appName, workerName); err != nil {
				return err
			}
			w.Status = "running"
		}
	}
}

// tuiSidecarsMenu lists all sidecars for an app and shows per-sidecar actions.
func tuiSidecarsMenu(appName string, sidecars map[string]state.AppSidecar) error {
	// Single sidecar → skip picker, go straight to actions
	if len(sidecars) == 1 {
		for scName, sc := range sidecars {
			return tuiSidecarActions(appName, scName, sc)
		}
	}

	for {
		opts := make([]ui.SelectOption, 0, len(sidecars)+1)
		for scName, sc := range sidecars {
			bullet := ui.StatusBullet(sc.Status)
			label := fmt.Sprintf("%s %-20s%s", bullet, scName, ui.Faint.Render(sc.Image))
			opts = append(opts, ui.SelectOption{label, scName})
		}
		opts = append(opts, ui.SelectOption{"Back", "back"})

		selected := ui.Select(fmt.Sprintf("  Sidecars — %s", ui.Bold.Render(appName)), opts)
		if selected == "" || selected == "back" {
			return nil
		}
		if err := tuiSidecarActions(appName, selected, sidecars[selected]); err != nil {
			return err
		}
	}
}

// tuiSidecarActions shows actions for a single sidecar container.
// Loops so that after an action the same sidecar's menu is shown again.
func tuiSidecarActions(appName, sidecarName string, sc state.AppSidecar) error {
	for {
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("  Sidecar: %s\n", ui.Bold.Render(sidecarName)))
		sb.WriteString(fmt.Sprintf("  App:     %s\n", ui.Faint.Render(appName)))
		sb.WriteString(fmt.Sprintf("  Image:   %s\n", ui.Faint.Render(sc.Image)))
		sb.WriteString(fmt.Sprintf("  Status:  %s %s", ui.StatusBullet(sc.Status), sc.Status))

		opts := []ui.SelectOption{{"View Logs", "logs"}}
		if sc.Status == "running" {
			opts = append(opts,
				ui.SelectOption{"Restart", "restart"},
				ui.SelectOption{"Stop", "stop"},
				ui.SelectOption{"Terminal", "terminal"},
			)
		} else {
			opts = append(opts, ui.SelectOption{"Start", "start"})
		}
		opts = append(opts,
			ui.SelectOption{"Redeploy (stop → remove → re-run)", "redeploy"},
			ui.SelectOption{"Back", "back"},
		)

		action := ui.Select(sb.String(), opts)
		switch action {
		case "", "back":
			return nil
		case "logs":
			if err := runSidecarLogs(appName, sidecarName); err != nil {
				return err
			}
			fmt.Print("\n  " + ui.Faint.Render("Press any key to return..."))
			ui.ReadKey()
			fmt.Println()
		case "start", "stop", "restart":
			if err := runSidecarManage(appName, sidecarName, action); err != nil {
				return err
			}
			sc.Status = action
			if action == "stop" {
				sc.Status = "stopped"
			}
		case "terminal":
			return runContainerTerminal(config.SvcContainer(appName, sidecarName))
		case "redeploy":
			if err := runSidecarRedeploy(appName, sidecarName); err != nil {
				return err
			}
			sc.Status = "running"
		}
	}
}

// runSidecarLogs connects via SSH and streams logs for the named sidecar container.
func runSidecarLogs(appName, sidecarName string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	containerName := config.SvcContainer(appName, sidecarName)
	docker := remote.NewDocker(exec)
	return docker.Logs(containerName, 50, false, os.Stdout)
}

// tuiManageDomains shows a sub-menu for managing all domains on an app.
func tuiManageDomains(appName string) error {
	for {
		_, st, err := mustResolveAndLoadState()
		if err != nil {
			return err
		}
		a := st.Apps[appName]
		domains := a.AllDomains()

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("  Domains for %s\n", ui.Bold.Render(appName)))
		if len(domains) == 0 {
			sb.WriteString("  " + ui.Faint.Render("No domain configured") + "\n")
		} else {
			scheme := "https"
			if a.HTTPOnly {
				scheme = "http"
			}
			for _, d := range domains {
				sb.WriteString(fmt.Sprintf("  %s  %s\n", ui.Cyan.Render("→"), scheme+"://"+d))
			}
		}

		opts := []ui.SelectOption{
			{"Set domain (replace all)", "set"},
			{"Add domain", "add"},
		}
		if len(domains) > 0 {
			opts = append(opts, ui.SelectOption{"Remove a domain", "remove"})
		}
		opts = append(opts, ui.SelectOption{"Back", "back"})

		action := ui.Select(sb.String(), opts)
		switch action {
		case "", "back":
			return nil
		case "set":
			var domain string
			huh.NewInput().
				Title("New domain for " + appName + " (replaces all existing)").
				Placeholder("app.example.com").
				Value(&domain).
				Run() //nolint:errcheck
			if domain == "" {
				continue
			}
			if err := runDomain(appName, domain, false, domainModeOptions{}); err != nil {
				ui.Error(err.Error())
				continue
			}
			printDNSInstructions(domain)
		case "add":
			var domain string
			huh.NewInput().
				Title("Additional domain for " + appName).
				Placeholder("other.example.com").
				Value(&domain).
				Run() //nolint:errcheck
			if domain == "" {
				continue
			}
			if err := runDomain(appName, domain, true, domainModeOptions{}); err != nil {
				ui.Error(err.Error())
				continue
			}
			printDNSInstructions(domain)
		case "remove":
			if len(domains) == 0 {
				continue
			}
			removeOpts := make([]ui.SelectOption, 0, len(domains)+1)
			for _, d := range domains {
				removeOpts = append(removeOpts, ui.SelectOption{d, d})
			}
			removeOpts = append(removeOpts, ui.SelectOption{"Cancel", ""})
			picked := ui.Select("Remove which domain?", removeOpts)
			if picked == "" {
				continue
			}
			if err := runDomainRemove(appName, picked); err != nil {
				ui.Error(err.Error())
			}
		}
	}
}

// printDNSInstructions shows DNS setup guidance after a domain is set.
func printDNSInstructions(domain string) {
	fmt.Println()
	card := ui.NewCard()
	card.Add(ui.Bold.Render("DNS Setup Required"))
	card.Blank()
	card.Add("Add an A record in your DNS provider:")
	card.Add(fmt.Sprintf("  %s  →  %s", ui.Cyan.Render(domain), ui.Bold.Render("<your-server-IP>")))
	card.Blank()
	card.Add(ui.Bold.Render("Cloudflare users:"))
	card.Add("  Set proxy to " + ui.Yellow.Render("DNS only (grey cloud)"))
	card.Add("  Caddy handles SSL — the orange cloud interferes.")
	card.Blank()
	card.Add(ui.Faint.Render("SSL cert auto-provisioned once DNS propagates (1–5 min)"))
	card.Render()
	fmt.Print("\n  " + ui.Faint.Render("Press any key to return..."))
	ui.ReadKey()
	fmt.Println()
}

// tuiLiveMetrics shows a live-updating server + container metrics view.
func tuiLiveMetrics(cfg *config.Config) error {
	srv, err := cfg.CurrentServer()
	if err != nil {
		ui.Error("No server selected.")
		return nil
	}

	stopLoading := ui.ShowLoading(fmt.Sprintf("Connecting to %s...", srv.Name))
	exec, connErr := connectSSH(srv)
	stopLoading()
	if connErr != nil {
		ui.Error(fmt.Sprintf("Cannot reach %s: %s", srv.Name, connErr))
		return nil
	}
	defer exec.Close()

	return ui.RunLiveView(ui.LiveViewConfig{
		Title:    fmt.Sprintf("  %s  %s", ui.Bold.Render(srv.Name), ui.Faint.Render(srv.Host)),
		Interval: 3 * time.Second,
		Render: func() (string, error) {
			return fetchLiveMetrics(exec)
		},
	})
}

// tuiRunCommand prompts for a command and executes it in the app container.
func tuiRunCommand(appName string) error {
	ui.ShowCursor()
	fmt.Println()

	var command string
	err := huh.NewInput().
		Title(fmt.Sprintf("Command to run in %s", appName)).
		Placeholder("e.g. php artisan migrate").
		Value(&command).
		Run()
	if err != nil || strings.TrimSpace(command) == "" {
		return nil
	}

	// Split command into args (simple space split)
	args := strings.Fields(command)
	return runExec(appName, "", "", args, false)
}

// tuiDockerTerminal opens an interactive shell in the app's main container via SSH.
func tuiDockerTerminal(appName string) error {
	return runContainerTerminal(config.AppContainer(appName))
}

// runContainerTerminal opens an interactive shell in any named Docker container via SSH.
func runContainerTerminal(containerName string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	srv, err := resolveServer(cfg)
	if err != nil {
		return err
	}

	ui.ShowCursor()
	fmt.Printf("\n  Opening terminal in %s (type 'exit' to return)\n\n", ui.Bold.Render(containerName))

	sshArgs := buildSSHArgs(srv)
	sshArgs = append(sshArgs, "-t") // force PTY
	sshArgs = append(sshArgs, srv.Host)
	sshArgs = append(sshArgs, "docker", "exec", "-it", containerName, "sh", "-c",
		"bash 2>/dev/null || sh")

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found: %w", err)
	}

	c := exec.Command(sshPath, sshArgs...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// tuiSSHServer opens an SSH session to the current server and returns to neo afterwards.
func tuiSSHServer() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	srv, err := resolveServer(cfg)
	if err != nil {
		return err
	}

	fmt.Printf("\n  SSH into %s (type 'exit' to return to neo)\n\n", ui.Bold.Render(srv.Host))

	sshArgs := buildSSHArgs(srv)
	sshArgs = append(sshArgs, srv.Host)

	sshPath, err := exec.LookPath("ssh")
	if err != nil {
		return fmt.Errorf("ssh not found: %w", err)
	}

	c := exec.Command(sshPath, sshArgs...)
	c.Stdin = os.Stdin
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// buildSSHArgs builds the base ssh arguments for a server (port + key).
func buildSSHArgs(srv *config.Server) []string {
	args := []string{"-o", "StrictHostKeyChecking=accept-new"}
	if srv.Port != 0 && srv.Port != 22 {
		args = append(args, "-p", fmt.Sprintf("%d", srv.Port))
	}
	if srv.Key != "" {
		args = append(args, "-i", srv.Key)
	}
	// Always include neo's managed key if it exists
	if neossh.NeoKeyExists() {
		args = append(args, "-i", neossh.NeoKeyPath())
	}
	return args
}

// tuiServicesMenu shows the shared services submenu.
func tuiServicesMenu(cfg *config.Config) error {
	srv, err := cfg.CurrentServer()
	if err != nil {
		ui.Error("No server selected. Add one first.")
		return nil
	}

	stopLoading := ui.ShowLoading(fmt.Sprintf("Connecting to %s...", srv.Name))
	exec, err := connectSSH(srv)
	if err != nil {
		stopLoading()
		ui.Error(fmt.Sprintf("Cannot reach server: %s", err))
		return nil
	}
	defer exec.Close()

	st, err := state.Load(exec)
	stopLoading()
	if err != nil {
		ui.Error("Cannot read server state")
		return nil
	}

	for {
		title := fmt.Sprintf("  Shared Services on %s", ui.Bold.Render(srv.Name))

		svcNames := make([]string, 0, len(st.Services))
		for name := range st.Services {
			svcNames = append(svcNames, name)
		}
		opts := make([]ui.SelectOption, 0, len(st.Services)+2)
		for _, name := range svcNames {
			svc := st.Services[name]
			bullet := ui.StatusBullet(svc.Status)
			label := fmt.Sprintf("%s %-15s %s", bullet, name, ui.Faint.Render(svc.Image))
			opts = append(opts, ui.SelectOption{label, name})
		}
		opts = append(opts,
			ui.SelectOption{"Create New Service", "create"},
			ui.SelectOption{"Back", "back"},
		)

		selected := ui.Select(title, opts)

		switch selected {
		case "", "back":
			return nil
		case "create":
			before := make(map[string]bool)
			for n := range st.Services {
				before[n] = true
			}
			if err := runServiceCreate("", ""); err != nil {
				ui.Error(err.Error())
			}
			// Reload state and show credentials for the new service via TUI
			if freshSt, err := state.Load(exec); err == nil {
				st = freshSt
				for n, svc := range st.Services {
					if !before[n] {
						tuiShowServiceInfo(svc)
						break
					}
				}
			}
		default:
			done, err := tuiServiceActions(selected, st)
			if err != nil {
				return err
			}
			if done {
				return nil
			}
			// Reload state after action
			if freshSt, err := state.Load(exec); err == nil {
				st = freshSt
			}
		}
	}
}

// tuiServiceActions shows actions for a selected shared service.
func tuiServiceActions(svcName string, st *state.State) (bool, error) {
	svc, ok := st.Services[svcName]
	if !ok {
		return false, nil
	}

	label := fmt.Sprintf("  %s  %s\n  Status: %s %s",
		ui.Bold.Render(svc.Name), ui.Faint.Render(svc.Image),
		ui.StatusBullet(svc.Status), svc.Status)

	svcType := detectServiceType(svc.Image)
	isDB := svcType == "mysql" || svcType == "mariadb" || svcType == "postgres"

	opts := []ui.SelectOption{
		{"View Logs", "logs"},
		{"Connection Info", "info"},
	}

	if svc.Status == "running" {
		opts = append(opts, ui.SelectOption{"Open Tunnel  " + ui.Faint.Render("(TablePlus / DataGrip)"), "tunnel"})
	}

	if isDB && svc.Status == "running" {
		opts = append(opts, ui.SelectOption{"Browse Database", "db"})
	}

	if svc.Status == "running" {
		opts = append(opts,
			ui.SelectOption{"Restart", "restart"},
			ui.SelectOption{"Stop", "stop"},
		)
	} else {
		opts = append(opts, ui.SelectOption{"Start", "start"})
	}

	opts = append(opts,
		ui.SelectOption{ui.Red.Render("Remove"), "remove"},
		ui.SelectOption{"Back", "back"},
	)

	action := ui.Select(label, opts)

	if action == "" || action == "back" {
		return false, nil
	}

	switch action {
	case "logs":
		if err := runServiceLogs(svcName, 50, false); err != nil {
			return false, err
		}
		fmt.Print("\n  " + ui.Faint.Render("Press any key to return..."))
		ui.ReadKey()
		fmt.Println()
		return false, nil
	case "info":
		tuiShowServiceInfo(svc)
		return false, nil
	case "tunnel":
		return false, runTunnel(svcName, 0)
	case "db":
		return false, runServiceDBTUI(svcName)
	case "start", "stop", "restart":
		return false, runServiceManage(svcName, action)
	case "remove":
		return false, runServiceRemove(svcName, false)
	}

	return false, nil
}

// tuiShowServiceInfo renders the connection card and waits for the user to dismiss it.
func tuiShowServiceInfo(svc state.SharedService) {
	card := ui.NewCard()
	card.Add(ui.Bold.Render(svc.Name) + "  " + ui.Faint.Render(svc.Image))
	card.Add(fmt.Sprintf("Status: %s %s", ui.StatusBullet(svc.Status), svc.Status))
	card.Blank()
	printConnInfoLines(card, svc, svc.DefaultDB)
	card.Render()

	// Wait for Enter/Esc via a confirm prompt with no real choice
	var dismiss bool
	huh.NewConfirm().
		Title("Press Enter to go back").
		Affirmative("Back").
		Negative("").
		Value(&dismiss).
		Run() //nolint:errcheck
}
