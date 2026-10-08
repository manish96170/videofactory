package domain

import "time"

// TimelineEntry places a Shot on the project timeline.
//
// Start and End are project-timeline-relative (not shot-relative) and
// serialize as integer nanoseconds (encoding/json's default for
// time.Duration). The interval is end-exclusive: [Start, End).
// See DECISIONS.md ADR-007.
type TimelineEntry struct {
	ShotID ID            `json:"shotId"`
	Start  time.Duration `json:"start"`
	End    time.Duration `json:"end"`
}

// Timeline is the ordered composition of shots for a project, plus the
// visual elements the renderer produced for them.
//
// The rendered MP4 is a preview artifact; this Timeline (and the Project
// it belongs to) is the source of truth (PHASES.md section 4.6.1).
type Timeline struct {
	ID        ID              `json:"id"`
	ProjectID ID              `json:"projectId"`
	Entries   []TimelineEntry `json:"entries"`
	Elements  []VisualElement `json:"elements,omitempty"`
}
