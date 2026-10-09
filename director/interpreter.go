package director

import (
	"context"

	"github.com/manish96170/videofactory/domain"
)

// ShotHint is one piece of LLM-suggested structure for a prompt: a
// text segment and the Category it should become. A PromptInterpreter
// may propose these instead of the deterministic sentence-splitting
// FixtureDirector falls back to; FixtureDirector still owns turning a
// hint into an actual domain.Shot (duration estimation, ID assignment,
// primitive selection, validation) — the interpreter only suggests
// structure, it never produces a Shot/Primitive directly.
type ShotHint struct {
	Text     string
	Category domain.ShotCategory
}

// PromptInterpretation is a PromptInterpreter's suggested structure
// for a prompt.
type PromptInterpretation struct {
	// SuggestedFlowID is empty if the interpreter has no opinion;
	// FixtureDirector falls back to its own heuristic/the request's
	// override in that case.
	SuggestedFlowID domain.ID
	// Shots is empty if the interpreter has no opinion; FixtureDirector
	// falls back to sentence-splitting the prompt itself in that case.
	Shots []ShotHint
}

// PromptInterpreter is the one seam a Director may use for
// LLM-assisted planning. PHASES.md Phase 05: "Do not tightly couple
// the Director to Claude, Qwen, Gemini, or any single model" — any
// Director wanting LLM help must go through this interface, never call
// a specific vendor's API directly.
//
// No implementation of this interface exists in this module yet
// (model-backed planning is later-phase work behind this interface;
// the Decision Engine classifies rather than implementing it).
// FixtureDirector works correctly with none configured at all — see decompose.go's
// deterministic heuristics.
type PromptInterpreter interface {
	Interpret(ctx context.Context, prompt string) (PromptInterpretation, error)
}
