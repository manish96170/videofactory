package domain

import "time"

// ChangeSetStatus tracks a ChangeSet through the review/apply lifecycle
// (PHASES.md section 13, "Review Before Submit").
type ChangeSetStatus string

const (
	ChangeSetProposed ChangeSetStatus = "proposed"
	ChangeSetApproved ChangeSetStatus = "approved"
	ChangeSetApplied  ChangeSetStatus = "applied"
	ChangeSetRejected ChangeSetStatus = "rejected"
)

// Change is a single proposed mutation against a target entity. Op is a
// free-form, provider-agnostic label in Phase 00; concrete mutation
// semantics arrive with Phase 13 (Director Chat / ChangeSets).
type Change struct {
	TargetID ID             `json:"targetId"`
	Op       string         `json:"op"`
	Payload  map[string]any `json:"payload,omitempty"`
}

// ChangeSet is a validated, reviewable set of mutations against
// authoritative project state. Both Director-chat intents and visual
// review feedback become ChangeSets rather than separate mutation paths
// (PHASES.md section 5/13).
type ChangeSet struct {
	ID ID `json:"id"`

	ProjectID ID `json:"projectId"`
	// Source identifies what produced this ChangeSet, e.g.
	// "director-chat" or "review-feedback". It is a free-form label, not a
	// closed enum, so new input surfaces don't require changing this type.
	Source    string          `json:"source"`
	Changes   []Change        `json:"changes"`
	Status    ChangeSetStatus `json:"status"`
	CreatedAt time.Time       `json:"createdAt"`
}
