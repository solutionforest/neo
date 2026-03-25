# Neo CLI — Security Review

**Date:** 2026-03-21
**Scope:** All Go source files in `neo/` (commands, internal packages), `site/install.sh`, `site/download.php`, `Dockerfile`, `go.mod`

---

## Critical Severity

### 1. Self-Update Mechanism Over Plain HTTP (upgrade.go:17)

The version check and binary download URLs use **unencrypted HTTP**:

```go
const versionURL = "http://demo.solutionforest.net/neo/version.json"
const downloadBaseURL = "http://demo.solutionforest.net/neo/download.php"
```

**Impact:** A network attacker (MITM) can intercept `neo upgrade` or `neo version` and serve a malicious binary. The user would unknowingly replace their CLI with malware. There is no checksum verification, no signature validation, and no TLS.

**Fix:** Switch to HTTPS. Add SHA-256 checksum verification. Consider GPG-signing release binaries and verifying signatures before replacement.

### 2. Install Script Downloads Over Plain HTTP (site/install.sh:10)

```sh
BASE_URL="http://demo.solutionforest.net/neo/download.php"
```

The `curl | sh` installation pattern already has inherent risks, but using HTTP makes it trivially exploitable via MITM. An attacker on the same network can inject arbitrary code.

**Fix:** Use HTTPS. Add checksum verification in the install script.

### 3. SQL Injection in Database Operations (service.go, install.go)

Database credentials and names are interpolated directly into SQL strings executed via `docker exec`:

```go
// service.go:423
createDB := fmt.Sprintf(`mysql -uroot -p'%s' -e "CREATE DATABASE IF NOT EXISTS %s;"`, rootPass, dbName)

// service.go:430
createUser := fmt.Sprintf(`mysql -uroot -p'%s' -e "CREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s'; GRANT ALL PRIVILEGES ON %s.* TO '%s'@'%%'; FLUSH PRIVILEGES;"`,
    rootPass, dbUser, dbPass, dbName, dbUser)
```

While `dbName` and `dbUser` come from `sanitizeName()` (which restricts to `[a-z0-9-]`), the passwords are hex-encoded random values that are safe in practice. However, if `sanitizeName` ever changes or is bypassed, this becomes exploitable. The pattern is inherently fragile.

**Fix:** Use parameterized queries or at minimum validate/escape all interpolated values explicitly. Consider using `--` comments to terminate SQL statements.

---

## High Severity

### 4. Command Injection via Unsanitized User Input (multiple files)

The `Docker.Run()` method constructs shell commands by string concatenation:

```go
// remote/docker.go:97-98
for k, v := range opts.Env {
    args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
}
// ...
return d.exec.Run(strings.Join(args, " "))
```

Environment variable values from user input (`.env` files, `--env` flags, `.neo.yml`) are concatenated into a single shell command string sent via SSH. While `env.go:shellEscape()` is used in `restartWithNewEnv()`, the `Docker.Run()` method used during deploy and install does **not** escape env values.

A malicious `.env` file or `--env` value like `FOO='; rm -rf / #` would be interpreted by the remote shell.

**Similarly affected:**
- `docker.go:48` — `CreateNetwork(name)` — network name unescaped
- `docker.go:56` — `Pull(image)` — image name unescaped
- `docker.go:113` — `Stop(name)` — container name unescaped
- `docker.go:133` — `Rename(oldName, newName)` — names unescaped
- `docker.go:172` — `VolumeSize(name)` — grep injection possible
- `docker.go:190` — `Build(contextDir, dockerfile, tag, w)` — paths unescaped
- `docker.go:206-208` — `CopyVolume(volumeName, hostPath)` — path injection

**Fix:** Shell-escape all user-provided values before interpolating into commands. The `shellEscape()` function from `env.go` should be used consistently, or better yet, pass env vars via a file (`docker run --env-file`) to avoid shell interpretation entirely.

### 5. Host Key Verification Bypass (ssh/executor.go:291-294)

```go
cb, err := knownhosts.New(knownHostsPath)
if err != nil {
    // No known_hosts file — accept any key
    return ssh.InsecureIgnoreHostKey()
}
```

If the `~/.ssh/known_hosts` file doesn't exist or can't be read, **all host keys are silently accepted**. This makes the initial connection vulnerable to MITM attacks. An attacker can impersonate the server and capture SSH credentials or inject commands.

Additionally, the `InsecureHostKey` field on the Executor struct allows complete bypass, which while intended for tests, could be misused.

**Fix:** When `known_hosts` doesn't exist, create it and prompt the user to verify the server's host key fingerprint (like SSH does). Never silently accept unknown keys.

### 6. Auto-Accept of Unknown Host Keys (ssh/executor.go:300-308)

```go
var keyErr *knownhosts.KeyError
if errors.As(err, &keyErr) && len(keyErr.Want) == 0 {
    // Key unknown (new host) — add it and accept
    f, ferr := os.OpenFile(knownHostsPath, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0600)
    if ferr == nil {
        defer f.Close()
        fmt.Fprintf(f, "%s\n", knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key))
    }
    return nil
}
```

New, unknown hosts are silently added to `known_hosts` and accepted without user confirmation. This is a Trust-On-First-Use (TOFU) model without the "trust" part — the user has no opportunity to verify the key.

**Fix:** Display the host key fingerprint and ask the user to confirm before accepting, similar to OpenSSH behavior.

### 7. Sensitive Data Stored in Plaintext on Remote Server (state)

The remote state file `/etc/neo/state.json` stores all environment variables including database passwords, API keys, and secrets in plaintext:

```json
{
    "apps": {
        "ghost": {
            "env": {
                "DATABASE_URL": "mysql://ghost:a1b2c3...@svc-ghost-mysql:3306/ghost",
                "SECRET_KEY_BASE": "..."
            }
        }
    },
    "vxero_token": "..."
}
```

Anyone with read access to `/etc/neo/state.json` on the server can extract all application secrets.

**Fix:** Set restrictive file permissions (0600 root-only). Consider encrypting sensitive values at rest. The Vxero API token is particularly sensitive and should be stored separately.

### 8. Docker Install via Piped Script (remote/docker.go:29)

```go
func (d *Docker) Install() error {
    return d.exec.RunQuiet("curl -fsSL https://get.docker.com | sh")
}
```

While using the official Docker install script, piping `curl | sh` on a remote server doesn't verify the script's integrity. If DNS is compromised, a malicious script could be served.

**Fix:** Download the script, verify its checksum against a known value, then execute it. Or use `apt-get install docker-ce` directly with the official Docker APT repository.

---

## Medium Severity

### 9. Caddy Admin API Listens on All Interfaces Inside Container (caddy.go:58)

```go
caddyfile := "{\n  admin 0.0.0.0:2019\n}\n"
```

The Caddy admin API is configured to listen on `0.0.0.0:2019` **inside the container**. While the Docker port mapping (`127.0.0.1:2019:2019`) restricts external access, this relies on Docker networking correctly isolating the container. If the container joins other networks or if Docker's iptables rules are misconfigured, the admin API could become exposed.

**Fix:** Configure Caddy to listen on `127.0.0.1:2019` even inside the container, as defense in depth.

### 10. Path Traversal in ReadFile/WriteFile (ssh/executor.go:140-145)

```go
func (e *Executor) ReadFile(remotePath string) ([]byte, error) {
    out, err := e.Run(fmt.Sprintf("cat %s", remotePath))
    // ...
}

func (e *Executor) WriteFile(remotePath string, data []byte, mode os.FileMode) error {
    // ...
    return session.Run(fmt.Sprintf("scp -t %s", dir))
}
```

Remote file paths are not validated or sanitized. While the callers currently use hardcoded paths, any future caller passing user-controlled paths could read/write arbitrary files on the server.

**Fix:** Validate that file paths don't contain shell metacharacters. Consider using an allowlist of permitted directories.

### 11. Weak Secret Masking (env.go:136-138)

```go
if looksLikeSecret(key) && len(v) > 8 {
    displayed = v[:4] + strings.Repeat("*", len(v)-8) + v[len(v)-4:]
}
```

The first and last 4 characters of secrets are shown in plaintext. For short secrets or secrets with known prefixes/suffixes, this leaks significant information.

**Fix:** Show at most the first 2 characters and mask the rest entirely, or just show the length.

### 12. Race Condition in Config File Access (config/config.go)

Multiple `neo` processes (e.g., running in different terminals) can read and write `~/.neo/config.json` concurrently without file locking, potentially corrupting the config.

**Fix:** Use file locking (`flock`) when writing config, or use atomic write (write to temp file, then rename).

### 13. Unvalidated Domain Names in Caddy Routes (caddy.go:95-126)

Domain names provided by users are passed directly into Caddy API requests:

```go
route := caddyRoute{
    ID: appID,
    Match: []caddyMatch{
        {Host: []string{domain}},
    },
    // ...
}
```

While the domain is JSON-encoded (which handles most injection), malformed domain values could cause unexpected Caddy behavior or route conflicts.

**Fix:** Validate domain names against RFC 1123 before accepting them.

### 14. Backup File Path Not Validated (backup.go:177-185)

```go
if !exec.FileExists(backupFile) {
    remotePath := fmt.Sprintf("/tmp/neo-restore-%s.tar.gz", appName)
    if err := exec.Upload(backupFile, remotePath); err != nil {
```

The `backupFile` argument from user input is used directly as a file path for both remote file existence checks and local file reads. Combined with the unsanitized `cat` in `ReadFile`, this could be exploited.

**Fix:** Validate and sanitize the backup file path.

### 15. Tar Archive Extraction Without Path Validation (backup.go:188-190)

```go
restoreCmd := fmt.Sprintf(
    "docker run --rm -v %s:/backup.tar.gz:ro%s alpine sh -c 'cd /restore && tar xzf /backup.tar.gz'",
    backupPath, volumeArgs,
)
```

A malicious tar archive could contain files with absolute paths or `../` traversal, potentially writing outside the intended restore directory.

**Fix:** Use `tar --strip-components=1` and validate archive contents before extraction.

### 16. XSS in Welcome Page (caddy.go:262)

```go
`<code>` + serverIP + `</code><span onclick="...navigator.clipboard.writeText('` + serverIP + `')...`
```

The `serverIP` is interpolated directly into HTML and JavaScript without escaping. If `serverIP` somehow contains HTML/JS (unlikely for an IP but possible if hostname resolution is used in the future), this is an XSS vector.

**Fix:** HTML-encode all dynamic values in the welcome page template.

---

## Low Severity

### 17. Config Directory Permissions (config/config.go:61)

```go
if err := os.MkdirAll(Dir(), 0o700); err != nil {
```

Good: `~/.neo/` directory is created with 0700. The config file itself is written with 0600. However, `state.json` permissions on the remote server are not explicitly set — they depend on the SCP default/umask.

### 18. No Rate Limiting on Password Auth Attempts (init.go)

When SSH key auth fails, the CLI prompts for a password and retries. There's no limit on how many times this can be attempted, though this is bounded by user interaction.

### 19. SSH Agent Connection Not Closed (ssh/executor.go:247-248)

```go
if conn, err := net.Dial("unix", sock); err == nil {
    methods = append(methods, ssh.PublicKeysCallback(agent.NewClient(conn).Signers))
}
```

The connection to the SSH agent is never closed, leaking a file descriptor for the lifetime of the executor.

**Fix:** Store the connection and close it in `Executor.Close()`.

### 20. Dependency: golang.org/x/crypto v0.31.0

This version should be checked against known CVEs. As of the review date, newer versions may include security patches. The `go.mod` specifies Go 1.23 minimum, which is current.

**Fix:** Run `go list -m -versions golang.org/x/crypto` and update to the latest patch version.

### 21. No TLS Certificate Pinning for Version Check

Even after switching to HTTPS (Critical #1), the version check and download should pin certificates or use a known CA to prevent attacks using rogue certificates.

### 22. Download PHP File Lacks Access Logging (site/download.php)

The download script serves binaries but doesn't log access, making it impossible to detect if an attacker is serving modified binaries.

---

## Summary by Severity

| Severity | Count | Key Issues |
|----------|-------|------------|
| **Critical** | 3 | HTTP self-update, HTTP install script, SQL injection pattern |
| **High** | 5 | Command injection, host key bypass, plaintext secrets, piped install |
| **Medium** | 8 | Caddy admin binding, path traversal, weak masking, race conditions |
| **Low** | 6 | Permission defaults, connection leaks, dependency versions |

## Priority Recommendations

1. **Immediate:** Switch all download/update URLs to HTTPS and add checksum verification
2. **Immediate:** Shell-escape all user input in SSH commands, especially in `Docker.Run()` env var handling
3. **High:** Implement proper host key verification with user confirmation
4. **High:** Add input validation for domain names, file paths, and container names
5. **Medium:** Encrypt sensitive values in state.json or use restrictive file permissions
6. **Medium:** Add file locking for config file writes
7. **Low:** Update dependencies, fix resource leaks, improve secret masking
