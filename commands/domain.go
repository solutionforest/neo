package commands

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

// validDomain matches RFC 1123 hostnames and IP addresses.
var validDomain = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.\-]*[a-zA-Z0-9])?$`)

// validateDomain returns an error if the domain name is invalid.
func validateDomain(domain string) error {
	if domain == "" {
		return nil // empty domain is allowed (means no domain)
	}
	if len(domain) > 253 {
		return fmt.Errorf("domain name too long (max 253 characters)")
	}
	if !validDomain.MatchString(domain) {
		return fmt.Errorf("invalid domain name %q — must contain only letters, digits, hyphens, and dots", domain)
	}
	return nil
}

func newDomainCmd() *cobra.Command {
	var tempFlag bool
	var addFlag, removeFlag bool
	var certFile, keyFile string

	cmd := &cobra.Command{
		Use:   "domain <app> [domain]",
		Short: "Set or change the domain(s) for an app",
		Long: `Set a custom domain, assign a temporary sslip.io domain (--temp), or manage multiple domains.

By default, the new domain replaces the existing one. Use --add to bind an
additional domain alongside the existing one(s), or --remove to unbind a
specific domain without affecting the others.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			appName := args[0]

			if addFlag && removeFlag {
				return fmt.Errorf("--add and --remove are mutually exclusive")
			}

			if tempFlag {
				return runDomainTemp(appName, addFlag)
			}

			if len(args) < 2 {
				return fmt.Errorf("provide a domain name, or use --temp for a temporary sslip.io domain")
			}

			domain := args[1]

			if removeFlag {
				return runDomainRemove(appName, domain)
			}

			if certFile != "" || keyFile != "" {
				if certFile == "" || keyFile == "" {
					return fmt.Errorf("both --cert and --key must be provided together")
				}
				return runDomainCustomCert(appName, domain, certFile, keyFile)
			}

			return runDomain(appName, domain, addFlag)
		},
	}

	cmd.Flags().BoolVar(&tempFlag, "temp", false, "assign a temporary {app}.{ip}.sslip.io domain with auto-SSL")
	cmd.Flags().BoolVar(&addFlag, "add", false, "add domain alongside existing ones instead of replacing")
	cmd.Flags().BoolVar(&removeFlag, "remove", false, "remove a specific domain without affecting others")
	cmd.Flags().StringVar(&certFile, "cert", "", "path to SSL certificate file (PEM)")
	cmd.Flags().StringVar(&keyFile, "key", "", "path to SSL private key file (PEM)")
	return cmd
}

func runDomain(appName, domain string, add bool) error {
	if err := validateDomain(domain); err != nil {
		return err
	}

	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	if add {
		app.AddDomain(domain)
	} else {
		app.Domain = domain
		app.ExtraDomains = nil
	}

	caddy := remote.NewCaddy(exec)
	containerName := config.AppContainer(appName)
	upstream := fmt.Sprintf("%s:%d", containerName, app.InternalPort)
	domains := app.AllDomains()

	spin := ui.NewSpinner(fmt.Sprintf("Updating Caddy route (%d domain(s))...", len(domains)))
	spin.Start()
	var routeErr error
	if app.HTTPOnly {
		routeErr = caddy.UpdateRouteHTTP(containerName, domains, upstream)
	} else {
		routeErr = caddy.UpdateRoute(containerName, domains, upstream)
	}
	spin.Stop()

	if routeErr != nil {
		return fmt.Errorf("update caddy route: %w", routeErr)
	}

	st.Apps[appName] = app
	state.Save(exec, st)

	scheme := "https"
	if app.HTTPOnly {
		scheme = "http"
	}
	if add {
		ui.Success(fmt.Sprintf("Domain added — %s://%s", scheme, domain))
		for _, d := range app.ExtraDomains {
			if d != domain {
				fmt.Printf("  %s (existing)\n", d)
			}
		}
	} else {
		ui.Success(fmt.Sprintf("Domain set — %s://%s", scheme, domain))
	}
	return nil
}

// runDomainRemove removes a single domain from the app without affecting others.
func runDomainRemove(appName, domain string) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	before := len(app.AllDomains())
	app.RemoveDomain(domain)
	if len(app.AllDomains()) == before {
		return fmt.Errorf("domain %q not found on app %q", domain, appName)
	}

	caddy := remote.NewCaddy(exec)
	containerName := config.AppContainer(appName)
	upstream := fmt.Sprintf("%s:%d", containerName, app.InternalPort)
	remaining := app.AllDomains()

	spin := ui.NewSpinner(fmt.Sprintf("Removing %s from Caddy route...", domain))
	spin.Start()
	var routeErr error
	if len(remaining) == 0 {
		routeErr = caddy.RemoveRoute(containerName)
	} else if app.HTTPOnly {
		routeErr = caddy.UpdateRouteHTTP(containerName, remaining, upstream)
	} else {
		routeErr = caddy.UpdateRoute(containerName, remaining, upstream)
	}
	spin.Stop()

	if routeErr != nil {
		return fmt.Errorf("update caddy route: %w", routeErr)
	}

	st.Apps[appName] = app
	state.Save(exec, st)

	ui.Success(fmt.Sprintf("Domain %q removed", domain))
	if len(remaining) > 0 {
		fmt.Printf("  Remaining: %s\n", strings.Join(remaining, ", "))
	} else {
		fmt.Println("  No domains left — app still running, no public route.")
	}
	return nil
}

// runDomainTemp assigns a temporary sslip.io domain with auto-SSL.
// If add is true, it is added alongside existing domains instead of replacing them.
func runDomainTemp(appName string, add bool) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	ip := st.ServerIP
	if ip == "" {
		return fmt.Errorf("server IP not found in state — run 'neo init' to re-initialize")
	}

	domain := fmt.Sprintf("%s.%s.sslip.io", appName, ip)

	if add {
		app.AddDomain(domain)
	} else {
		app.Domain = domain
		app.ExtraDomains = nil
	}
	app.HTTPOnly = false

	caddy := remote.NewCaddy(exec)
	containerName := config.AppContainer(appName)
	upstream := fmt.Sprintf("%s:%d", containerName, app.InternalPort)

	spin := ui.NewSpinner(fmt.Sprintf("Setting up %s with auto-SSL...", domain))
	spin.Start()
	routeErr := caddy.UpdateRoute(containerName, app.AllDomains(), upstream)
	spin.Stop()

	if routeErr != nil {
		return fmt.Errorf("update caddy route: %w", routeErr)
	}

	st.Apps[appName] = app
	state.Save(exec, st)

	card := ui.NewCard()
	card.Add(ui.Bold.Render("Temporary domain ready!"))
	card.Blank()
	card.Add(fmt.Sprintf("  URL:  %s", ui.Green.Render("https://"+domain)))
	if add && len(app.ExtraDomains) > 0 {
		for _, d := range app.AllDomains() {
			if d != domain {
				card.Add(fmt.Sprintf("  Also: %s", ui.Cyan.Render("https://"+d)))
			}
		}
	}
	card.Blank()
	card.Add(ui.Faint.Render("sslip.io resolves to your server IP automatically."))
	card.Add(ui.Faint.Render("SSL certificate auto-provisioned via Let's Encrypt."))
	card.Blank()
	card.Add(ui.Faint.Render("Add a real domain alongside:"))
	card.Add(fmt.Sprintf("  %s", ui.Cyan.Render(fmt.Sprintf("neo domain %s yourdomain.com --add", appName))))
	card.Add(ui.Faint.Render("Remove the temp URL once ready:"))
	card.Add(fmt.Sprintf("  %s", ui.Cyan.Render(fmt.Sprintf("neo domain %s %s --remove", appName, domain))))
	card.Render()

	return nil
}

// runDomainCustomCert sets a domain with a custom SSL certificate.
func runDomainCustomCert(appName, domain, certFile, keyFile string) error {
	if err := validateDomain(domain); err != nil {
		return err
	}

	certData, err := os.ReadFile(certFile)
	if err != nil {
		return fmt.Errorf("cannot read certificate file: %w", err)
	}
	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return fmt.Errorf("cannot read key file: %w", err)
	}

	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	caddy := remote.NewCaddy(exec)
	containerName := config.AppContainer(appName)
	upstream := fmt.Sprintf("%s:%d", containerName, app.InternalPort)

	// Upload cert and key to server
	remoteCertDir := fmt.Sprintf("/etc/neo/certs/%s", appName)
	exec.RunQuiet(fmt.Sprintf("mkdir -p %s", remoteCertDir))

	spin := ui.NewSpinner("Uploading SSL certificate...")
	spin.Start()
	certPath := remoteCertDir + "/cert.pem"
	keyPath := remoteCertDir + "/key.pem"
	if err := exec.WriteFile(certPath, certData, 0644); err != nil {
		spin.Stop()
		return fmt.Errorf("upload certificate: %w", err)
	}
	if err := exec.WriteFile(keyPath, keyData, 0600); err != nil {
		spin.Stop()
		return fmt.Errorf("upload key: %w", err)
	}
	spin.Stop()
	ui.Success("Certificate uploaded")

	spin = ui.NewSpinner("Configuring SSL...")
	spin.Start()
	loadErr := caddy.LoadCertificate(certPath, keyPath)
	spin.Stop()
	if loadErr != nil {
		return fmt.Errorf("load certificate into Caddy: %w", loadErr)
	}

	spin = ui.NewSpinner(fmt.Sprintf("Setting domain to %s...", domain))
	spin.Start()
	app.Domain = domain
	app.ExtraDomains = nil
	app.HTTPOnly = false
	routeErr := caddy.UpdateRoute(containerName, app.AllDomains(), upstream)
	spin.Stop()
	if routeErr != nil {
		return fmt.Errorf("update caddy route: %w", routeErr)
	}

	st.Apps[appName] = app
	state.Save(exec, st)

	ui.Success(fmt.Sprintf("Domain set with custom SSL — https://%s", domain))
	return nil
}

// runSetHTTPS switches an app between HTTPS (secure) and HTTP-only (insecure).
func runSetHTTPS(appName string, httpsOn bool) error {
	exec, st, err := mustResolveAndLoadState()
	if err != nil {
		return err
	}
	defer exec.Close()

	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}

	if app.Domain == "" {
		return fmt.Errorf("no domain set — use 'neo domain %s --temp' first", appName)
	}

	caddy := remote.NewCaddy(exec)
	containerName := config.AppContainer(appName)
	upstream := fmt.Sprintf("%s:%d", containerName, app.InternalPort)

	label := "HTTP only"
	if httpsOn {
		label = "HTTPS"
	}

	spin := ui.NewSpinner(fmt.Sprintf("Switching to %s...", label))
	spin.Start()
	var routeErr error
	domains := app.AllDomains()
	if httpsOn {
		routeErr = caddy.UpdateRoute(containerName, domains, upstream)
	} else {
		routeErr = caddy.UpdateRouteHTTP(containerName, domains, upstream)
	}
	spin.Stop()

	if routeErr != nil {
		return fmt.Errorf("update caddy route: %w", routeErr)
	}

	app.HTTPOnly = !httpsOn
	st.Apps[appName] = app
	state.Save(exec, st)

	if httpsOn {
		ui.Success(fmt.Sprintf("HTTPS enabled — https://%s (SSL cert auto-provisioned)", app.Domain))
	} else {
		ui.Success(fmt.Sprintf("HTTP only — http://%s", app.Domain))
	}
	return nil
}
