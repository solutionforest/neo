# Fix: Neo Install Script Fails on Windows

## Context

A user on Windows tried two ways to install Neo, both failed:

1. **PowerShell**: `curl -fsSL https://neo-staging.vxero.dev/neo | powershell` — bash script can't run in PowerShell
2. **Git Bash (MINGW32)**: `curl -fsSL https://neo-staging.vxero.dev/neo | sh` — **"Error: unsupported architecture: i686"** because 32-bit Git Bash reports `uname -m` as `i686` even on 64-bit Windows

**No Go code changes needed.** The binary is compiled as `amd64` and `runtime.GOARCH` always reports correctly. This is purely an install script + portal issue.

## Changes

### 1. Fix i686 detection in CMS install script

**File:** [VersionController.php](../neo-cms/app/Http/Controllers/Api/VersionController.php) (lines 87-92)

Add `i686|i386` case that checks the Windows `PROCESSOR_ARCHITECTURE` env var (reports `AMD64` on 64-bit Windows regardless of Git Bash bitness):

```bash
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    arm64|aarch64) ARCH="arm64" ;;
    i686|i386)
        if [ "$OS" = "windows" ] && [ "$PROCESSOR_ARCHITECTURE" = "AMD64" ]; then
            ARCH="amd64"
        else
            echo "Error: 32-bit systems are not supported."; exit 1
        fi
        ;;
    *) echo "Error: unsupported architecture: $ARCH"; exit 1 ;;
esac
```

### 2. Add PowerShell install script endpoint in CMS

**File:** [VersionController.php](../neo-cms/app/Http/Controllers/Api/VersionController.php)

Add new method `installScriptPowerShell()` that returns a PowerShell script:

```powershell
$ErrorActionPreference = "Stop"

# Vxero Neo Installer (PowerShell)

$arch = if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
$url = "{BASE_URL}/windows/$arch"

Write-Host ""
Write-Host "  Vxero Neo Installer"
Write-Host "  ────────────────────"
Write-Host "  OS:   windows"
Write-Host "  Arch: $arch"
Write-Host ""

$installDir = "$env:LOCALAPPDATA\neo"
New-Item -ItemType Directory -Force -Path $installDir | Out-Null

Write-Host "  Downloading neo..."
Invoke-WebRequest -Uri $url -OutFile "$installDir\neo.exe" -UseBasicParsing

# Add to user PATH if not already there
$userPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$userPath;$installDir", "User")
    Write-Host "  Added $installDir to your PATH."
}

Write-Host "  Installed to $installDir\neo.exe"
Write-Host ""
Write-Host "  Done! Restart your terminal, then run 'neo --help' to get started."
Write-Host ""
```

### 3. Add route for PowerShell script

**File:** [routes/web.php](../neo-cms/routes/web.php)

```php
Route::get('/neo/windows', [VersionController::class, 'installScriptPowerShell'])->name('install-script-powershell');
```

**File:** [routes/api.php](../neo-cms/routes/api.php)

```php
Route::get('/neo/windows', [VersionController::class, 'installScriptPowerShell']);
```

### 4. Add `NEO_WINDOWS_ENABLED` feature flag

**File:** [config/services.php](../neo-cms/config/services.php) (line 52, inside `'neo'` array)

```php
'windows_enabled' => env('NEO_WINDOWS_ENABLED', false),
```

Set `NEO_WINDOWS_ENABLED=true` in `.env` to show the Windows install option.

### 5. Update quickstart page to show Windows install option (gated)

**File:** [quickstart.blade.php](../neo-cms/resources/views/pages/quickstart.blade.php) (lines 243-246)

Wrap Windows install command in `@if(config('services.neo.windows_enabled'))`:

```blade
@if(config('services.neo.windows_enabled'))
    <p>Windows (PowerShell):</p>
    <x-copy-command :command="'irm ' . url('/neo/windows') . ' | iex'" />
@endif
```

Apply this pattern at all 3 places where `curl | sh` appears (lines ~91, ~245, ~567).

### 6. Sync static install scripts in neo repo

**Files:** [site/install.sh](site/install.sh), [dist/install.sh](dist/install.sh)

Apply the same i686 fix to the static copies (kept for reference/fallback).

## Files Summary

| File | Change |
|------|--------|
| `neo-cms/.../VersionController.php` | Fix i686, add PowerShell method |
| `neo-cms/routes/web.php` | Add `/neo/windows` route |
| `neo-cms/routes/api.php` | Add `/neo/windows` route |
| `neo-cms/config/services.php` | Add `NEO_WINDOWS_ENABLED` flag |
| `neo-cms/.../quickstart.blade.php` | Show Windows install (gated by flag) |
| `neo/site/install.sh` | Fix i686 detection |
| `neo/dist/install.sh` | Fix i686 detection |

## Verification

1. Test bash script: simulate `ARCH=i686` + `PROCESSOR_ARCHITECTURE=AMD64` in MINGW shell
2. Test PowerShell script: run `irm .../neo/windows | iex` on Windows
3. Verify `/neo/windows` route returns PowerShell script with correct Content-Type
4. Check quickstart page shows both install commands
