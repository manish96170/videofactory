package domain

import "time"

// Project is the root persisted entity for a single Video Factory project.
type Project struct {
	ID        ID         `json:"id"`
	Name      string     `json:"name"`
	Plan      *VideoPlan `json:"plan,omitempty"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// VideoPlan is the Director's structured output: a prompt decomposed into
// shots under a chosen Flow and Framing (PHASES.md section 1/7).
//
// FlowID and FramingID reference registry entries (see Flow and Framing)
// by ID rather than embedding them, so a plan never goes stale relative to
// the registry.
type VideoPlan struct {
	ID        ID        `json:"id"`
	ProjectID ID        `json:"projectId"`
	FlowID    ID        `json:"flowId"`
	FramingID ID        `json:"framingId"`
	Shots     []Shot    `json:"shots"`
	CreatedAt time.Time `json:"createdAt"`
}
