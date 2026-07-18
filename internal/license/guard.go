package license

import "time"

// ActivationState is a coarse, machine-facing activation status shared by the
// CLI activation gate and the desktop bridge's `bridge.hello`. It intentionally
// carries NO license key or personal data — the key is never exposed to the
// webview (plan "Licensing"). Values mirror the `activation` union in
// apps/desktop/src/lib/protocol.ts.
type ActivationState string

const (
	// ActivationActive: a valid license, confirmed online recently or via a
	// fresh cache. neo may run.
	ActivationActive ActivationState = "active"
	// ActivationGrace: a valid cached license being trusted offline within the
	// OfflineGraceDays window. neo may run, but the user should reconnect.
	ActivationGrace ActivationState = "grace"
	// ActivationInactive: no valid license. neo is gated.
	ActivationInactive ActivationState = "inactive"
	// ActivationUnknown: activation could not be determined (e.g. config could
	// not be read). Callers decide how strict to be.
	ActivationUnknown ActivationState = "unknown"
)

// graceThreshold is how old a cached (online) validation may be before we
// report ActivationGrace rather than ActivationActive. It is well within
// OfflineGraceDays so "grace" is a soft nudge, not a hard cutoff.
const graceThreshold = 24 * time.Hour

// Activation reports the activation state for a license key WITHOUT performing
// network I/O — it consults only the local offline cache and the dev-bypass
// switch. This makes it safe to call on hot paths like `bridge.hello`, which
// must answer quickly and must never block on the license server. A hard
// online re-check still happens through Check / IsActivated on the CLI gate.
//
// This is the single reusable guard the plan calls for: the CLI and the bridge
// both derive their activation decision from the same rules here.
func Activation(licenseKey string) ActivationState {
	if DevBypassEnabled() {
		return ActivationActive
	}
	if licenseKey == "" {
		return ActivationInactive
	}

	cached := loadCache()
	if cached == nil || !cached.Valid {
		return ActivationInactive
	}
	// A cache produced against a different license server must not vouch for
	// this environment (mirrors Check's same-server guard).
	if cached.ValidatedBy != "" && cached.ValidatedBy != LicenseAPIURL() {
		return ActivationInactive
	}

	t, err := time.Parse(time.RFC3339, cached.ValidatedAt)
	if err != nil {
		// Valid cache with an unparseable timestamp: trust it conservatively as
		// grace rather than claiming a fresh online confirmation.
		return ActivationGrace
	}
	age := time.Since(t)
	if age >= time.Duration(OfflineGraceDays)*24*time.Hour {
		return ActivationInactive // cache expired past the offline grace window
	}
	if age >= graceThreshold {
		return ActivationGrace
	}
	return ActivationActive
}

// Allowed reports whether neo may run given a coarse ActivationState.
func (s ActivationState) Allowed() bool {
	return s == ActivationActive || s == ActivationGrace
}
