// Package decision defines the Decision Engine's provider contract and
// the pure threshold policy described in PHASES.md section 8.
//
// This package does not implement provider selection, fallback,
// escalation, or a registry: that is Phase 06 (Decision Engine) scope.
// Phase 00 only establishes the stable contract every future decision
// provider and caller depends on.
package decision

import (
	"context"
	"fmt"
	"math"

	"github.com/manish96170/videofactory/domain"
)

// Provider classifies a domain.DecisionRequest into a structured
// domain.DecisionResult. Concrete providers (JEV, local Qwen/Gemma,
// Claude Haiku, Gemini Flash, ...) are introduced behind this interface
// in later phases.
type Provider interface {
	ID() string
	Decide(ctx context.Context, req domain.DecisionRequest) (domain.DecisionResult, error)
}

// Policy is the pure, configurable threshold policy from PHASES.md
// section 8. It turns a continuous score into a domain.DecisionVerdict
// and makes no provider/network/model calls itself.
//
// A valid Policy requires 0 <= NoThreshold <= YesThreshold <= 1; callers
// that build a Policy from external configuration should call Validate
// before using it, since Evaluate itself does not reject an invalid or
// inverted configuration (see DECISIONS.md ADR for the resolved finding).
type Policy struct {
	YesThreshold float64
	NoThreshold  float64
}

// DefaultPolicy mirrors the example thresholds in PHASES.md section 8:
// score >= 0.85 -> YES, score <= 0.30 -> NO, otherwise -> UNCERTAIN.
func DefaultPolicy() Policy {
	return Policy{YesThreshold: 0.85, NoThreshold: 0.30}
}

// Validate reports whether the policy's thresholds are well-formed:
// finite and satisfying 0 <= NoThreshold <= YesThreshold <= 1. A
// zero-value Policy is invalid (NoThreshold 0 <= YesThreshold 0 holds,
// but a caller almost certainly meant to configure real thresholds), so
// callers should always validate policies sourced from configuration.
func (p Policy) Validate() error {
	if math.IsNaN(p.YesThreshold) || math.IsInf(p.YesThreshold, 0) {
		return fmt.Errorf("decision: YesThreshold must be finite, got %v", p.YesThreshold)
	}
	if math.IsNaN(p.NoThreshold) || math.IsInf(p.NoThreshold, 0) {
		return fmt.Errorf("decision: NoThreshold must be finite, got %v", p.NoThreshold)
	}
	if p.NoThreshold < 0 || p.NoThreshold > 1 {
		return fmt.Errorf("decision: NoThreshold must be within [0,1], got %v", p.NoThreshold)
	}
	if p.YesThreshold < 0 || p.YesThreshold > 1 {
		return fmt.Errorf("decision: YesThreshold must be within [0,1], got %v", p.YesThreshold)
	}
	if p.NoThreshold > p.YesThreshold {
		return fmt.Errorf("decision: NoThreshold (%v) must be <= YesThreshold (%v)", p.NoThreshold, p.YesThreshold)
	}
	return nil
}

// Evaluate maps a score to a verdict using the policy's thresholds. It
// does not itself validate the policy (see Validate); passing an invalid
// policy produces an unspecified but deterministic verdict.
func (p Policy) Evaluate(score float64) domain.DecisionVerdict {
	switch {
	case score >= p.YesThreshold:
		return domain.DecisionYes
	case score <= p.NoThreshold:
		return domain.DecisionNo
	default:
		return domain.DecisionUncertain
	}
}
