package domain

// DecisionVerdict is the structured output of a decision provider.
// PHASES.md section 8 prefers exactly these three verdicts over prose.
type DecisionVerdict string

const (
	DecisionYes       DecisionVerdict = "yes"
	DecisionNo        DecisionVerdict = "no"
	DecisionUncertain DecisionVerdict = "uncertain"
)

// DecisionRequest asks a decision provider to classify something, e.g.
// "can this shot be deterministic?" (PHASES.md section 8).
type DecisionRequest struct {
	ID        ID             `json:"id"`
	Kind      string         `json:"kind"`
	ProjectID ID             `json:"projectId,omitempty"`
	ShotID    ID             `json:"shotId,omitempty"`
	Input     map[string]any `json:"input,omitempty"`
}

// DecisionResult is a structured classification, not prose, matching the
// example in PHASES.md section 8. Score is finite affirmative likelihood in
// [0,1], not confidence in the reported Decision. The decision engine validates
// the provider verdict, then recomputes it from Score and its threshold policy.
// ReasonCode is optional provider metadata, not authority for classification.
type DecisionResult struct {
	Decision   DecisionVerdict `json:"decision"`
	Score      float64         `json:"score"`
	ReasonCode string          `json:"reasonCode,omitempty"`
	Provider   string          `json:"provider,omitempty"`
}
