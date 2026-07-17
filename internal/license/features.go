package license

// Plan labels returned by the license server. The client no longer gates
// features by plan — every valid (activated) license unlocks everything.
// Kept only as informational labels for status display.
const (
	PlanFree = "free"
)

// MaxActivations is retained for reference; free licenses use an unlimited
// device cap enforced server-side (activation_limit = 0).
const MaxActivations = 0

// MaxParallelUploads is the number of parallel streams used when transferring
// a built image to the server during deploy. Same for every user.
const MaxParallelUploads = 3
