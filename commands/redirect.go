package commands

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newRedirectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "redirect",
		Short: "Manage domain redirects (no app required)",
	}
	cmd.AddCommand(
		newRedirectAddCmd(),
		newRedirectListCmd(),
		newRedirectRemoveCmd(),
	)
	return cmd
}

func newRedirectAddCmd() *cobra.Command {
	var temporary bool

	cmd := &cobra.Command{
		Use:   "add <from-domain> <to-url>",
		Short: "Redirect a domain to another URL (301 by default)",
		Example: `  neo redirect add vxero.dev vxero.com
  neo redirect add old.api.com new.api.com --temporary`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			code := 301
			if temporary {
				code = 302
			}
			return runRedirectAdd(args[0], args[1], code)
		},
	}
	cmd.Flags().BoolVar(&temporary, "temporary", false, "use a 302 temporary redirect instead of 301")
	return cmd
}

func newRedirectListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all active domain redirects",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRedirectList()
		},
	}
}

func newRedirectRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <from-domain>",
		Short: "Remove a domain redirect",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRedirectRemove(args[0])
		},
	}
}

func runRedirectAdd(from, to string, code int) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	from = normalizeRedirectFrom(from)
	to = normalizeRedirectTo(to)

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	// Check for conflict with existing app domains
	for _, app := range st.Apps {
		for _, d := range app.AllDomains() {
			if d == from {
				return fmt.Errorf("domain %q is already in use by app %q — remove it from the app first", from, app.Name)
			}
		}
	}

	// Check for duplicate redirect
	if existing, ok := st.Redirects[from]; ok {
		return fmt.Errorf("redirect from %q already exists (→ %s) — remove it first with: neo redirect remove %s", from, existing.To, from)
	}

	spin := ui.NewSpinner(fmt.Sprintf("Setting up redirect %s → %s...", from, to))
	spin.Start()

	caddy := remote.NewCaddy(exec)
	if err := caddy.AddRedirect(from, to, code); err != nil {
		spin.Stop()
		return fmt.Errorf("caddy redirect: %w", err)
	}

	if st.Redirects == nil {
		st.Redirects = make(map[string]state.DomainRedirect)
	}
	st.Redirects[from] = state.DomainRedirect{From: from, To: to, Code: code}
	if err := state.Save(exec, st); err != nil {
		spin.Stop()
		return fmt.Errorf("save state: %w", err)
	}

	spin.Stop()

	codeLabel := "301 permanent"
	if code == 302 {
		codeLabel = "302 temporary"
	}

	card := ui.NewCard()
	card.Add(ui.Green.Render("✓") + " Redirect active!")
	card.Blank()
	card.AddKV("From", from)
	card.AddKV("To", to)
	card.AddKV("Type", codeLabel)
	card.AddKV("SSL", "auto-provisioned by Caddy")
	card.Blank()
	card.Add("Paths are preserved: " + ui.Faint.Render(from+"/blog → "+stripScheme(to)+"/blog"))
	card.Render()

	return nil
}

func runRedirectList() error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	if len(st.Redirects) == 0 {
		fmt.Println()
		fmt.Println("  No redirects configured.")
		fmt.Println()
		fmt.Println("  Add one with: " + ui.Faint.Render("neo redirect add <from> <to>"))
		fmt.Println()
		return nil
	}

	fmt.Println()
	fmt.Printf("  %-30s  %-40s  %s\n", ui.Bold.Render("FROM"), ui.Bold.Render("TO"), ui.Bold.Render("TYPE"))
	fmt.Printf("  %-30s  %-40s  %s\n", strings.Repeat("─", 30), strings.Repeat("─", 40), strings.Repeat("─", 12))

	for _, r := range st.Redirects {
		codeLabel := "301 permanent"
		if r.Code == 302 {
			codeLabel = "302 temporary"
		}
		fmt.Printf("  %-30s  %-40s  %s\n", r.From, r.To, ui.Faint.Render(codeLabel))
	}
	fmt.Println()
	return nil
}

func runRedirectRemove(from string) error {
	_, _, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	from = normalizeRedirectFrom(from)

	st, err := state.Load(exec)
	if err != nil {
		return err
	}

	r, ok := st.Redirects[from]
	if !ok {
		return fmt.Errorf("no redirect found for %q — list with: neo redirect list", from)
	}

	spin := ui.NewSpinner(fmt.Sprintf("Removing redirect %s → %s...", from, r.To))
	spin.Start()

	caddy := remote.NewCaddy(exec)
	caddy.RemoveRedirect(from) // best-effort; Caddy may not have the route if server was rebuilt

	delete(st.Redirects, from)
	if err := state.Save(exec, st); err != nil {
		spin.Stop()
		return fmt.Errorf("save state: %w", err)
	}

	spin.Stop()
	ui.Success(fmt.Sprintf("Redirect %s → %s removed", from, r.To))
	return nil
}

// normalizeRedirectFrom strips any scheme and trailing slash from the source domain.
// e.g. "https://vxero.dev/" → "vxero.dev"
func normalizeRedirectFrom(s string) string {
	s = stripScheme(s)
	return strings.TrimRight(s, "/")
}

// normalizeRedirectTo ensures the destination has an https:// scheme and no trailing slash.
// e.g. "vxero.com" → "https://vxero.com"
func normalizeRedirectTo(s string) string {
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/")
}

// stripScheme removes the URL scheme and any leading slashes.
func stripScheme(s string) string {
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	return s
}
