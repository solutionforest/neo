# Plan: Auto-deploy SSH key during `neo init` with password auth

## Context

When a VPS only has password auth (common with fresh servers from providers), `neo init` prompts for the password and connects fine. But the password isn't stored — so all subsequent connections (dashboard background refresh, `neo status`, etc.) fail with "auth failed" because they try key-only auth.

**Fix:** After `neo init` successfully connects via password, automatically copy the user's SSH public key to the server's `~/.ssh/authorized_keys`. This is what `ssh-copy-id` does.

## Implementation

### File: `commands/init.go`

**After `setupServer` returns successfully**, if the connection used password auth (i.e., `exec.Password != ""`), deploy the public key:

1. Find the user's public key: check `~/.ssh/id_ed25519.pub`, then `~/.ssh/id_rsa.pub`
2. Read the public key file
3. Run on the remote server:
   ```sh
   mkdir -p ~/.ssh && chmod 700 ~/.ssh && \
   echo '<pubkey contents>' >> ~/.ssh/authorized_keys && \
   chmod 600 ~/.ssh/authorized_keys
   ```
4. Print a success message: `SSH key deployed — future connections won't need a password`
5. If no public key file found, print a hint: `Tip: run ssh-keygen to create an SSH key for passwordless access`

**Where to add this logic:**

- In `runInit()` — after `setupServer(exec, cfg, name, host, "")` returns nil, before the function returns
- In the password retry path (line 90-98) — `exec.Password` is already set
- Also in `runInitWithKey()` — no change needed since it uses explicit key auth

**Helper function:** `deploySSHKey(exec *ssh.Executor) error` — encapsulates the logic. Put it in `init.go` since it's only used during init.

```go
func deploySSHKey(exec *ssh.Executor) {
    home, _ := os.UserHomeDir()
    pubKeyFiles := []string{
        filepath.Join(home, ".ssh", "id_ed25519.pub"),
        filepath.Join(home, ".ssh", "id_rsa.pub"),
    }

    var pubKey []byte
    for _, f := range pubKeyFiles {
        if data, err := os.ReadFile(f); err == nil {
            pubKey = data
            break
        }
    }

    if pubKey == nil {
        fmt.Println()
        ui.Info("Tip: run ssh-keygen to create an SSH key for passwordless access")
        return
    }

    // Deploy to server
    key := strings.TrimSpace(string(pubKey))
    cmd := fmt.Sprintf(
        `mkdir -p ~/.ssh && chmod 700 ~/.ssh && grep -qF %s ~/.ssh/authorized_keys 2>/dev/null || echo %s >> ~/.ssh/authorized_keys && chmod 600 ~/.ssh/authorized_keys`,
        ssh.ShellQuote(key), ssh.ShellQuote(key),
    )
    if err := exec.RunQuiet(cmd); err == nil {
        ui.Success("SSH key deployed — future connections won't need a password")
    }
}
```

Key detail: uses `grep -qF` to check if the key already exists before appending (idempotent — safe to re-run `neo init`).

### Call site in `runInit()`:

```go
// After setupServer succeeds:
err = setupServer(exec, cfg, name, host, "")
if err != nil {
    return err
}

// Deploy SSH key if we connected with password
if exec.Password != "" {
    deploySSHKey(exec)
}
return nil
```

### Files to modify:
- `commands/init.go` — add `deploySSHKey()` helper, call it after `setupServer` in both password paths

### No other files need changes
- The SSH executor, config, and dashboard code all already work with key auth
- Once the key is on the server, `connectSSH` and `connectSSHNonInteractive` will use it via ssh-agent or key file

## Verification

1. `make build`
2. Test with a password-only server:
   - `./bin/neo init root@<ip>` — enters password, initializes, deploys key
   - `./bin/neo status` — should connect without password (key auth)
   - Dashboard should show the server as reachable
3. Test idempotency: `./bin/neo init root@<ip>` again — key not duplicated in authorized_keys
4. Test with key already configured: no password prompt, no key deployment message
