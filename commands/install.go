package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/app"
	"github.com/vxero/neo/internal/ui"
)

func newInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install [app]",
		Short: "Scaffold a bundled app template into a folder",
		Long: "Scaffolds a ready-to-deploy project for a bundled app template. Prompts for\n" +
			"a folder, then writes a docker-compose.yml (app image + bundled databases),\n" +
			"a .neo.yml, and a .env with generated secrets. Then run `neo deploy` in that\n" +
			"folder. If no app name is given, shows an interactive picker.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := ""
			if len(args) > 0 {
				appName = args[0]
			}
			return runInstall(appName)
		},
	}
}

func runInstall(appName string) error {
	registry, err := app.NewRegistry()
	if err != nil {
		return fmt.Errorf("load app registry: %w", err)
	}

	// Select the template.
	var manifest *app.Manifest
	if appName == "" {
		if manifest, err = pickApp(registry); err != nil || manifest == nil {
			return err
		}
	} else {
		m, ok := registry.Get(appName)
		if !ok {
			ui.Error(fmt.Sprintf("Unknown app %q. Run 'neo install' to see available apps.", appName))
			return nil
		}
		manifest = m
	}

	// Ask where to scaffold.
	dir := "./" + manifest.Name
	if err := huh.NewInput().
		Title("Folder to scaffold " + manifest.Title + " into").
		Description("A docker-compose.yml, .neo.yml, and .env will be written here.").
		Placeholder("./" + manifest.Name).
		Value(&dir).
		Run(); err != nil {
		return nil
	}
	dir = strings.TrimSpace(dir)
	if dir == "" {
		dir = "./" + manifest.Name
	}

	// Refuse to clobber an existing project.
	for _, f := range []string{"docker-compose.yml", ".neo.yml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			ui.Error(fmt.Sprintf("%s already exists in %s — choose an empty folder.", f, dir))
			return nil
		}
	}

	// Domain (optional) + any ask-able env vars.
	domain, userVars, err := collectConfig(manifest)
	if err != nil {
		return err
	}

	// Generate each bundled service's env (secrets), then resolve the app env.
	serviceEnvs := make(map[string]map[string]string)
	for _, svc := range manifest.Services {
		e := make(map[string]string)
		for _, ev := range svc.Env {
			switch {
			case ev.Generate != "":
				v, err := app.GenerateValue(ev.Generate)
				if err != nil {
					return err
				}
				e[ev.Key] = v
			case ev.Value != "":
				e[ev.Key] = ev.Value
			}
		}
		serviceEnvs[svc.Name] = e
	}
	appEnv := resolveScaffoldEnv(manifest, domain, userVars, serviceEnvs)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "docker-compose.yml"), []byte(composeYAML(manifest, serviceEnvs)), 0o644); err != nil {
		return fmt.Errorf("write docker-compose.yml: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envFile(manifest, appEnv)), 0o600); err != nil {
		return fmt.Errorf("write .env: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".neo.yml"), []byte(neoYML(manifest, domain)), 0o644); err != nil {
		return fmt.Errorf("write .neo.yml: %w", err)
	}

	// Success + next steps.
	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " " + manifest.Title + " scaffolded")
	card.Blank()
	card.AddKV("Folder", dir)
	card.Add(ui.Faint.Render("  docker-compose.yml · .neo.yml · .env"))
	card.Blank()
	card.Add("Next steps:")
	card.Add("  cd " + dir)
	if domain == "" {
		card.Add("  # set a domain in .neo.yml (or: neo domain " + manifest.Name + " --temp)")
	}
	card.Add("  " + ui.Cyan.Render("neo deploy"))
	card.Render()

	if strings.TrimSpace(manifest.Notes) != "" {
		fmt.Println()
		fmt.Println("  " + ui.Bold.Render("Notes"))
		for _, line := range strings.Split(strings.TrimRight(manifest.Notes, "\n"), "\n") {
			fmt.Println("  " + ui.Faint.Render(line))
		}
	}
	fmt.Println()
	return nil
}

// resolveScaffoldEnv builds the app's environment for a scaffolded compose
// project. Service references resolve to the compose service name (host) and
// generated service secrets (passwords), not server-side container names.
func resolveScaffoldEnv(m *app.Manifest, domain string, userVars map[string]string, serviceEnvs map[string]map[string]string) map[string]string {
	env := make(map[string]string)
	for _, e := range m.Env {
		switch {
		case e.From == "domain":
			env[e.Key] = "https://" + domain
		case e.From == "domain_host":
			env[e.Key] = domain
		case e.Generate != "":
			v, _ := app.GenerateValue(e.Generate)
			env[e.Key] = v
		case e.FromService != "" && e.Template != "" && !strings.Contains(e.Template, "${"):
			// A service hostname reference — use the compose service name.
			env[e.Key] = e.FromService
		case e.Template != "":
			env[e.Key] = expandServiceVars(e.Template, serviceEnvs)
		case e.Value != "":
			env[e.Key] = e.Value
		case e.Ask:
			if v, ok := userVars[e.Key]; ok {
				env[e.Key] = v
			}
		}
	}
	return env
}

// composeYAML renders a docker-compose.yml for the template: the app service
// (env from .env) plus each bundled service, with named volumes.
func composeYAML(m *app.Manifest, serviceEnvs map[string]map[string]string) string {
	var b strings.Builder
	vols := map[string]bool{}

	fmt.Fprintf(&b, "# Generated by `neo install %s` — edit freely, then run `neo deploy`.\n", m.Name)
	b.WriteString("services:\n")

	// App service.
	fmt.Fprintf(&b, "  %s:\n", m.Name)
	fmt.Fprintf(&b, "    image: %s\n", m.Image)
	b.WriteString("    restart: unless-stopped\n")
	b.WriteString("    env_file:\n      - .env\n")
	if m.Port > 0 {
		fmt.Fprintf(&b, "    expose:\n      - \"%d\"\n", m.Port)
	}
	if len(m.Volumes) > 0 {
		b.WriteString("    volumes:\n")
		for _, v := range m.Volumes {
			fmt.Fprintf(&b, "      - %s:%s\n", v.Name, v.Path)
			vols[v.Name] = true
		}
	}
	if len(m.Services) > 0 {
		b.WriteString("    depends_on:\n")
		for _, svc := range m.Services {
			fmt.Fprintf(&b, "      - %s\n", svc.Name)
		}
	}

	// Bundled services.
	for _, svc := range m.Services {
		fmt.Fprintf(&b, "  %s:\n", svc.Name)
		fmt.Fprintf(&b, "    image: %s\n", svc.Image)
		b.WriteString("    restart: unless-stopped\n")
		if env := serviceEnvs[svc.Name]; len(env) > 0 {
			b.WriteString("    environment:\n")
			for _, k := range sortedKeys(env) {
				fmt.Fprintf(&b, "      %s: %s\n", k, yamlValue(env[k]))
			}
		}
		if len(svc.Volumes) > 0 {
			b.WriteString("    volumes:\n")
			for _, v := range svc.Volumes {
				fmt.Fprintf(&b, "      - %s:%s\n", v.Name, v.Path)
				vols[v.Name] = true
			}
		}
	}

	if len(vols) > 0 {
		b.WriteString("volumes:\n")
		names := make([]string, 0, len(vols))
		for n := range vols {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&b, "  %s: {}\n", n)
		}
	}
	return b.String()
}

// envFile renders the .env consumed by the app service.
func envFile(m *app.Manifest, appEnv map[string]string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s environment — generated by `neo install`. Keep secrets private.\n", m.Title)
	for _, k := range sortedKeys(appEnv) {
		fmt.Fprintf(&b, "%s=%s\n", k, appEnv[k])
	}
	return b.String()
}

// neoYML renders a minimal .neo.yml for the scaffolded project.
func neoYML(m *app.Manifest, domain string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# .neo.yml — Neo project config. Docs: https://neo.vxero.dev/docs\n")
	fmt.Fprintf(&b, "name: %s\n", m.Name)
	if domain != "" {
		fmt.Fprintf(&b, "domain: %s\n", domain)
	} else {
		fmt.Fprintf(&b, "# domain: app.example.com          # set a domain (or run: neo domain %s --temp)\n", m.Name)
	}
	if m.Port > 0 {
		fmt.Fprintf(&b, "port: %d\n", m.Port)
	}
	fmt.Fprintf(&b, "compose_service: %s\n", m.Name)
	if m.Health != nil && m.Health.Path != "" {
		b.WriteString("\nhealth:\n")
		fmt.Fprintf(&b, "  cmd: \"curl -f http://localhost:%d%s\"\n", m.Port, m.Health.Path)
		if m.Health.Interval != "" {
			fmt.Fprintf(&b, "  interval: %s\n", m.Health.Interval)
		}
		if m.Health.Retries > 0 {
			fmt.Fprintf(&b, "  retries: %d\n", m.Health.Retries)
		}
	}
	return b.String()
}

// yamlValue quotes a compose environment value when needed.
func yamlValue(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, ":#{}[],&*?|<>=!%@`\"' ") {
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return v
}

// pickApp shows an interactive app picker.
func pickApp(registry *app.Registry) (*app.Manifest, error) {
	apps := registry.List()
	opts := make([]ui.SelectOption, len(apps))
	for i, a := range apps {
		label := fmt.Sprintf("%-15s %s", a.Name, ui.Faint.Render(a.Description))
		opts[i] = ui.SelectOption{Label: label, Value: a.Name}
	}

	selected := ui.Select("Choose an app to scaffold", opts)
	if selected == "" {
		return nil, nil
	}
	m, _ := registry.Get(selected)
	return m, nil
}

// collectConfig prompts for an optional domain and any ask-able env vars.
func collectConfig(m *app.Manifest) (string, map[string]string, error) {
	var domain string
	envVars := make(map[string]string)

	if err := huh.NewInput().
		Title("Domain for " + m.Title + " (optional)").
		Description("Leave blank to set it later in .neo.yml.").
		Placeholder("app.example.com").
		Value(&domain).
		Run(); err != nil {
		return "", nil, err
	}

	for _, e := range m.Env {
		if e.Ask {
			var val string
			if err := huh.NewInput().Title(e.Label).Value(&val).Run(); err != nil {
				return "", nil, err
			}
			envVars[e.Key] = val
		}
	}
	return strings.TrimSpace(domain), envVars, nil
}

// expandServiceVars replaces ${VAR} references with actual service env values.
func expandServiceVars(tmpl string, serviceEnvs map[string]map[string]string) string {
	result := tmpl
	for _, svcEnv := range serviceEnvs {
		for k, v := range svcEnv {
			result = strings.ReplaceAll(result, "${"+k+"}", v)
		}
	}
	return result
}
