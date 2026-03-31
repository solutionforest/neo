# Security Vulnerability Audit — Neo CLI

**Date:** 2026-03-30
**Scope:** Full codebase review of `vxero-neo` (Go CLI for remote server management over SSH)

---

## Summary

| Severity | Count |
|----------|-------|
| CRITICAL | 6 |
| HIGH     | 5 |
| MEDIUM   | 6 |
| LOW      | 4 |

---

## CRITICAL Vulnerabilities

### 1. Command Injection via Unquoted Volume Paths in Backup/Restore

**Files:** `commands/backup.go:83-90`, `commands/backup.go:191-197`

Volume mount paths from server state (`vol.Mount`) are concatenated directly into shell commands without any quoting or sanitization:

```go
// backup.go:84-89
for volName, vol := range app.Volumes {
    src := volName
    if vol.Mount != nil {
        src = *vol.Mount  // user-controllable via state file
    }
    volumeArgs += fmt.Sprintf(" -v %s:/backup/%s:ro", src, volName)
}
```

The `volumeArgs` string is then passed directly to `exec.RunQuiet(tarCmd)` at line 99. An attacker who can modify the remote state file (`/etc/neo/state.json`) can inject arbitrary Docker flags or shell commands via crafted volume mount paths (e.g., `$(malicious_command)`).

The same pattern repeats in restore at lines 191-197.

**Impact:** Remote Code Execution on the target server.

**Fix:** Use `ssh.ShellQuote()` for all volume path components.

---

### 2. Command Injection via Unquoted Volume Paths in Container Restart (env.go)

**File:** `commands/env.go:336-341`

```go
for name, vol := range app.Volumes {
    src := name
    if vol.Mount != nil {
        src = *vol.Mount
    }
    volArgs = append(volArgs, fmt.Sprintf("-v %s:%s", src, vol.ContainerPath))
}
```

Neither `src` nor `vol.ContainerPath` are shell-quoted. These values come from persisted app state and are injected into a `docker run` command at line 349. Combined with the unquoted `restart` policy (line 344-347) and container name (line 326), this creates multiple injection vectors.

**Impact:** Remote Code Execution via crafted state data.

**Fix:** Shell-quote all dynamic values in the docker command construction.

---

### 3. SQL Injection in Database User/Database Creation

**File:** `commands/install.go:276-302`, `commands/service.go:350-400`

Database names and usernames derived from app names are interpolated directly into SQL statements without identifier escaping:

```go
dbName := strings.ReplaceAll(sanitizeName(manifest.Name), "-", "_") + "_db"
dbUser := strings.ReplaceAll(sanitizeName(manifest.Name), "-", "_")

createDB := fmt.Sprintf(`mysql ... -e "CREATE DATABASE IF NOT EXISTS %s;"`, ..., dbName)
createUser := fmt.Sprintf(`mysql ... -e "CREATE USER IF NOT EXISTS '%s'@'%%' ..."`, dbUser, ...)
```

While `sanitizeName()` restricts to `[a-z0-9-]`, the replacement of `-` with `_` means the identifier is still unquoted in the SQL. If `sanitizeName` is ever relaxed or bypassed, this becomes a direct SQL injection. More critically, `safeSQLValue()` only escapes single quotes and backslashes — it does **not** handle SQL identifiers (backtick-escaping for MySQL, double-quote for PostgreSQL).

The PostgreSQL variant (line 298-301) is worse — identifiers are completely unquoted:
```go
createUser := fmt.Sprintf(`psql -U postgres -c "CREATE USER %s WITH PASSWORD '%s';"`, dbUser, ...)
```

**Impact:** SQL injection leading to privilege escalation within the database.

**Fix:** Use backtick-quoted identifiers for MySQL, double-quoted identifiers for PostgreSQL.

---

### 4. Shell Injection via Caddy JSON Route Data

**File:** `internal/remote/caddy.go:197-201`

```go
cmd := fmt.Sprintf(
    `curl ... -d '%s'`,
    CaddyAdminURL, string(data),  // JSON data embedded in single-quoted shell string
)
c.exec.RunQuiet(cmd)
```

The JSON route data is embedded inside single quotes in a shell command. If the serialized JSON contains a single quote (possible via domain names or route IDs in edge cases), this breaks out of the quoted string and enables shell command injection.

**Impact:** Remote Code Execution on the server via crafted route configuration.

**Fix:** Use `ssh.ShellQuote()` for the JSON data, or write to a temp file and use `curl -d @file`.

---

### 5. Self-Update Without Mandatory Checksum Verification

**File:** `commands/upgrade.go:116-131`

```go
if latest.Checksums != nil {
    if expectedHash, ok := latest.Checksums[platform]; ok {
        // ... verify
    }
}
// If Checksums is nil or platform not found, skip verification entirely
```

Checksum verification is **optional** — it only runs if the version manifest includes checksums for the current platform. If the version endpoint is compromised or MITMed (it uses plain `http.Get` with no certificate pinning), an attacker can:
1. Serve a malicious binary via the download URL
2. Omit the `checksums` field from `version.json`
3. The binary is installed without any integrity verification

Additionally, `downloadBinary()` (line 170) uses `http.Get()` with Go's default HTTP client — no TLS pinning, no additional verification.

**Impact:** Complete compromise of any machine running `neo upgrade`.

**Fix:** Make checksum verification mandatory; fail if no checksum is available. Consider code signing.

---

### 6. Unquoted Shell Arguments in Backup File Operations

**File:** `commands/backup.go:115, 139`

```go
size, _ := exec.Run(fmt.Sprintf("du -h %s | cut -f1", backupFile))
err = exec.Stream(fmt.Sprintf("cat %s", backupFile), f)
```

`backupFile` contains `appName` which, while going through state lookup, is not shell-quoted in these commands. If an app name contains shell metacharacters that survive the state lookup, this enables command injection.

The restore path (line 152) only checks for `..` but not shell metacharacters:
```go
if strings.Contains(backupFile, "..") {  // insufficient — doesn't prevent $(cmd), backticks, pipes
```

**Impact:** Command injection via crafted backup file paths.

---

## HIGH Vulnerabilities

### 7. SSH Host Key Verification Bypass

**File:** `internal/ssh/executor.go:25, 52-54`

```go
InsecureHostKey bool   // skip host key verification (for tests)
...
if e.InsecureHostKey {
    hkCallback = ssh.InsecureIgnoreHostKey()
}
```

A public `InsecureHostKey` field on the `Executor` struct completely disables SSH host key verification. While intended for tests, this is a production-accessible field that enables MITM attacks if accidentally or maliciously set.

**Impact:** Full MITM of SSH connections — credential theft, command interception.

**Fix:** Use build tags to restrict this to test builds only.

---

### 8. curl | sh Installation Pattern

**Files:** `internal/bridge/connect.go:14`, `internal/bridge/migrate.go:145`, `internal/remote/docker.go:30`

```go
installCmd := fmt.Sprintf("curl -fsSL %s | sh", config.AgentInstallURL())
```

Downloads and executes scripts directly via pipe to shell. Vulnerable to:
- DNS hijacking
- MITM (depending on TLS configuration of curl on target)
- Compromised upstream server

**Impact:** Arbitrary code execution as root on the target server.

---

### 9. Health Check Parameters Not Validated/Quoted

**File:** `commands/env.go:360-371`

```go
if app.Health.Interval != "" {
    cmd += fmt.Sprintf(" --health-interval %s", app.Health.Interval)
}
if app.Health.Timeout != "" {
    cmd += fmt.Sprintf(" --health-timeout %s", app.Health.Timeout)
}
if app.Health.StartPeriod != "" {
    cmd += fmt.Sprintf(" --health-start-period %s", app.Health.StartPeriod)
}
```

Health check interval, timeout, and start period values from app state are concatenated into docker commands without quoting or validation. These are string values that could contain shell metacharacters.

**Impact:** Command injection via crafted health check configuration in state file.

---

### 10. Database Passwords Exposed in Process Arguments

**File:** `commands/db.go:159-272`

```go
remoteCmd = fmt.Sprintf("docker exec -it %s mysql --user=%s --password=%s --database=%s",
    ..., db.Password, ...)
```

Database passwords are passed as command-line arguments to `docker exec`, making them visible to:
- `ps aux` on the remote server
- `/proc/*/cmdline` reads
- Shell history
- Process accounting logs

**Impact:** Credential exposure to any user on the server.

**Fix:** Use environment variables (`-e MYSQL_PWD=...`) or config files for password passing.

---

### 11. Plaintext Secrets in State File

**File:** `internal/state/state.go:150`

```go
VxeroToken  string `json:"vxero_token,omitempty"`
```

The Vxero authentication token is stored in plaintext JSON at `/etc/neo/state.json`. Database passwords, API keys, and other sensitive values in `app.Env` are also stored unencrypted.

**Impact:** Server compromise leads to token theft and lateral movement to Vxero platform.

---

## MEDIUM Vulnerabilities

### 12. Race Condition in Cache Serialization

**File:** `internal/config/cache.go:91-124`

`SaveCache()` writes to the global `memCache` and disk without consistently holding the mutex lock. It's called from `UpdateServerCache()` which holds the lock, but `SaveCache()` is exported and can be called externally without synchronization.

**Impact:** Cache corruption under concurrent access.

---

### 13. License Validation Bypass via Offline Grace Period

**File:** `internal/license/license.go:19-20, 160-165`

```go
const OfflineGraceDays = 7
```

Blocking network access to the license server allows continued use of Plus features for 7 days after license expiration.

**Impact:** Bypasses the entire licensing/payment model.

---

### 14. Insufficient Path Traversal Protection in Restore

**File:** `commands/backup.go:152`

```go
if strings.Contains(backupFile, "..") {
    return fmt.Errorf("backup file path must not contain '..'")
}
```

Only checks for `..` substring. Does not prevent:
- Absolute paths to sensitive files (`/etc/shadow`)
- Shell metacharacters (`file; rm -rf /`)
- Symlink-based attacks
- URL-encoded traversal

**Impact:** Arbitrary file read when combined with the upload path at line 207.

---

### 15. Basic Auth Passwords in Plaintext Config

**File:** `commands/neoconfig.go:145-149`

```go
type NeoBasicAuth struct {
    User     string   `yaml:"user"`
    Password string   `yaml:"password"`  // plaintext in .neo.yml
}
```

HTTP basic auth credentials stored in plaintext in `.neo.yml`, which is typically committed to version control.

**Impact:** Credential exposure via repository access.

---

### 16. TOCTOU in known_hosts Update

**File:** `internal/ssh/executor.go:377-382`

Between verifying the host key fingerprint with the user and writing to `known_hosts`, concurrent SSH connections could race, potentially corrupting the file.

---

### 17. License API Uses Default HTTP Client

**File:** `internal/license/license.go:60-138`

While the default URL is HTTPS, the `http.PostForm()` calls use Go's default HTTP client with no certificate pinning. The `NEO_LICENSE_URL` environment variable override could point to an HTTP endpoint, enabling MITM of license key transmission.

**Impact:** License key interception.

---

## LOW Vulnerabilities

### 18. Service Passwords Displayed in Terminal

**File:** `commands/service.go:449-462`

Database root and user passwords are printed to the terminal after service creation, visible in terminal scrollback, screenshots, and CI/CD logs.

### 19. Certificate File Permissions Too Permissive

**File:** `commands/domain.go:308`

SSL certificates written with `0644` permissions (world-readable). While certificates are public, best practice is `0600`.

### 20. Worker/Sidecar Names Not Sanitized

**File:** `commands/deploy.go:730, 846, 900`

Worker names (`wName`) and sidecar names (`scName`) from `.neo.yml` are used in container names and volume names without sanitization through `sanitizeName()`.

### 21. License Key Potentially in Error Messages

**File:** `commands/plus.go:98`

Error paths may expose unmasked license keys in error messages that could be logged.

---

## Recommendations (Priority Order)

1. **Immediate:** Shell-quote ALL dynamic values in SSH commands using `ssh.ShellQuote()` — especially in `backup.go`, `env.go`, `caddy.go`
2. **Immediate:** Make checksum verification mandatory in `upgrade.go`
3. **Urgent:** Use parameterized queries or properly escape SQL identifiers in `install.go` and `service.go`
4. **Urgent:** Pass database passwords via environment variables, not command-line arguments
5. **High:** Restrict `InsecureHostKey` to test builds using build tags
6. **High:** Validate and sanitize all health check parameters, volume paths, worker names, and sidecar names before shell interpolation
7. **Medium:** Encrypt sensitive fields in state file, or use a separate secrets store
8. **Medium:** Add proper path validation (not just `..` check) for restore file paths
9. **Medium:** Remove `curl | sh` pattern; use checksummed/signed downloads
10. **Low:** Use `0600` for all certificate files; mask passwords in UI output
