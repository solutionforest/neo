package remote

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/vxero/neo/internal/ssh"
)

const (
	CaddyContainer = "neo-caddy"
	CaddyImage     = "caddy:2-alpine"
	CaddyNetwork   = "neo"
	CaddyAdminURL  = "http://localhost:2019"
)

// Caddy wraps SSH-based Caddy Admin API operations.
type Caddy struct {
	exec *ssh.Executor
}

// NewCaddy creates a Caddy remote executor.
func NewCaddy(exec *ssh.Executor) *Caddy {
	return &Caddy{exec: exec}
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
	c.exec.RunQuiet("mkdir -p /etc/neo/caddy")
	if err := c.exec.WriteFile("/etc/neo/caddy/Caddyfile", []byte(caddyfile), 0644); err != nil {
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
func (c *Caddy) AddRoute(appID string, domains []string, upstream string) error {
	if len(domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	// Ensure the HTTPS server exists (create if missing, no-op if present)
	ensure := fmt.Sprintf(
		`curl -sf %s/config/apps/http/servers/srv0 >/dev/null 2>&1 || curl -sf -X PUT %s/config/apps/http/servers/srv0 -H 'Content-Type: application/json' -d '{"listen":[":443",":80"],"routes":[]}'`,
		CaddyAdminURL, CaddyAdminURL,
	)
	c.exec.RunQuiet(ensure)

	route := caddyRoute{
		ID:     appID,
		Match:  []caddyMatch{{Host: domains}},
		Handle: []caddyHandle{{Handler: "reverse_proxy", Upstreams: []caddyUpstream{{Dial: upstream}}}},
	}

	data, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("marshal route: %w", err)
	}

	cmd := fmt.Sprintf(
		`curl -sf -X POST %s/config/apps/http/servers/srv0/routes -H "Content-Type: application/json" -d '%s'`,
		CaddyAdminURL, string(data),
	)
	return c.exec.RunQuiet(cmd)
}

// RemoveRoute removes a route by its ID.
func (c *Caddy) RemoveRoute(appID string) error {
	cmd := fmt.Sprintf("curl -sf -X DELETE %s/id/%s", CaddyAdminURL, ssh.ShellQuote(appID))
	return c.exec.RunQuiet(cmd)
}

// UpdateRoute replaces an existing route's domains and upstream (HTTPS).
func (c *Caddy) UpdateRoute(appID string, domains []string, upstream string) error {
	c.RemoveRoute(appID) // ignore error if doesn't exist
	c.removeFromAutoHTTPSSkip(domains)
	return c.AddRoute(appID, domains, upstream)
}

// AddRouteHTTP adds an HTTP-only reverse proxy route (no auto-SSL).
// Routes are added to srv0 with automatic_https.skip to prevent cert
// provisioning and HTTP→HTTPS redirects for these domains.
func (c *Caddy) AddRouteHTTP(appID string, domains []string, upstream string) error {
	if len(domains) == 0 {
		return fmt.Errorf("at least one domain is required")
	}
	if err := c.addToAutoHTTPSSkip(domains); err != nil {
		return fmt.Errorf("disable auto-https: %w", err)
	}
	return c.AddRoute(appID, domains, upstream)
}

// UpdateRouteHTTP replaces an existing route with an HTTP-only route.
func (c *Caddy) UpdateRouteHTTP(appID string, domains []string, upstream string) error {
	c.RemoveRoute(appID)
	if err := c.addToAutoHTTPSSkip(domains); err != nil {
		return fmt.Errorf("disable auto-https: %w", err)
	}
	return c.AddRoute(appID, domains, upstream)
}

// addToAutoHTTPSSkip adds domains to srv0's automatic_https.skip list so Caddy
// does not provision certificates or redirect HTTP→HTTPS for them.
func (c *Caddy) addToAutoHTTPSSkip(domains []string) error {
	// Read current skip list (may not exist yet)
	out, _ := c.exec.Run(fmt.Sprintf(
		"curl -sf %s/config/apps/http/servers/srv0/automatic_https/skip 2>/dev/null || echo '[]'",
		CaddyAdminURL,
	))
	var skip []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &skip); err != nil {
		skip = nil
	}

	existing := make(map[string]bool, len(skip))
	for _, d := range skip {
		existing[d] = true
	}
	for _, d := range domains {
		if !existing[d] {
			skip = append(skip, d)
		}
	}

	data, _ := json.Marshal(skip)

	// Ensure the automatic_https object exists, then set the skip list
	c.exec.RunQuiet(fmt.Sprintf(
		`curl -sf %s/config/apps/http/servers/srv0/automatic_https >/dev/null 2>&1 || curl -sf -X PUT %s/config/apps/http/servers/srv0/automatic_https -H 'Content-Type: application/json' -d '{}'`,
		CaddyAdminURL, CaddyAdminURL,
	))
	cmd := fmt.Sprintf(
		`curl -sf -X PUT %s/config/apps/http/servers/srv0/automatic_https/skip -H "Content-Type: application/json" -d '%s'`,
		CaddyAdminURL, string(data),
	)
	return c.exec.RunQuiet(cmd)
}

// removeFromAutoHTTPSSkip removes domains from srv0's automatic_https.skip list
// so Caddy resumes certificate provisioning and HTTPS redirects for them.
func (c *Caddy) removeFromAutoHTTPSSkip(domains []string) {
	out, _ := c.exec.Run(fmt.Sprintf(
		"curl -sf %s/config/apps/http/servers/srv0/automatic_https/skip 2>/dev/null || echo '[]'",
		CaddyAdminURL,
	))
	var skip []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &skip); err != nil || len(skip) == 0 {
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

	if len(newSkip) == 0 {
		c.exec.RunQuiet(fmt.Sprintf(
			"curl -sf -X DELETE %s/config/apps/http/servers/srv0/automatic_https/skip 2>/dev/null || true",
			CaddyAdminURL,
		))
		return
	}

	data, _ := json.Marshal(newSkip)
	c.exec.RunQuiet(fmt.Sprintf(
		`curl -sf -X PUT %s/config/apps/http/servers/srv0/automatic_https/skip -H "Content-Type: application/json" -d '%s'`,
		CaddyAdminURL, string(data),
	))
}

// PatchUpstream atomically updates the upstream dial address for an existing route
// without removing and re-adding it, preventing the brief routing gap of UpdateRoute.
func (c *Caddy) PatchUpstream(appID string, dial string) error {
	dialJSON, err := json.Marshal(dial)
	if err != nil {
		return err
	}
	cmd := fmt.Sprintf(
		`curl -sf -X PATCH %s/id/%s/handle/0/upstreams/0/dial -H "Content-Type: application/json" -d '%s'`,
		CaddyAdminURL, appID, string(dialJSON),
	)
	return c.exec.RunQuiet(cmd)
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
    <a href="https://vxero.dev/neo" target="_blank">Vxero Neo</a>
    &nbsp;&middot;&nbsp; Docker &nbsp;&middot;&nbsp; Caddy &nbsp;&middot;&nbsp; auto-SSL<br>
    <a href="https://vxero.dev/neo" target="_blank">vxero.dev/neo</a>
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
