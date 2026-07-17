package remote

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/vxero/neo/internal/ssh"
)

const (
	CaddyContainer   = "neo-caddy"
	CaddyImage       = "caddy:2-alpine"
	CaddyNetwork     = "neo"
	CaddyAdminURL    = "http://localhost:2019"
	CaddyDNSEnvFile  = "/etc/neo/secrets/caddy-dns.env"
	caddyDNSBuildDir = "/etc/neo/caddy-dns"
)

// CaddyDNSProvider describes a Caddy DNS challenge plugin.
type CaddyDNSProvider struct {
	Name       string
	Module     string
	TokenEnv   string
	TokenField string
}

// CaddyDNSProviderFor resolves a supported DNS provider name.
func CaddyDNSProviderFor(name string) (CaddyDNSProvider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "cloudflare":
		return CaddyDNSProvider{
			Name:       "cloudflare",
			Module:     "github.com/caddy-dns/cloudflare",
			TokenEnv:   "CLOUDFLARE_API_TOKEN",
			TokenField: "api_token",
		}, nil
	default:
		return CaddyDNSProvider{}, fmt.Errorf("unsupported DNS provider %q (currently supported: cloudflare)", name)
	}
}

// WildcardDomain returns the one-label wildcard domain for a base domain.
func WildcardDomain(baseDomain string) string {
	return "*." + strings.TrimPrefix(strings.TrimSpace(baseDomain), "*.")
}

// Caddy wraps SSH-based Caddy Admin API operations.
type Caddy struct {
	exec *ssh.Executor
}

// NewCaddy creates a Caddy remote executor.
func NewCaddy(exec *ssh.Executor) *Caddy {
	return &Caddy{exec: exec}
}

// BasicAuthConfig configures HTTP basic authentication for a Caddy route.
// Caddy handles auth entirely at the proxy layer — the app container is unaffected.
type BasicAuthConfig struct {
	Username    string   // plaintext username
	Password    string   // plaintext password; bcrypt-hashed before sending to Caddy
	BypassPaths []string // URL paths excluded from auth (e.g. "/api/*", "/webhooks/*")
}

// RouteOptions carries optional configuration for AddRoute / UpdateRoute calls.
type RouteOptions struct {
	BasicAuth      *BasicAuthConfig
	ForwardedProto string // override X-Forwarded-Proto when behind an HTTPS edge proxy
	ForwardedSSL   bool   // set X-Forwarded-Ssl=on when behind an HTTPS edge proxy
}

// caddyRoute represents a Caddy reverse proxy route.
type caddyRoute struct {
	ID     string        `json:"@id"`
	Match  []caddyMatch  `json:"match"`
	Handle []caddyHandle `json:"handle"`
}

type caddyMatch struct {
	Host []string `json:"host"`
}

type caddyHandle struct {
	Handler   string          `json:"handler"`
	Upstreams []caddyUpstream `json:"upstreams"`
}

type caddyUpstream struct {
	Dial string `json:"dial"`
}

// buildRouteJSON builds the Caddy JSON for a route, with optional basic auth.
// When auth is nil the route is a plain reverse_proxy.
// When auth is provided the route uses a subroute with:
//   - bypass paths (if any) routed directly without auth
//   - everything else guarded by http_basic authentication
//
// upstreams may contain one or more dial addresses (e.g. "app-name:8080").
// Multiple upstreams are load-balanced round-robin by Caddy automatically.
func buildRouteJSON(appID string, domains []string, upstreams []string, opts RouteOptions) ([]byte, error) {
	dialList := make([]map[string]string, len(upstreams))
	for i, u := range upstreams {
		dialList[i] = map[string]string{"dial": u}
	}
	reverseProxy := map[string]interface{}{
		"handler":   "reverse_proxy",
		"upstreams": dialList,
	}
	if opts.ForwardedProto != "" || opts.ForwardedSSL {
		set := map[string][]string{}
		if opts.ForwardedProto != "" {
			set["X-Forwarded-Proto"] = []string{opts.ForwardedProto}
			set["X-Forwarded-Scheme"] = []string{opts.ForwardedProto}
			if opts.ForwardedProto == "https" {
				set["X-Forwarded-Port"] = []string{"443"}
			}
		}
		if opts.ForwardedSSL {
			set["X-Forwarded-Ssl"] = []string{"on"}
		}
		reverseProxy["headers"] = map[string]interface{}{
			"request": map[string]interface{}{
				"set": set,
			},
		}
	}

	var handles []interface{}

	if opts.BasicAuth != nil {
		hashBytes, err := bcrypt.GenerateFromPassword([]byte(opts.BasicAuth.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("bcrypt password: %w", err)
		}
		authHandler := map[string]interface{}{
			"handler": "authentication",
			"providers": map[string]interface{}{
				"http_basic": map[string]interface{}{
					"accounts": []map[string]string{
						{"username": opts.BasicAuth.Username, "password": string(hashBytes)},
					},
				},
			},
		}

		var subroutes []interface{}
		if len(opts.BasicAuth.BypassPaths) > 0 {
			// Bypass route: matching paths skip auth entirely
			subroutes = append(subroutes, map[string]interface{}{
				"match":  []map[string]interface{}{{"path": opts.BasicAuth.BypassPaths}},
				"handle": []interface{}{reverseProxy},
			})
		}
		// Catch-all route: requires basic auth
		subroutes = append(subroutes, map[string]interface{}{
			"handle": []interface{}{authHandler, reverseProxy},
		})

		handles = []interface{}{
			map[string]interface{}{
				"handler": "subroute",
				"routes":  subroutes,
			},
		}
	} else {
		handles = []interface{}{reverseProxy}
	}

	route := map[string]interface{}{
		"@id":    appID,
		"match":  []map[string]interface{}{{"host": domains}},
		"handle": handles,
	}
	return json.Marshal(route)
}

// StartContainer starts the Caddy container on the remote server.
func (c *Caddy) StartContainer() error {
	docker := NewDocker(c.exec)

	// Pull caddy image
	if err := docker.Pull(CaddyImage); err != nil {
		return fmt.Errorf("pull caddy: %w", err)
	}

	// Write initial Caddyfile to a persistent path that survives reboots.
	// /tmp is cleared on boot, so we use /etc/neo/caddy/ instead.
	caddyfile := "{\n  admin 0.0.0.0:2019\n}\n"
	if err := c.exec.WriteFileElevated("/etc/neo/caddy/Caddyfile", []byte(caddyfile), 0644); err != nil {
		return fmt.Errorf("write Caddyfile: %w", err)
	}

	// Start container with --resume so saved routes (from autosave.json) are
	// automatically reloaded after every container restart or server reboot.
	_, err := docker.Run(RunOpts{
		Name:    CaddyContainer,
		Image:   CaddyImage,
		Network: CaddyNetwork,
		Restart: "unless-stopped",
		Ports: []string{
			"80:80",
			"443:443",
			"127.0.0.1:2019:2019",
		},
		Volumes: []string{
			"neo-caddy-data:/data",
			"neo-caddy-config:/config",
			"/etc/neo/caddy/Caddyfile:/etc/caddy/Caddyfile",
		},
		Cmd: "caddy run --config /etc/caddy/Caddyfile --resume",
	})
	return err
}

// dnsCaddyImagePrefix marks a custom Caddy image built with a DNS plugin.
const dnsCaddyImagePrefix = "neo-caddy-dns-"

// isDNSCaddyImage reports whether the given image is a custom DNS-enabled Caddy
// build (produced by InstallDNSProvider) rather than the stock caddy:2-alpine.
func isDNSCaddyImage(image string) bool {
	return strings.HasPrefix(image, dnsCaddyImagePrefix)
}

// Update pulls the newest Caddy image (security patches) and recreates the
// neo-caddy container. Routes and certificates survive because the persistent
// data/config volumes and --resume are reused. If the running proxy is a custom
// DNS-enabled build, its image is rebuilt from the stored Dockerfile with a
// fresh base layer instead of pulling caddy:2-alpine. Returns the image used.
func (c *Caddy) Update(w io.Writer) (string, error) {
	docker := NewDocker(c.exec)
	current := docker.ImageOf(CaddyContainer)

	// Custom DNS build (e.g. neo-caddy-dns-cloudflare:latest): rebuild from the
	// stored Dockerfile, pulling a fresh caddy base, and keep the DNS env file.
	if isDNSCaddyImage(current) {
		if err := docker.BuildPull(caddyDNSBuildDir, caddyDNSBuildDir+"/Dockerfile", current, w); err != nil {
			return "", fmt.Errorf("rebuild Caddy DNS image: %w", err)
		}
		var envFiles []string
		if c.exec.FileExists(CaddyDNSEnvFile) {
			envFiles = []string{CaddyDNSEnvFile}
		}
		if err := c.RecreateWithImage(current, envFiles); err != nil {
			return "", fmt.Errorf("recreate Caddy: %w", err)
		}
		return current, nil
	}

	// Plain build: pull the rolling caddy:2-alpine tag.
	if err := docker.PullStream(CaddyImage, w); err != nil {
		return "", fmt.Errorf("pull %s: %w", CaddyImage, err)
	}
	if err := c.RecreateWithImage(CaddyImage, nil); err != nil {
		return "", fmt.Errorf("recreate Caddy: %w", err)
	}
	return CaddyImage, nil
}

// InstallDNSProvider builds a custom Caddy image with the selected DNS plugin and
// stores the DNS API token in a root-only env file on the remote host.
func (c *Caddy) InstallDNSProvider(provider CaddyDNSProvider, token string, w io.Writer) (string, error) {
	if strings.TrimSpace(token) == "" {
		return "", fmt.Errorf("%s is empty", provider.TokenEnv)
	}
	if strings.ContainsAny(token, "\r\n") {
		return "", fmt.Errorf("%s must be a single-line token", provider.TokenEnv)
	}

	dockerfile := fmt.Sprintf(`FROM caddy:2-builder AS builder
RUN xcaddy build --with %s

FROM caddy:2-alpine
COPY --from=builder /usr/bin/caddy /usr/bin/caddy
`, provider.Module)
	if err := c.exec.WriteFileElevated(caddyDNSBuildDir+"/Dockerfile", []byte(dockerfile), 0644); err != nil {
		return "", fmt.Errorf("write Caddy DNS Dockerfile: %w", err)
	}

	envData := fmt.Sprintf("%s=%s\n", provider.TokenEnv, token)
	if err := c.exec.WriteFileElevated(CaddyDNSEnvFile, []byte(envData), 0600); err != nil {
		return "", fmt.Errorf("write Caddy DNS env file: %w", err)
	}

	image := dnsCaddyImagePrefix + provider.Name + ":latest"
	if err := NewDocker(c.exec).Build(caddyDNSBuildDir, caddyDNSBuildDir+"/Dockerfile", image, w); err != nil {
		return "", fmt.Errorf("build Caddy DNS image: %w", err)
	}
	return image, nil
}

// RecreateWithImage recreates the neo-caddy container using the given image while
// preserving Neo's Caddy data/config volumes and routes.
func (c *Caddy) RecreateWithImage(image string, envFiles []string) error {
	docker := NewDocker(c.exec)
	_ = docker.Remove(CaddyContainer)
	_, err := docker.Run(RunOpts{
		Name:    CaddyContainer,
		Image:   image,
		Network: CaddyNetwork,
		Restart: "unless-stopped",
		Ports: []string{
			"80:80",
			"443:443",
			"127.0.0.1:2019:2019",
		},
		EnvFiles: envFiles,
		Volumes: []string{
			"neo-caddy-data:/data",
			"neo-caddy-config:/config",
			"/etc/neo/caddy/Caddyfile:/etc/caddy/Caddyfile",
		},
		Cmd: "caddy run --config /etc/caddy/Caddyfile --resume",
	})
	return err
}

// ConfigureDNSAutomation enables ACME DNS-01 for the base domain and its
// one-label wildcard using the selected provider. The policy is merged into any
// existing TLS automation (keyed by base domain), so independent wildcard trees
// — e.g. *.example.com and *.staging.example.com — coexist instead of one call
// overwriting the other.
func (c *Caddy) ConfigureDNSAutomation(baseDomain string, provider CaddyDNSProvider) error {
	baseDomain = strings.TrimPrefix(strings.TrimSpace(baseDomain), "*.")
	providerConfig := map[string]interface{}{
		"name":              provider.Name,
		provider.TokenField: fmt.Sprintf("{env.%s}", provider.TokenEnv),
	}
	policy := map[string]interface{}{
		"subjects": []string{baseDomain, WildcardDomain(baseDomain)},
		"issuers": []map[string]interface{}{
			{
				"module": "acme",
				"challenges": map[string]interface{}{
					"dns": map[string]interface{}{
						"provider": providerConfig,
					},
				},
			},
		},
	}
	return c.upsertTLSPolicy(baseDomain, policy, nil)
}

// onDemandPolicy builds the automation policy enabling on-demand TLS for a base
// domain and its one-label wildcard.
func onDemandPolicy(baseDomain string) map[string]interface{} {
	return map[string]interface{}{
		"subjects":  []string{baseDomain, WildcardDomain(baseDomain)},
		"on_demand": true,
	}
}

// onDemandPermission builds the automation-level on_demand permission block that
// makes Caddy ask askURL before issuing a cert for a new hostname.
func onDemandPermission(askURL string) map[string]interface{} {
	return map[string]interface{}{
		"permission": map[string]interface{}{
			"module":   "http",
			"endpoint": askURL,
		},
	}
}

func buildOnDemandAutomationJSON(baseDomain, askURL string) ([]byte, error) {
	baseDomain = strings.TrimPrefix(strings.TrimSpace(baseDomain), "*.")
	askURL = strings.TrimSpace(askURL)
	if baseDomain == "" {
		return nil, fmt.Errorf("base domain is required")
	}
	if askURL == "" {
		return nil, fmt.Errorf("ask URL is required")
	}
	automation := map[string]interface{}{
		"policies":  []map[string]interface{}{onDemandPolicy(baseDomain)},
		"on_demand": onDemandPermission(askURL),
	}
	return json.Marshal(automation)
}

// ConfigureOnDemandTLS enables guarded on-demand TLS for the base domain and
// its one-label wildcard. Caddy calls askURL before issuing a certificate for a
// new hostname, so apps can allow only active tenant subdomains. The policy is
// merged into any existing TLS automation (keyed by base domain).
//
// Note: Caddy supports a single automation-level on_demand permission endpoint,
// so the most recently configured askURL applies to all on-demand hostnames. For
// independent wildcard trees on separate apps, prefer DNS-01 (ConfigureDNSAutomation).
func (c *Caddy) ConfigureOnDemandTLS(baseDomain, askURL string) error {
	baseDomain = strings.TrimPrefix(strings.TrimSpace(baseDomain), "*.")
	askURL = strings.TrimSpace(askURL)
	if baseDomain == "" {
		return fmt.Errorf("base domain is required")
	}
	if askURL == "" {
		return fmt.Errorf("ask URL is required")
	}
	return c.upsertTLSPolicy(baseDomain, onDemandPolicy(baseDomain), onDemandPermission(askURL))
}

// loadTLSAutomation fetches Caddy's current tls.automation object, or an empty
// map if none is configured yet.
func (c *Caddy) loadTLSAutomation() map[string]interface{} {
	out, err := c.exec.Run(fmt.Sprintf("curl -sf %s/config/apps/tls/automation 2>/dev/null", CaddyAdminURL))
	trimmed := strings.TrimSpace(out)
	if err != nil || trimmed == "" || trimmed == "null" {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil || m == nil {
		return map[string]interface{}{}
	}
	return m
}

// tlsPolicyManagesBase reports whether an existing automation policy is the one
// managing baseDomain's wildcard tree — its subjects are exactly the base domain
// and its one-label wildcard. Used to upsert policies keyed by base domain.
func tlsPolicyManagesBase(policy map[string]interface{}, base, wildcard string) bool {
	raw, _ := policy["subjects"].([]interface{})
	if len(raw) == 0 {
		return false
	}
	seenBase, seenWildcard := false, false
	for _, v := range raw {
		s, _ := v.(string)
		switch strings.TrimSpace(s) {
		case base:
			seenBase = true
		case wildcard:
			seenWildcard = true
		}
	}
	return seenBase && seenWildcard
}

// upsertTLSPolicy merges newPolicy into Caddy's tls.automation, replacing any
// existing policy that manages baseDomain's wildcard tree while preserving every
// other policy (and any unrelated automation keys). This lets independent
// wildcard trees coexist instead of overwriting one another. When onDemand is
// non-nil it is set as the automation's on_demand permission block; when nil any
// existing on_demand block is left untouched.
func (c *Caddy) upsertTLSPolicy(baseDomain string, newPolicy, onDemand map[string]interface{}) error {
	base := strings.TrimPrefix(strings.TrimSpace(baseDomain), "*.")
	if base == "" {
		return fmt.Errorf("base domain is required")
	}
	wildcard := WildcardDomain(base)

	// Ensure the TLS app exists so the automation path is writable.
	ensureTLS := fmt.Sprintf(
		`curl -sf %s/config/apps/tls >/dev/null 2>&1 || curl -sf -X PUT %s/config/apps/tls -H 'Content-Type: application/json' -d '{}'`,
		CaddyAdminURL, CaddyAdminURL,
	)
	if err := c.exec.RunQuiet(ensureTLS); err != nil {
		return fmt.Errorf("ensure Caddy TLS app: %w", err)
	}

	automation := c.loadTLSAutomation()

	// Keep every policy except the one already managing this base domain.
	var policies []interface{}
	if existing, ok := automation["policies"].([]interface{}); ok {
		for _, p := range existing {
			if pm, ok := p.(map[string]interface{}); ok && tlsPolicyManagesBase(pm, base, wildcard) {
				continue
			}
			policies = append(policies, p)
		}
	}
	policies = append(policies, newPolicy)
	automation["policies"] = policies

	if onDemand != nil {
		automation["on_demand"] = onDemand
	}

	data, err := json.Marshal(automation)
	if err != nil {
		return fmt.Errorf("build TLS automation config: %w", err)
	}
	cmd := fmt.Sprintf(
		`curl -sf -X PUT %s/config/apps/tls/automation -H "Content-Type: application/json" -d %s`,
		CaddyAdminURL, ssh.ShellQuote(string(data)),
	)
	return c.exec.RunQuiet(cmd)
}

// HasOnDemandTLS reports whether Caddy has guarded on-demand TLS configured for
// the base domain's wildcard.
func (c *Caddy) HasOnDemandTLS(baseDomain string) bool {
	out, err := c.exec.Run(fmt.Sprintf("curl -sf %s/config/apps/tls 2>/dev/null", CaddyAdminURL))
	if err != nil || strings.TrimSpace(out) == "" {
		return false
	}
	return tlsConfigHasOnDemandWildcard([]byte(out), baseDomain)
}

func tlsConfigHasOnDemandWildcard(data []byte, baseDomain string) bool {
	baseDomain = strings.TrimPrefix(strings.TrimSpace(baseDomain), "*.")
	if baseDomain == "" {
		return false
	}
	var cfg struct {
		Automation struct {
			OnDemand *struct {
				Permission *struct {
					Module   string `json:"module"`
					Endpoint string `json:"endpoint"`
				} `json:"permission"`
			} `json:"on_demand"`
			Policies []struct {
				OnDemand bool     `json:"on_demand"`
				Subjects []string `json:"subjects"`
			} `json:"policies"`
		} `json:"automation"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return false
	}
	if cfg.Automation.OnDemand == nil || cfg.Automation.OnDemand.Permission == nil {
		return false
	}
	if cfg.Automation.OnDemand.Permission.Module != "http" || strings.TrimSpace(cfg.Automation.OnDemand.Permission.Endpoint) == "" {
		return false
	}

	wildcard := WildcardDomain(baseDomain)
	for _, policy := range cfg.Automation.Policies {
		if !policy.OnDemand {
			continue
		}
		seenBase := false
		seenWildcard := false
		for _, subject := range policy.Subjects {
			switch strings.TrimSpace(subject) {
			case baseDomain:
				seenBase = true
			case wildcard:
				seenWildcard = true
			}
		}
		if seenBase && seenWildcard {
			return true
		}
	}
	return false
}

// HasDNSProvider reports whether the running Caddy binary includes a DNS provider module.
func (c *Caddy) HasDNSProvider(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return false
	}
	out, err := NewDocker(c.exec).Exec(CaddyContainer, "caddy list-modules 2>/dev/null || true")
	if err != nil {
		return false
	}
	return strings.Contains(out, "dns.providers."+provider)
}

// Version returns the Caddy version.
func (c *Caddy) Version() (string, error) {
	docker := NewDocker(c.exec)
	out, err := docker.Exec(CaddyContainer, "caddy version")
	if err != nil {
		return "", err
	}
	return out, nil
}

// AddRoute adds a reverse proxy HTTPS route for an app.
// domains may contain one or more hostnames — Caddy issues a cert for each automatically.
// Pass a RouteOptions with BasicAuth set to enable HTTP basic authentication.
func (c *Caddy) AddRoute(appID string, domains []string, upstream string, opts ...RouteOptions) error {
	if len(domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	// Ensure the HTTPS server exists (create if missing, no-op if present)
	ensure := fmt.Sprintf(
		`curl -sf %s/config/apps/http/servers/srv0 >/dev/null 2>&1 || curl -sf -X PUT %s/config/apps/http/servers/srv0 -H 'Content-Type: application/json' -d '{"listen":[":443",":80"],"routes":[]}'`,
		CaddyAdminURL, CaddyAdminURL,
	)
	c.exec.RunQuiet(ensure)

	var routeOpts RouteOptions
	if len(opts) > 0 {
		routeOpts = opts[0]
	}

	data, err := buildRouteJSON(appID, domains, []string{upstream}, routeOpts)
	if err != nil {
		return fmt.Errorf("build route: %w", err)
	}

	cmd := fmt.Sprintf(
		`curl -sf -X POST %s/config/apps/http/servers/srv0/routes -H "Content-Type: application/json" -d %s`,
		CaddyAdminURL, ssh.ShellQuote(string(data)),
	)
	return c.exec.RunQuiet(cmd)
}

// RemoveRoute removes a route by its ID.
func (c *Caddy) RemoveRoute(appID string) error {
	cmd := fmt.Sprintf("curl -sf -X DELETE %s/id/%s", CaddyAdminURL, ssh.ShellQuote(appID))
	return c.exec.RunQuiet(cmd)
}

// redirectRouteID returns the Caddy route ID for a domain redirect.
func redirectRouteID(fromDomain string) string {
	return "redirect-" + fromDomain
}

// AddRedirect creates a Caddy route that issues an HTTP redirect from fromDomain to toURL.
// code should be 301 (permanent) or 302 (temporary).
// The request path is preserved: fromDomain/blog → toURL/blog.
// Auto-SSL is provisioned for fromDomain by Caddy automatically.
func (c *Caddy) AddRedirect(fromDomain, toURL string, code int) error {
	// Ensure srv0 exists
	ensure := fmt.Sprintf(
		`curl -sf %s/config/apps/http/servers/srv0 >/dev/null 2>&1 || curl -sf -X PUT %s/config/apps/http/servers/srv0 -H 'Content-Type: application/json' -d '{"listen":[":443",":80"],"routes":[]}'`,
		CaddyAdminURL, CaddyAdminURL,
	)
	c.exec.RunQuiet(ensure)

	route := map[string]interface{}{
		"@id":   redirectRouteID(fromDomain),
		"match": []map[string]interface{}{{"host": []string{fromDomain}}},
		"handle": []interface{}{
			map[string]interface{}{
				"handler": "subroute",
				"routes": []interface{}{
					map[string]interface{}{
						"handle": []interface{}{
							map[string]interface{}{
								"handler":     "static_response",
								"status_code": code,
								"headers": map[string]interface{}{
									"Location": []string{toURL + "{http.request.uri}"},
								},
							},
						},
					},
				},
			},
		},
		"terminal": true,
	}

	data, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("build redirect route: %w", err)
	}

	cmd := fmt.Sprintf(
		`curl -sf -X POST %s/config/apps/http/servers/srv0/routes -H "Content-Type: application/json" -d %s`,
		CaddyAdminURL, ssh.ShellQuote(string(data)),
	)
	return c.exec.RunQuiet(cmd)
}

// RemoveRedirect removes a redirect route for the given source domain.
func (c *Caddy) RemoveRedirect(fromDomain string) error {
	cmd := fmt.Sprintf("curl -sf -X DELETE %s/id/%s", CaddyAdminURL, ssh.ShellQuote(redirectRouteID(fromDomain)))
	return c.exec.RunQuiet(cmd)
}

// UpdateRoute replaces an existing route's domains and upstream (HTTPS).
func (c *Caddy) UpdateRoute(appID string, domains []string, upstream string, opts ...RouteOptions) error {
	c.RemoveRoute(appID) // ignore error if doesn't exist
	c.removeFromAutoHTTPSSkip(domains)
	return c.AddRoute(appID, domains, upstream, opts...)
}

// AddRouteHTTP adds an HTTP-only reverse proxy route (no auto-SSL).
// Routes are added to srv0 with automatic_https.skip to prevent cert
// provisioning and HTTP→HTTPS redirects for these domains.
func (c *Caddy) AddRouteHTTP(appID string, domains []string, upstream string, opts ...RouteOptions) error {
	if len(domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	if err := c.addToAutoHTTPSSkip(domains); err != nil {
		return fmt.Errorf("disable auto-https: %w", err)
	}
	return c.AddRoute(appID, domains, upstream, opts...)
}

// UpdateRouteHTTP replaces an existing route with an HTTP-only route.
func (c *Caddy) UpdateRouteHTTP(appID string, domains []string, upstream string, opts ...RouteOptions) error {
	if err := c.addToAutoHTTPSSkip(domains); err != nil {
		return fmt.Errorf("disable auto-https: %w", err)
	}
	c.RemoveRoute(appID)
	return c.AddRoute(appID, domains, upstream, opts...)
}

// addToAutoHTTPSSkip adds domains to srv0's automatic_https.skip list so Caddy
// does not provision certificates or redirect HTTP→HTTPS for them.
func (c *Caddy) addToAutoHTTPSSkip(domains []string) error {
	server, err := c.loadHTTPServerConfig()
	if err != nil {
		return err
	}
	skip := autoHTTPSSkip(server)

	existing := make(map[string]bool, len(skip))
	for _, d := range skip {
		existing[d] = true
	}
	for _, d := range domains {
		if !existing[d] {
			skip = append(skip, d)
		}
	}

	setAutoHTTPSSkip(server, skip)
	return c.saveHTTPServerConfig(server)
}

// removeFromAutoHTTPSSkip removes domains from srv0's automatic_https.skip list
// so Caddy resumes certificate provisioning and HTTPS redirects for them.
func (c *Caddy) removeFromAutoHTTPSSkip(domains []string) {
	server, err := c.loadHTTPServerConfig()
	if err != nil {
		return
	}
	skip := autoHTTPSSkip(server)
	if len(skip) == 0 {
		return
	}

	remove := make(map[string]bool, len(domains))
	for _, d := range domains {
		remove[d] = true
	}
	var newSkip []string
	for _, d := range skip {
		if !remove[d] {
			newSkip = append(newSkip, d)
		}
	}

	setAutoHTTPSSkip(server, newSkip)
	_ = c.saveHTTPServerConfig(server)
}

func (c *Caddy) loadHTTPServerConfig() (map[string]interface{}, error) {
	out, err := c.exec.Run(fmt.Sprintf("curl -sf %s/config/apps/http/servers/srv0 2>/dev/null", CaddyAdminURL))
	if err != nil || strings.TrimSpace(out) == "" {
		return map[string]interface{}{
			"listen": []interface{}{":443", ":80"},
			"routes": []interface{}{},
		}, nil
	}
	var server map[string]interface{}
	if err := json.Unmarshal([]byte(out), &server); err != nil {
		return nil, fmt.Errorf("parse Caddy HTTP server config: %w", err)
	}
	return server, nil
}

func (c *Caddy) saveHTTPServerConfig(server map[string]interface{}) error {
	data, err := json.Marshal(server)
	if err != nil {
		return fmt.Errorf("build Caddy HTTP server config: %w", err)
	}
	cmd := fmt.Sprintf(
		`curl -sf -X PUT %s/config/apps/http/servers/srv0 -H "Content-Type: application/json" -d %s`,
		CaddyAdminURL, ssh.ShellQuote(string(data)),
	)
	return c.exec.RunQuiet(cmd)
}

func autoHTTPSSkip(server map[string]interface{}) []string {
	auto, _ := server["automatic_https"].(map[string]interface{})
	if auto == nil {
		return nil
	}
	raw, _ := auto["skip"].([]interface{})
	result := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && s != "" {
			result = append(result, s)
		}
	}
	return result
}

func setAutoHTTPSSkip(server map[string]interface{}, skip []string) {
	if len(skip) == 0 {
		if auto, ok := server["automatic_https"].(map[string]interface{}); ok {
			delete(auto, "skip")
			if len(auto) == 0 {
				delete(server, "automatic_https")
			}
		}
		return
	}
	auto, _ := server["automatic_https"].(map[string]interface{})
	if auto == nil {
		auto = map[string]interface{}{}
		server["automatic_https"] = auto
	}
	values := make([]interface{}, len(skip))
	for i, domain := range skip {
		values[i] = domain
	}
	auto["skip"] = values
}

// PatchUpstream atomically updates the upstream dial address for an existing route
// without removing and re-adding it, preventing the brief routing gap of UpdateRoute.
func (c *Caddy) PatchUpstream(appID string, dial string) error {
	dialJSON, err := json.Marshal(dial)
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf(
		`curl -sf -X PATCH %s/id/%s/handle/0/upstreams/0/dial -H "Content-Type: application/json" -d %s`,
		CaddyAdminURL, ssh.ShellQuote(appID), ssh.ShellQuote(string(dialJSON)),
	)
	return c.exec.RunQuiet(cmd)
}

// PatchUpstreams atomically replaces the entire upstreams list for an existing route.
// Used for scaled (multi-replica) apps to switch all upstream addresses at once.
// Falls back to UpdateRouteMulti if the PATCH fails (e.g. route has auth subroute structure).
func (c *Caddy) PatchUpstreams(appID string, dials []string, domains []string, httpOnly bool, opts ...RouteOptions) error {
	ups := make([]map[string]string, len(dials))
	for i, d := range dials {
		ups[i] = map[string]string{"dial": d}
	}
	data, err := json.Marshal(ups)
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf(
		`curl -sf -X PUT %s/id/%s/handle/0/upstreams -H "Content-Type: application/json" -d %s`,
		CaddyAdminURL, ssh.ShellQuote(appID), ssh.ShellQuote(string(data)),
	)
	if err := c.exec.RunQuiet(cmd); err != nil {
		// Fallback: full route replacement (e.g. route has basic_auth subroute structure)
		if httpOnly {
			return c.UpdateRouteMultiHTTP(appID, domains, dials, opts...)
		}
		return c.UpdateRouteMulti(appID, domains, dials, opts...)
	}
	return nil
}

// AddRouteMulti adds an HTTPS reverse proxy route with multiple upstreams (load-balanced).
func (c *Caddy) AddRouteMulti(appID string, domains []string, upstreams []string, opts ...RouteOptions) error {
	if len(domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	c.exec.RunQuiet(fmt.Sprintf(
		`curl -sf %s/config/apps/http/servers/srv0 >/dev/null 2>&1 || curl -sf -X PUT %s/config/apps/http/servers/srv0 -H 'Content-Type: application/json' -d '{"listen":[":443",":80"],"routes":[]}'`,
		CaddyAdminURL, CaddyAdminURL,
	))
	var routeOpts RouteOptions
	if len(opts) > 0 {
		routeOpts = opts[0]
	}
	data, err := buildRouteJSON(appID, domains, upstreams, routeOpts)
	if err != nil {
		return fmt.Errorf("build route: %w", err)
	}
	cmd := fmt.Sprintf(
		`curl -sf -X POST %s/config/apps/http/servers/srv0/routes -H "Content-Type: application/json" -d %s`,
		CaddyAdminURL, ssh.ShellQuote(string(data)),
	)
	return c.exec.RunQuiet(cmd)
}

// AddRouteMultiHTTP adds an HTTP-only reverse proxy route with multiple upstreams.
func (c *Caddy) AddRouteMultiHTTP(appID string, domains []string, upstreams []string, opts ...RouteOptions) error {
	if len(domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	if err := c.addToAutoHTTPSSkip(domains); err != nil {
		return fmt.Errorf("disable auto-https: %w", err)
	}
	return c.AddRouteMulti(appID, domains, upstreams, opts...)
}

// UpdateRouteMulti replaces an existing route with a multi-upstream HTTPS route.
func (c *Caddy) UpdateRouteMulti(appID string, domains []string, upstreams []string, opts ...RouteOptions) error {
	c.RemoveRoute(appID)
	c.removeFromAutoHTTPSSkip(domains)
	return c.AddRouteMulti(appID, domains, upstreams, opts...)
}

// UpdateRouteMultiHTTP replaces an existing route with a multi-upstream HTTP-only route.
func (c *Caddy) UpdateRouteMultiHTTP(appID string, domains []string, upstreams []string, opts ...RouteOptions) error {
	c.RemoveRoute(appID)
	if err := c.addToAutoHTTPSSkip(domains); err != nil {
		return fmt.Errorf("disable auto-https: %w", err)
	}
	return c.AddRouteMulti(appID, domains, upstreams, opts...)
}

// LoadCertificate loads a custom TLS certificate and key into Caddy.
// The cert and key files must already exist on the remote server.
func (c *Caddy) LoadCertificate(certPath, keyPath string) error {
	// Copy cert files into Caddy container
	docker := NewDocker(c.exec)
	c.exec.RunQuiet(fmt.Sprintf("docker exec %s mkdir -p /etc/caddy/certs", ssh.ShellQuote(CaddyContainer)))
	if _, err := docker.Exec(CaddyContainer, fmt.Sprintf("sh -c 'cat > /etc/caddy/certs/cert.pem' < %s", ssh.ShellQuote(certPath))); err != nil {
		// Fallback: copy via docker cp
		c.exec.RunQuiet(fmt.Sprintf("docker cp %s %s:/etc/caddy/certs/cert.pem", ssh.ShellQuote(certPath), ssh.ShellQuote(CaddyContainer)))
	}
	if _, err := docker.Exec(CaddyContainer, fmt.Sprintf("sh -c 'cat > /etc/caddy/certs/key.pem' < %s", ssh.ShellQuote(keyPath))); err != nil {
		c.exec.RunQuiet(fmt.Sprintf("docker cp %s %s:/etc/caddy/certs/key.pem", ssh.ShellQuote(keyPath), ssh.ShellQuote(CaddyContainer)))
	}

	// Load certificate via Caddy Admin API
	cmd := fmt.Sprintf(
		`curl -sf -X POST %s/load -H "Content-Type: application/json" -d '{
			"apps": {
				"tls": {
					"certificates": {
						"load_files": [{
							"certificate": "/etc/caddy/certs/cert.pem",
							"key": "/etc/caddy/certs/key.pem"
						}]
					}
				}
			}
		}' 2>/dev/null;
		curl -sf -X PATCH %s/config/apps/tls/certificates/load_files -H "Content-Type: application/json" -d '[{"certificate": "/etc/caddy/certs/cert.pem", "key": "/etc/caddy/certs/key.pem"}]'`,
		CaddyAdminURL, CaddyAdminURL,
	)
	return c.exec.RunQuiet(cmd)
}

// IsRunning checks if the Caddy container is running.
func (c *Caddy) IsRunning() bool {
	docker := NewDocker(c.exec)
	return docker.IsRunning(CaddyContainer)
}

// Exists checks if the Caddy container exists (running or stopped).
func (c *Caddy) Exists() bool {
	out, _ := c.exec.Run(fmt.Sprintf("docker inspect --format '{{.Name}}' %s 2>/dev/null", ssh.ShellQuote(CaddyContainer)))
	return strings.TrimSpace(out) != ""
}

// Start starts an existing stopped Caddy container.
func (c *Caddy) Start() error {
	_, err := c.exec.Run(fmt.Sprintf("docker start %s", ssh.ShellQuote(CaddyContainer)))
	return err
}

// CheckPortConflict returns a warning string if ports 80 or 443 are in use
// by something other than the Caddy container.
func (c *Caddy) CheckPortConflict() string {
	out, _ := c.exec.Run("ss -tlnp 2>/dev/null | grep -E ':80 |:443 ' | grep -v docker || true")
	out = strings.TrimSpace(out)
	if out == "" {
		return ""
	}
	// Detect common servers
	switch {
	case strings.Contains(out, "nginx"):
		return "nginx is using ports 80/443 — run: systemctl stop nginx && systemctl disable nginx"
	case strings.Contains(out, "apache"), strings.Contains(out, "httpd"):
		return "apache is using ports 80/443 — run: systemctl stop apache2 && systemctl disable apache2"
	case out != "":
		return "ports 80/443 are in use by another process — free them before starting Caddy"
	}
	return ""
}

// RemoveWelcomePage removes the branded welcome page for direct IP access.
func (c *Caddy) RemoveWelcomePage() error {
	return c.exec.RunQuiet(fmt.Sprintf("curl -sf -X DELETE %s/id/neo-welcome 2>/dev/null || true", CaddyAdminURL))
}

// AddWelcomePage adds a route for direct IP access showing a branded server-ready page.
func (c *Caddy) AddWelcomePage(serverIP string) error {
	// HTML-escape the server IP to prevent XSS
	safeIP := html.EscapeString(serverIP)
	_ = safeIP

	welcomeHTML := `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<meta name="robots" content="noindex,nofollow">
<title>Vxero Neo · Server Ready</title>
<link rel="preconnect" href="https://fonts.googleapis.com">
<link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500&display=swap" rel="stylesheet">
<style>
:root{--bg:#0a0c10;--surface:#111827;--border:rgba(255,255,255,0.07);--text:#f8fafc;--soft:#94a3b8;--green:#22c55e;--blue:#60a5fa;--lime:#a3e635}
*{box-sizing:border-box;margin:0;padding:0}
body{font-family:-apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;background:var(--bg);color:var(--text);min-height:100vh;display:flex;flex-direction:column;align-items:center;justify-content:center;padding:24px;-webkit-font-smoothing:antialiased}
.card{background:var(--surface);border:1px solid var(--border);border-radius:20px;padding:40px 44px;max-width:520px;width:100%;box-shadow:0 24px 80px rgba(0,0,0,0.5)}
.ascii{font-family:'JetBrains Mono',monospace;font-size:13px;line-height:1.2;color:var(--green);margin-bottom:28px;white-space:pre;letter-spacing:0.02em}
.status{display:flex;align-items:center;gap:10px;margin-bottom:6px}
.dot{width:9px;height:9px;border-radius:50%;background:var(--green);box-shadow:0 0 10px var(--green);animation:pulse 2s infinite}
@keyframes pulse{0%,100%{opacity:1}50%{opacity:.4}}
h1{font-size:1.35rem;font-weight:700}
h1 em{font-style:normal;color:var(--green)}
.sub{color:var(--soft);margin-top:6px;margin-bottom:28px;font-size:.875rem;line-height:1.6}
.label{font-size:.7rem;color:var(--soft);text-transform:uppercase;letter-spacing:.08em;margin-bottom:7px}
.box{background:#0b1017;border:1px solid var(--border);border-radius:10px;padding:11px 16px;font-family:'JetBrains Mono',monospace;font-size:.875rem;margin-bottom:18px;display:flex;align-items:center;justify-content:space-between;gap:8px}
.box.green{color:var(--lime)}
.box.blue{color:var(--blue)}
.box span{opacity:.4;font-size:.75rem;cursor:pointer;user-select:none;transition:opacity .15s,color .15s}
.box span:hover{opacity:.9}
.box span.copied{opacity:1;color:var(--green)}
hr{border:none;border-top:1px solid var(--border);margin:24px 0}
.footer{font-size:.78rem;color:var(--soft);text-align:center;line-height:1.7}
.footer a{color:var(--blue);text-decoration:none}
.footer a:hover{text-decoration:underline}
.badge{display:inline-flex;align-items:center;gap:5px;background:rgba(34,197,94,.1);color:var(--green);border:1px solid rgba(34,197,94,.2);border-radius:999px;padding:3px 10px;font-size:.72rem;font-weight:600;margin-bottom:24px}
</style>
</head>
<body>
<div class="card">
  <pre class="ascii">&#x2588; &#x2588; &#x2580;&#x2584;&#x2580; &#x2588;&#x2580;&#x2580; &#x2588;&#x2580;&#x2588; &#x2588;&#x2580;&#x2588; &#x2503; &#x2588;&#x2584; &#x2588; &#x2588;&#x2580;&#x2580; &#x2588;&#x2580;&#x2588;
&#x2580;&#x2584;&#x2580; &#x2588; &#x2588; &#x2588;&#x2588;&#x2584; &#x2588;&#x2580;&#x2584; &#x2588;&#x2584;&#x2588; &#x2503; &#x2588; &#x2580;&#x2588; &#x2588;&#x2588;&#x2584; &#x2588;&#x2584;&#x2588;</pre>
  <div class="badge">
    <span>&#x2022;</span> Server Initialized
  </div>
  <div class="status">
    <div class="dot"></div>
    <h1>Server <em>Ready</em></h1>
  </div>
  <p class="sub">Your neo server is running. Deploy your first app from your local machine.</p>
  <div class="label">Server IP</div>
  <div class="box green"><code>` + safeIP + `</code><span onclick="var e=this;navigator.clipboard.writeText('` + safeIP + `').then(function(){e.textContent='copied';e.classList.add('copied');setTimeout(function(){e.textContent='copy';e.classList.remove('copied')},1500)})" title="Copy">copy</span></div>
  <div class="label">Deploy from your machine</div>
  <div class="box blue"><code>neo deploy</code><span onclick="var e=this;navigator.clipboard.writeText('neo deploy').then(function(){e.textContent='copied';e.classList.add('copied');setTimeout(function(){e.textContent='copy';e.classList.remove('copied')},1500)})" title="Copy">copy</span></div>
  <hr>
  <div class="footer">
    <a href="https://github.com/solutionforest/neo" target="_blank" rel="noopener">&#x2B50; Star us on GitHub</a><br>
    <a href="https://neo.vxero.dev" target="_blank">Vxero Neo</a>
    &nbsp;&middot;&nbsp; Docker &nbsp;&middot;&nbsp; Caddy &nbsp;&middot;&nbsp; auto-SSL<br>
    <a href="https://neo.vxero.dev" target="_blank">neo.vxero.dev</a>
  </div>
</div>
</body>
</html>`

	route := map[string]interface{}{
		"@id": "neo-welcome",
		"match": []map[string]interface{}{
			{"host": []string{serverIP}},
		},
		"handle": []map[string]interface{}{
			{
				"handler": "static_response",
				"body":    welcomeHTML,
				"headers": map[string][]string{
					"Content-Type": {"text/html; charset=utf-8"},
				},
			},
		},
	}

	data, err := json.Marshal(route)
	if err != nil {
		return err
	}

	// Write payload to a temp file to avoid shell quoting issues with complex HTML
	writeCmd := fmt.Sprintf("cat > /tmp/neo-welcome.json << 'NEOJSON'\n%s\nNEOJSON", string(data))
	c.exec.RunQuiet(writeCmd)

	// Ensure srv0 exists
	ensure := fmt.Sprintf(
		`curl -sf %s/config/apps/http/servers/srv0 >/dev/null 2>&1 || curl -sf -X PUT %s/config/apps/http/servers/srv0 -H 'Content-Type: application/json' -d '{"listen":[":443",":80"],"routes":[]}'`,
		CaddyAdminURL, CaddyAdminURL,
	)
	c.exec.RunQuiet(ensure)

	// Remove old welcome route if exists, then add new
	c.exec.RunQuiet(fmt.Sprintf("curl -sf -X DELETE %s/id/neo-welcome 2>/dev/null || true", CaddyAdminURL))
	cmd := fmt.Sprintf(
		`curl -sf -X POST %s/config/apps/http/servers/srv0/routes -H "Content-Type: application/json" -d @/tmp/neo-welcome.json`,
		CaddyAdminURL,
	)
	return c.exec.RunQuiet(cmd)
}
