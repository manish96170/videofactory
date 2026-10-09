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

	// DefaultFramingID is this flow's suggested Framing — a sensible
	// default a Director (Phase 05) may use, not a binding rule; empty
	// means "no default, the Director must choose explicitly."
	DefaultFramingID ID `json:"defaultFramingId,omitempty"`

	// RecommendedCategories is an ordered, advisory hint of the kinds
	// of shots this flow typically produces (PHASES.md section 6). It
	// is metadata for a future Director to consult, not enforced by
	// this package or the Phase 04 registry.
	RecommendedCategories []ShotCategory `json:"recommendedCategories,omitempty"`
}

// FramingSlot is one named placement region within a Framing's layout,
// e.g. "main" and "overlay" for picture-in-picture. Bounds uses the
// same normalized [0,1], top-left-origin convention as VisualElement's
// (PHASES.md section 4.6.1 / DECISIONS.md ADR-007).
type FramingSlot struct {
	Name   string `json:"name"`
	Bounds Bounds `json:"bounds"`
}

// Framing describes layout/composition, e.g. "fullscreen" or
// "split-screen" (PHASES.md section 7). Framing is independent of Flow:
// "developer-circle" is one reusable framing, not a hard-coded global
// architecture.
//
// Slots is the framing's actual layout: code that composes content onto
// a Framing should iterate Slots generically (see the registry
// package's AssignSlots) rather than special-casing any FramingID —
// that is what "framing must not be embedded into business logic"
// (PHASES.md Phase 04) means in practice.
type Framing struct {
	ID          ID            `json:"id"`
	Name        string        `json:"name"`
	Description string        `json:"description,omitempty"`
	Slots       []FramingSlot `json:"slots,omitempty"`
}
