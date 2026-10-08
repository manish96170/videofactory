package domain

import "time"

// Bounds is an optional spatial region for a VisualElement, used when the
// element occupies a rectangular area in the frame. Coordinates are
// normalized to [0,1] relative to the full output frame, with the origin
// (0,0) at the top-left. See DECISIONS.md ADR-007.
type Bounds struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// VisualElement is stable, inspectable metadata about something the
// renderer produced, mapping it back to authoritative project state
// (PHASES.md section 4.6.1). It is metadata about the render, not a
// second source of truth.
//
// Start and End are relative to the start of the shot named by ShotID
// (0 = start of that shot's rendered output), matching the per-shot
// RenderManifest convention (e.g. shot-017.mp4 / shot-017.render.json).
// To get a project-relative timestamp, add the matching
// TimelineEntry.Start from the project's Timeline. The interval is
// end-exclusive: [Start, End). Durations serialize as integer
// nanoseconds (encoding/json's default for time.Duration). See
// DECISIONS.md ADR-007.
type VisualElement struct {
	ID                 ID             `json:"id"`
	ShotID             ID             `json:"shotId"`
	Type               string         `json:"type"`
	Start              time.Duration  `json:"start"`
	End                time.Duration  `json:"end"`
	Bounds             *Bounds        `json:"bounds,omitempty"`
	EditableProperties map[string]any `json:"editableProperties,omitempty"`
}

// RenderManifest is the sidecar manifest mapping a rendered shot's output
// media to its VisualElements, per the PHASES.md section 4.6.1 example
// (shot-017.mp4 / shot-017.render.json). The exact storage format may
// change; this struct is the in-memory contract.
type RenderManifest struct {
	ID        ID              `json:"id"`
	ShotID    ID              `json:"shotId"`
	MediaPath string          `json:"mediaPath"`
	Elements  []VisualElement `json:"elements"`
	CreatedAt time.Time       `json:"createdAt"`
}
