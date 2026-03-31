# Test License Activation Against Local CMS

## Context

The Neo+ license system has two sides:
1. **Neo CLI** (`internal/license/license.go`) — calls `POST /api/license/activate`, `/validate`, `/deactivate`
2. **Neo CMS** (`/Users/alan/Development/solution-forest/projects/neo-cms`) — Laravel API at `http://neo.test`

The CMS already has 16 passing PHPUnit tests. Now we need to test the **CLI ↔ CMS integration** end-to-end — point the real Neo binary at the local CMS and verify activate/validate/deactivate work.

## Approach: Go Integration Test

Create a Go integration test that calls the real CMS API at `http://neo.test/api/license/`. This tests the actual HTTP wire format (form-encoded POST, JSON response) that the CLI uses.

### Prerequisites

1. A valid license key must exist in the local CMS database
2. The CMS must be running at `http://neo.test`

### Seed a test license in the CMS

Create a Laravel seeder/command to insert a known test license, or seed one via tinker:

```bash
cd /Users/alan/Development/solution-forest/projects/neo-cms
php artisan tinker --execute="
  \App\Models\License::updateOrCreate(
    ['license_key' => 'TEST-NEO-PLUS-0001'],
    ['plan' => 'plus', 'status' => 'active', 'activation_limit' => 2, 'expires_at' => null, 'customer_email' => 'test@neo.dev', 'customer_name' => 'Neo Test']
  );
"
```

### Go test file: `internal/license/license_integration_test.go`

Test cases:
1. **Activate valid key** — POST `/activate` with `TEST-NEO-PLUS-0001` + machine_id → expect `valid: true, plan: "plus"`
2. **Activate same key again (idempotent)** — same request → expect `valid: true` (no error, updates `last_validated_at`)
3. **Validate active key** — POST `/validate` → expect `valid: true, plan: "plus"`
4. **Deactivate** — POST `/deactivate` → expect HTTP 200
5. **Validate after deactivate** — POST `/validate` → should re-register or return valid (validate is lenient)
6. **Activate invalid key** — POST `/activate` with `INVALID-KEY` → expect `valid: false`
7. **Activate expired key** — need a second seeded license with past expiry

Guard with build tag `//go:build integration` so it doesn't run in normal `go test`.

### Test structure

```go
//go:build integration

package license_test

import (
    "encoding/json"
    "net/http"
    "net/url"
    "os"
    "testing"
)

const testAPIBase = "http://neo.test/api/license" // override via NEO_LICENSE_URL

func apiBase() string {
    if v := os.Getenv("NEO_LICENSE_URL"); v != "" {
        return v
    }
    return testAPIBase
}

func postForm(t *testing.T, endpoint string, vals url.Values) map[string]interface{} {
    resp, err := http.PostForm(apiBase()+endpoint, vals)
    // ... decode JSON, return map
}

func TestActivateValidKey(t *testing.T) { ... }
func TestActivateIdempotent(t *testing.T) { ... }
func TestValidateActiveKey(t *testing.T) { ... }
func TestDeactivate(t *testing.T) { ... }
func TestActivateInvalidKey(t *testing.T) { ... }
```

### Also: Quick CLI smoke test via binary

After the Go tests, run the actual `neo` binary with `NEO_LICENSE_URL=http://neo.test/api/license`:

```bash
NEO_LICENSE_URL=http://neo.test/api/license ./bin/neo plus activate TEST-NEO-PLUS-0001
NEO_LICENSE_URL=http://neo.test/api/license ./bin/neo plus status
NEO_LICENSE_URL=http://neo.test/api/license ./bin/neo plus deactivate
```

## Files to Create/Modify

| File | Action |
|------|--------|
| `internal/license/license_integration_test.go` | **New** — Go integration tests against live CMS |
| Neo CMS database | Seed `TEST-NEO-PLUS-0001` license via tinker |

## Verification

1. Seed test license in CMS: `php artisan tinker`
2. Run Go integration tests: `NEO_LICENSE_URL=http://neo.test/api/license go test -tags integration ./internal/license/ -v`
3. Run CLI smoke test: `NEO_LICENSE_URL=http://neo.test/api/license ./bin/neo plus activate TEST-NEO-PLUS-0001`
4. Verify CMS database has activation record: `php artisan tinker --execute="App\Models\LicenseActivation::all()"`
