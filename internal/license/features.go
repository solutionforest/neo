package license

// Feature identifies a gated capability.
type Feature string

const (
	FeatureMultiServer Feature = "multi_server"
	FeatureBackup      Feature = "backup"
)

// Plan represents a subscription tier.
const (
	PlanFree = "free"
	PlanPlus = "plus"
)

// MaxActivations is the maximum number of devices per license key.
const MaxActivations = 2

// gate defines limits for a single feature across plans.
type gate struct {
	Description string
	FreeLimit   int // 0 = blocked, -1 = unlimited, N = count limit
	PlusLimit   int // -1 = unlimited
}

// gates is the single source of truth for what Neo+ unlocks.
var gates = map[Feature]gate{
	FeatureMultiServer: {"Servers", 1, -1},   // Free: 1 server, Plus: unlimited
	FeatureBackup:      {"Backups", 0, -1},   // Free: blocked, Plus: unlimited
}

// Allowed returns true if the feature is permitted given the current plan and usage count.
func Allowed(f Feature, plan string, currentCount int) bool {
	g, ok := gates[f]
	if !ok {
		return true // unknown feature = unrestricted
	}
	limit := g.FreeLimit
	if plan == PlanPlus {
		limit = g.PlusLimit
	}
	if limit == -1 {
		return true
	}
	if limit == 0 {
		return false
	}
	return currentCount < limit
}

// Limit returns the usage limit for a feature and plan. -1 means unlimited, 0 means blocked.
func Limit(f Feature, plan string) int {
	g, ok := gates[f]
	if !ok {
		return -1
	}
	if plan == PlanPlus {
		return g.PlusLimit
	}
	return g.FreeLimit
}

// FeatureDescription returns a human-readable description for a feature.
func FeatureDescription(f Feature) string {
	if g, ok := gates[f]; ok {
		return g.Description
	}
	return string(f)
}

// AllFeatures returns all gated features for display purposes.
func AllFeatures() []Feature {
	return []Feature{FeatureMultiServer, FeatureBackup}
}
