package commands

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vxero/neo/internal/config"
	"github.com/vxero/neo/internal/remote"
	"github.com/vxero/neo/internal/ssh"
	"github.com/vxero/neo/internal/state"
	"github.com/vxero/neo/internal/ui"
)

func newCaddyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "caddy",
		Short: "Manage the Neo Caddy reverse proxy",
	}

	cmd.AddCommand(newCaddyDNSCmd())
	cmd.AddCommand(newCaddyOnDemandCmd())
	cmd.AddCommand(newCaddyUpdateCmd())
	return cmd
}

func newCaddyUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Pull the latest Caddy image and recreate the proxy (security patches)",
		Long: `Update the neo-caddy reverse proxy to the latest Caddy 2.x image.

Pulls the newest caddy:2-alpine — or rebuilds the DNS-enabled image with a fresh
base — and recreates the container. Routes and TLS certificates are preserved via
the persistent data/config volumes and --resume, so there is no downtime cost
beyond a quick restart.`,
		RunE: func(cmd *cobra.Command, args []string) error { return runCaddyUpdate() },
	}
}

func runCaddyUpdate() error {
	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	caddy := remote.NewCaddy(exec)

	ui.Info(fmt.Sprintf("Updating Caddy on %s...", srv.Name))
	image, err := caddy.Update(os.Stdout)
	if err != nil {
		return err
	}

	ui.Success(fmt.Sprintf("Caddy updated to %s and restarted", image))
	return nil
}

func newCaddyOnDemandCmd() *cobra.Command {
	var appName string
	var askURL string
	var replaceDomains bool

	cmd := &cobra.Command{
		Use:     "ondemand <domain>",
		Aliases: []string{"on-demand", "wildcard"},
		Short:   "Enable wildcard tenant HTTPS with on-demand certs",
		Long: `Enable dynamic tenant subdomains without listing every hostname.

Caddy will route the base domain and *.domain to the app, then issue a real
Let's Encrypt certificate for each tenant hostname on first use. The ask URL is
called before certificate issuance and should return 200 only for allowed hosts.

Example:
  neo --server prod caddy ondemand example.com --app myapp --replace-domains`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCaddyOnDemand(args[0], appName, askURL, replaceDomains)
		},
	}

	cmd.Flags().StringVar(&appName, "app", "", "optional app to bind the wildcard domain to")
	cmd.Flags().StringVar(&askURL, "ask-url", "", "URL Caddy calls to allow/deny certificate issuance")
	cmd.Flags().BoolVar(&replaceDomains, "replace-domains", false, "replace existing app extra domains with the wildcard")
	return cmd
}

func newCaddyDNSCmd() *cobra.Command {
	var providerName string
	var tokenEnv string
	var appName string

	cmd := &cobra.Command{
		Use:   "dns <domain>",
		Short: "Enable wildcard SSL with ACME DNS-01",
		Long: `Enable wildcard HTTPS for a domain by installing a DNS-enabled Caddy build
and configuring ACME DNS-01 automation.

The DNS API token is read from a local environment variable and copied to the
server as a root-only env file. The token is never requested interactively.

Example:
  CLOUDFLARE_API_TOKEN=... neo --server prod caddy dns example.com --provider cloudflare --app myapp`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCaddyDNS(args[0], providerName, tokenEnv, appName)
		},
	}

	cmd.Flags().StringVar(&providerName, "provider", "cloudflare", "DNS provider for ACME DNS-01 (currently: cloudflare)")
	cmd.Flags().StringVar(&tokenEnv, "token-env", "", "local env var containing the DNS API token (default: provider-specific)")
	cmd.Flags().StringVar(&appName, "app", "", "optional app to bind the wildcard domain to after DNS-01 is enabled")
	return cmd
}

func runCaddyDNS(domain, providerName, tokenEnv, appName string) error {
	baseDomain := normalizeWildcardBaseDomain(domain)
	if err := validateDomain(baseDomain); err != nil {
		return err
	}

	provider, err := remote.CaddyDNSProviderFor(providerName)
	if err != nil {
		return err
	}
	if tokenEnv != "" {
		provider.TokenEnv = tokenEnv
	}

	token := os.Getenv(provider.TokenEnv)
	if strings.TrimSpace(token) == "" {
		return fmt.Errorf("%s is not set; export it locally, then rerun this command", provider.TokenEnv)
	}

	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	caddy := remote.NewCaddy(exec)

	ui.Info(fmt.Sprintf("Building Caddy with %s DNS support on %s...", provider.Name, srv.Name))
	image, err := caddy.InstallDNSProvider(provider, token, os.Stdout)
	if err != nil {
		return err
	}

	spin := ui.NewSpinner("Restarting Caddy with DNS credentials...")
	spin.Start()
	err = caddy.RecreateWithImage(image, []string{remote.CaddyDNSEnvFile})
	spin.Stop()
	if err != nil {
		return fmt.Errorf("restart Caddy: %w", err)
	}

	spin = ui.NewSpinner("Configuring ACME DNS-01 automation...")
	spin.Start()
	err = caddy.ConfigureDNSAutomation(baseDomain, provider)
	spin.Stop()
	if err != nil {
		return fmt.Errorf("configure DNS-01 automation: %w", err)
	}

	wildcard := remote.WildcardDomain(baseDomain)
	if appName != "" {
		if err := bindWildcardDomain(exec, caddy, provider, appName, wildcard, false); err != nil {
			return err
		}
	}

	ui.Success(fmt.Sprintf("Wildcard HTTPS enabled for %s", wildcard))
	if appName == "" {
		ui.Info(fmt.Sprintf("Bind it to an app with: neo --server %s domain <app> %q --add", srv.Name, wildcard))
	}
	return nil
}

func normalizeWildcardBaseDomain(domain string) string {
	domain = strings.TrimSpace(domain)
	domain = strings.TrimPrefix(domain, "https://")
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimSuffix(domain, "/")
	domain = strings.TrimPrefix(domain, "*.")
	return domain
}

func runCaddyOnDemand(domain, appName, askURL string, replaceDomains bool) error {
	baseDomain := normalizeWildcardBaseDomain(domain)
	if err := validateDomain(baseDomain); err != nil {
		return err
	}

	_, srv, exec, err := mustResolveAndConnect()
	if err != nil {
		return err
	}
	defer exec.Close()

	caddy := remote.NewCaddy(exec)
	wildcard := remote.WildcardDomain(baseDomain)

	if appName != "" && askURL == "" {
		st, err := state.Load(exec)
		if err != nil {
			return err
		}
		app, ok := st.Apps[appName]
		if !ok {
			return fmt.Errorf("app %q not found", appName)
		}
		askURL = fmt.Sprintf("http://%s:%d/_neo/caddy/ask", config.AppContainer(appName), app.InternalPort)
	}
	if askURL == "" {
		return fmt.Errorf("--ask-url is required when --app is not provided")
	}

	spin := ui.NewSpinner("Configuring guarded on-demand TLS...")
	spin.Start()
	err = caddy.ConfigureOnDemandTLS(baseDomain, askURL)
	spin.Stop()
	if err != nil {
		return fmt.Errorf("configure on-demand TLS: %w", err)
	}

	if appName != "" {
		provider, _ := remote.CaddyDNSProviderFor("cloudflare")
		if err := bindWildcardDomain(exec, caddy, provider, appName, wildcard, replaceDomains); err != nil {
			return err
		}
	}

	ui.Success(fmt.Sprintf("On-demand wildcard HTTPS enabled for %s on %s", wildcard, srv.Name))
	ui.Info(fmt.Sprintf("Ask endpoint: %s", askURL))
	return nil
}

func bindWildcardDomain(exec *ssh.Executor, caddy *remote.Caddy, provider remote.CaddyDNSProvider, appName, wildcard string, replaceDomains bool) error {
	st, err := state.Load(exec)
	if err != nil {
		return err
	}
	app, ok := st.Apps[appName]
	if !ok {
		return fmt.Errorf("app %q not found", appName)
	}
	if app.HTTPOnly {
		return fmt.Errorf("app %q is HTTP-only; wildcard HTTPS requires HTTPS routes", appName)
	}

	if replaceDomains {
		baseDomain := strings.TrimPrefix(wildcard, "*.")
		if app.Domain == "" || strings.HasSuffix(app.Domain, "."+baseDomain) {
			app.Domain = baseDomain
		}
		app.ExtraDomains = []string{wildcard}
	} else {
		app.AddDomain(wildcard)
	}
	containerName := config.AppContainer(appName)
	upstream := fmt.Sprintf("%s:%d", containerName, app.InternalPort)

	spin := ui.NewSpinner(fmt.Sprintf("Binding %s to %s...", wildcard, appName))
	spin.Start()
	err = updateHTTPSRouteAllowingWildcard(caddy, provider, containerName, app.AllDomains(), upstream, routeOptionsForApp(app)...)
	spin.Stop()
	if err != nil {
		return fmt.Errorf("update Caddy route: %w", err)
	}

	st.Apps[appName] = app
	if err := state.Save(exec, st); err != nil {
		return err
	}
	return nil
}
