# Plan: Remove Neo+ Paid Tier — Free License Required for All Users

**Status:** Planned (not started)
**Repos:** `neo` (Go CLI) + `neo-cms` (Laravel 13 backend)
**Date:** 2026-07-07

---

## Goal / Model Shift

Kill the paid tier. Every user must activate a **free** license key to use `neo`.
All features unlocked; parallel uploads capped at 3 for everyone.

| | Old model | New model |
|---|---|---|
| No key | Free tier (limited) | **Blocked** — must activate |
| Paid key | Plus (unlocked) | (grandfathered, still valid) |
| Free key | n/a | **Everything unlocked** |
| Gates | multi-server=1, backups=blocked, parallel=2/5 | none except parallel=3 for all |

**Locked decisions:**
- Key issuance: **in-CLI auto-register** (`neo activate` → email prompt → API issues key).
- Enforcement: **hard-block all ops** without a valid key (allowlist: activate/help/version/upgrade).
- Naming: rename `neo plus` → **`neo license`**; keep `plus` as hidden alias; add top-level `neo activate`.

**Resolved decisions (2026-07-07):**
1. **Device cap: unlimited** — `activation_limit=0` sentinel; patch `License::canActivate()` to treat `0` as unlimited.
2. **`/register`: issue instantly** — email → key immediately, idempotent per email, throttled.
3. **Paid plumbing: keep dormant** — remove checkout routes + pricing UI only; keep webhook handler + `sync-licenses` + LS secrets so existing paid customers keep validating.
4. **Offline first-run: clear error, require network** — first activation must reach `/register`; fail with "network required to activate (one-time)". 3-day offline grace applies only after first activation.

---

## Cross-Repo Sequencing (hard ordering)

1. **neo-cms first** — ship `/register` + free-plan licenses + free device cap. Deploy, verify endpoint live.
2. **neo client next** — ship `Register()` + hard-block + `neo license`. Hard-block is only safe once step 1 is live (else all users lock out).
3. Docs in **both** repos in same PR wave (`neo/site/docs.html` ⇄ neo-cms `resources/views/pages/docs.blade.php` must stay in sync).
4. Marketing copy anytime.

---

# PART 1 — neo (Go CLI)

## B. `internal/license/`

**`features.go`** — gut the tier machinery:
- Delete `FeatureMultiServer`, `FeatureBackup`, `FeatureParallelUploads`, `Feature`, `gate`, `gates`, `Allowed`, `Limit`, `FeatureDescription`, `AllFeatures`.
- Replace parallel uploads with `const MaxParallelUploads = 3`.
- Drop `PlanPlus`/`PlanFree` tier concept (or keep `PlanFree` only as label).

**`license.go`:**
- Add `Register(email) (*Status, error)` → `POST {API}/register` → save key to cfg.
- Add `IsActivated(key) bool` (valid via cache/online). Retire `CurrentPlan`.
- Drop expired-Plus paths (`Expired`, `isExpiredDate`, expiry-plan special case). Free keys = lifetime.
- Keep offline grace (3d) so offline users aren't bricked *after* first activation. **First** activation requires network → fail with "network required to activate (one-time)".
- Repurpose `DevBypassEnabled` / `NEO_DEV_PLUS` as "skip activation" for dev + test harness.

## C. Enforcement — hard-block (`commands/root.go`)

- `PersistentPreRunE`: if not activated and not dev-bypass → refuse: `Activate first (free): neo activate`.
- Allowlist (run without key): `license`, `activate`, `help`, `version`, `upgrade` (consider `config`).
- Detect current command name to exempt the activation path itself.
- Bare `neo` dashboard with no key → route to activation prompt.
- Remove `CheckDaily`/expired banner call (`root.go:37-44`).

## D. Activation flow + rename (`commands/plus.go` → `commands/license.go`)

- Rename `neo plus` → `neo license` (activate/status/deactivate). Keep `plus` as **hidden alias**.
- Add top-level `neo activate` shortcut.
- `neo activate` logic: key given → validate + save; no key → prompt email → `Register(email)` → save.
- Strip paid UI: `upgrade`/`renew`/pricing options, Free-vs-Plus branching, `tuiPlusMenuExpired`. Collapse to activated-vs-not.

## E. Remove upsell + gate call sites

| File | Change |
|---|---|
| `init.go:585-593` | delete `checkServerLimit` + calls at `:62`, `:172` |
| `attach.go:59` | delete `checkServerLimit` call |
| `backup.go:46-50` | delete backup gate |
| `deploy.go:1357-1361` | use `license.MaxParallelUploads` (=3) |
| `root.go:443-468` | delete `printNeoPlusGate` + `printLicenseExpiredBanner` |
| `ui/banner.go:19-27` | `PrintUpgradeHint` → activation hint (or delete) |
| `dashboard.go:287-298, 42-45, 407` | `plusSummary`→licensed/not-activated; menu "Neo+"→"License" |

`runRestore` already ungated — no change.

## F. Docs (neo side)

- `site/docs.html` — remove Neo+/pricing/Free-tier tables, add Activation section (sync with neo-cms).
- `site/index.html` (14 hits), `site/details.html` (6), `site/pricing-options.html` — strip paid messaging.
- `CLAUDE.md` — rewrite "Neo+ Licensing" section → "Licensing (free, required activation)".

## G. Tests (neo side)

- `internal/license/dev_bypass_test.go`, `license_integration_test.go` — rewrite for activated-vs-not; drop plan/expiry.
- Add: parallel=3 constant, activation-required gate, `Register` flow.
- **`internal/sandbox/` + `internal/testinfra/`** — hard-block breaks integration tests. Set `NEO_DEV_PLUS=true` (or activate) in both harnesses.

---

# PART 2 — neo-cms (Laravel 13 backend)

Existing system = LemonSqueezy paid: checkout → webhook `order_created` → `LicenseOrder`(plan plus/team) → user magic-links into `/account`, generates keys (`activation_limit=1`, 1yr expiry). API validates by `machine_id`.

## A1. New `/register` endpoint (core addition)

- `routes/api.php` (inside throttle group): `POST /license/register`.
- New `LicenseController@register`:
  - Validate `email` (+ `machine_id`).
  - `User::firstOrCreate` by email (reuse webhook pattern).
  - `LicenseOrder::firstOrCreate` `plan='free'` — **idempotent: one free order per user**.
  - Reuse-or-create free `License` → return `{valid, plan, expires, error}` (same shape as activate). Optionally register `machine_id` in same call (one round-trip activation).
  - Repeat calls (same email) return existing key — no duplicate spam.

## A2. License model + migration

- New free licenses: `plan='free'`, `expires_at=NULL` (lifetime), `status='active'`, `activation_limit=0`.
- **Unlimited device cap:** patch `License::canActivate()` to return `true` when `activation_limit <= 0`.
- No destructive migration; existing paid rows untouched.
- Note: client "unlimited multi-server" = neo *servers managed*, NOT device activations — separate axis, no backend change.

## A3. LicenseController — minimal

- `activate`/`validate`/`deactivate` already work for any active license regardless of plan.
- `successResponse` returns `plan`+`expires`; client ignores plan now (only `valid` matters).
- Patch `canActivate()` for unlimited (`activation_limit <= 0`).

## A4. Abuse controls on `/register`

- **Issue instantly** — no email verification. Key returned in the response.
- Idempotent one-free-license-per-email caps blast radius.
- Add stricter throttle (per-IP + per-email) beyond group `throttle:30,1`.
- Log registrations for admin review.

## A5. Stop selling + grandfather

- **Keep webhook handler** (`HandleLemonSqueezyOrderCreated`) so existing paid/subscription customers keep validating.
- Remove/hide new purchase paths: `routes/web.php:42-43` checkout routes, `MarketingController@checkoutPersonal/checkoutTeam`, LS variant config. Redirect old links → `/account` or activation docs.
- **Keep `neo:sync-licenses` + webhook handler + LS secrets dormant** (grandfather existing paid). Remove entry points only, not the plumbing.

## A6. Account page

- `/account` magic-link flow stays as "recover/view my free key."
- `generateLicenseKey` — set `plan='free'`, `expires_at=null` for new free orders (currently `plus`+1yr).

## A7. Docs + marketing copy (neo-cms side)

- `resources/views/pages/docs.blade.php` — sync with `neo/site/docs.html`; remove paid/tier, add Activation.
- `docs/licensing.md` — rewrite: free, required activation.
- Marketing views: `pages/home.blade.php`, `components/marketing-pricing.blade.php`, `marketing-faq.blade.php`, `marketing-features.blade.php`, `marketing-nav.blade.php`, `welcome.blade.php` — strip pricing/$/Buy/Upgrade → "free, activate to use."
- `mail/purchase-welcome.blade.php` → repurpose "welcome / here's your free key" or drop.
- Admin `pages/admin/licenses` — fine as-is; optional plan filter.

## A8. Tests (neo-cms side)

- Feature test for `/register` (new email issues key; repeat returns same; throttle).
- Update activate/validate tests for `plan='free'` + unlimited cap if chosen.
- `SeedFakeLicensesCommand` / factories — add free-plan variant.

## A9. Config/env

- If retiring paid: keep LS keys/secrets (grandfather) — stop new checkouts only.

---

## Top Risks

1. **Backend dependency** — client hard-block needs `/register` live first. Ship neo-cms → neo.
2. **Existing no-key users** auto-blocked on upgrade — email auto-register must be frictionless.
3. **`/register` spam** — open email→key surface; mitigated by idempotency (one key/email) + throttle.
4. **Offline first-run** — network required for first activation; fails with clear one-time message (accepted).

## Decisions — RESOLVED (2026-07-07)

| # | Decision | Resolution |
|---|---|---|
| 1 | Free device (activation) cap | **Unlimited** (`activation_limit=0`, `canActivate()` treats `<=0` as unlimited) |
| 2 | `/register` flow | **Issue instantly**, idempotent per email, throttled, no email-verify |
| 3 | Paid plumbing | **Keep dormant** — remove checkout + pricing UI only; keep webhook/sync/secrets |
| 4 | Client offline first-run | **Clear error, require network** for first activation; 3-day grace after |
