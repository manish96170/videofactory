package domain

// ResourcePlan is the Resource Planner's recommendation for executing a
// unit of work, e.g. a GenerationRequest (PHASES.md section 9).
//
// It is a plan, not an enforcement mechanism: the resource.Manager
// (implemented in Phase 08) is the authority that actually enforces
// budgets. "The LLM must never be the authority enforcing resource
// limits" (PHASES.md section 3).
type ResourcePlan struct {
	ID           ID      `json:"id"`
	TargetID     ID      `json:"targetId"`
	RAMBytes     int64   `json:"ramBytes"`
	CPU          float64 `json:"cpu,omitempty"`
	GPU          float64 `json:"gpu,omitempty"`
	PreferRemote bool    `json:"preferRemote,omitempty"`
	Rationale    string  `json:"rationale,omitempty"`
}
