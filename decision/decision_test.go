package decision

import (
	"context"
	"math"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func TestDefaultPolicyEvaluate(t *testing.T) {
	policy := DefaultPolicy()

	cases := []struct {
		name  string
		score float64
		want  domain.DecisionVerdict
	}{
		{"well above yes threshold", 0.94, domain.DecisionYes},
		{"exactly at yes threshold", 0.85, domain.DecisionYes},
		{"middle of the range", 0.5, domain.DecisionUncertain},
		{"exactly at no threshold", 0.30, domain.DecisionNo},
		{"well below no threshold", 0.05, domain.DecisionNo},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := policy.Evaluate(tc.score)
			if got != tc.want {
				t.Fatalf("Evaluate(%v) = %v, want %v", tc.score, got, tc.want)
			}
		})
	}
}

func TestCustomPolicyThresholds(t *testing.T) {
	policy := Policy{YesThreshold: 0.6, NoThreshold: 0.4}

	if got := policy.Evaluate(0.6); got != domain.DecisionYes {
		t.Fatalf("Evaluate(0.6) = %v, want yes", got)
	}
	if got := policy.Evaluate(0.4); got != domain.DecisionNo {
		t.Fatalf("Evaluate(0.4) = %v, want no", got)
	}
	if got := policy.Evaluate(0.5); got != domain.DecisionUncertain {
		t.Fatalf("Evaluate(0.5) = %v, want uncertain", got)
	}
}

func TestPolicyValidate(t *testing.T) {
	valid := []Policy{
		DefaultPolicy(),
		{YesThreshold: 0.6, NoThreshold: 0.4},
		{YesThreshold: 0.5, NoThreshold: 0.5}, // equal thresholds are allowed
		{YesThreshold: 1, NoThreshold: 0},
	}
	for _, p := range valid {
		if err := p.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", p, err)
		}
	}

	invalid := []Policy{
		{YesThreshold: 0.3, NoThreshold: 0.8},         // inverted
		{YesThreshold: -0.1, NoThreshold: 0},          // out of range
		{YesThreshold: 1.1, NoThreshold: 0},           // out of range
		{YesThreshold: 0.5, NoThreshold: -0.1},        // out of range
		{YesThreshold: math.NaN(), NoThreshold: 0.3},  // non-finite
		{YesThreshold: math.Inf(1), NoThreshold: 0.3}, // non-finite
	}
	for _, p := range invalid {
		if err := p.Validate(); err == nil {
			t.Errorf("Validate(%+v) = nil, want an error", p)
		}
	}
}

func TestZeroValuePolicyIsValidButLikelyUnintended(t *testing.T) {
	// A zero-value Policy (YesThreshold: 0, NoThreshold: 0) passes
	// Validate (0 <= 0 <= 0 <= 1 holds) but evaluates every nonnegative
	// score as "yes", which is almost certainly not what a caller wants.
	// This test documents that Validate checks well-formedness, not
	// caller intent, and callers must set real thresholds explicitly.
	var zero Policy
	if err := zero.Validate(); err != nil {
		t.Fatalf("Validate(zero value) = %v, want nil", err)
	}
	if got := zero.Evaluate(0.5); got != domain.DecisionYes {
		t.Fatalf("Evaluate(0.5) with zero-value policy = %v, want yes", got)
	}
}

// fakeDecisionProvider exists only to prove the Provider interface is
// satisfiable; real decision providers arrive in Phase 06/14/15.
type fakeDecisionProvider struct {
	id     string
	result domain.DecisionResult
}

func (f fakeDecisionProvider) ID() string { return f.id }

func (f fakeDecisionProvider) Decide(ctx context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
	return f.result, nil
}

var _ Provider = fakeDecisionProvider{}

func TestFakeDecisionProvider(t *testing.T) {
	p := fakeDecisionProvider{
		id: "mock",
		result: domain.DecisionResult{
			Decision:   domain.DecisionYes,
			Score:      0.94,
			ReasonCode: "REALISTIC_HUMAN_MOTION",
			Provider:   "mock",
		},
	}

	result, err := p.Decide(context.Background(), domain.DecisionRequest{Kind: "deterministic_possible"})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if result.Decision != domain.DecisionYes {
		t.Fatalf("unexpected decision: %+v", result)
	}
}
