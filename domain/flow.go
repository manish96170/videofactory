package domain

// Flow describes what type of video is being produced, e.g.
// "developer-explainer" or "cinematic-product-demo" (PHASES.md section 7).
//
// Flows are registered by name/ID in a registry (Phase 04); this package
// deliberately does not hard-code any specific flow as a Go type.
type Flow struct {
	ID          ID     `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Framing describes layout/composition, e.g. "fullscreen" or
// "split-screen" (PHASES.md section 7). Framing is independent of Flow:
// "developer-circle" is one reusable framing, not a hard-coded global
// architecture.
type Framing struct {
	ID          ID     `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}
