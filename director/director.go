// Package director implements Phase 05's Director: turning a user
// prompt into a structured domain.VideoPlan (PHASES.md
// "PHASE 05 — DIRECTOR / VIDEO PLANNING").
//
// The Director plans; it does not render (PHASES.md section 1's
// pipeline: Prompt -> Director -> Video Plan -> ... -> Final Render).
// It must not be tightly coupled to any single AI model/vendor
// (PHASES.md Phase 05 "Important" — "Do not tightly couple the
// Director to Claude, Qwen, Gemini, or any single model"): see
// PromptInterpreter for the one, optional seam a smarter Director
// implementation may use for LLM-assisted planning, instead of
// calling a specific provider's API directly.
package director

import (
	"context"
	"time"

	"github.com/manish96170/videofactory/domain"
)

// Director converts a prompt into a structured VideoPlan for an
// existing project.
type Director interface {
	Plan(ctx context.Context, req PlanRequest) (PlanResult, error)
}

// PlanRequest asks a Director to plan a prompt for an existing
// project (PHASES.md Phase 05 "planning request").
type PlanRequest struct {
	// ProjectID must name an existing project; the Director plans a
	// VideoPlan for it, it does not create the Project itself
	// (that's Phase 01's project.New).
	ProjectID domain.ID
	Prompt    string

	// FlowID/FramingID let a caller pin a specific Flow/Framing
	// instead of letting the Director choose. Empty means "Director
	// decides" (PHASES.md Phase 05 "flow selection" / "framing
	// selection").
	FlowID    domain.ID
	FramingID domain.ID
}

// AssetRequirement names something a planned Shot needs that this
// Director does not itself produce — e.g. an AI-generated avatar clip
// (PHASES.md Phase 05 "asset requirements"). Later phases (Shot
// Intelligence/Phase 09, Provider Router/Phase 07, local/cloud
// providers/Phase 14-15) are expected to resolve these; the Director's
// job is to name them, not fulfill them.
type AssetRequirement struct {
	ShotID      domain.ID `json:"shotId"`
	Kind        string    `json:"kind"`
	Description string    `json:"description"`
}

// PlanMetadata records what produced a PlanResult and from what input
// (PHASES.md Phase 05 "prompt/version metadata").
type PlanMetadata struct {
	Prompt          string    `json:"prompt"`
	DirectorID      string    `json:"directorId"`
	DirectorVersion string    `json:"directorVersion"`
	CreatedAt       time.Time `json:"createdAt"`
}

// PlanResult is a Director's structured output (PHASES.md Phase 05
// "planning result").
type PlanResult struct {
	Plan     domain.VideoPlan
	Metadata PlanMetadata

	// AssetRequirements and RequiredCapabilities are PHASES.md Phase
	// 05's "asset requirements" / "provider capability requirements"
	// TODO items: forward-looking metadata for later phases
	// (Phase 07/09/14/15) to resolve, not resolved here. The Director
	// decides *what* is needed, never *how* to fulfill it with a
	// specific provider.
	AssetRequirements    []AssetRequirement
	RequiredCapabilities []string
}
