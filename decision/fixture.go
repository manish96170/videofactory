package decision

import (
	"context"
	"strings"

	"github.com/manish96170/videofactory/domain"
)

// FixtureProvider is a deterministic per-kind result table, not a learned
// classifier. It ignores Input and never calls a model/network/process. The
// copied scalar table is immutable, so shared concurrent calls are safe.
type FixtureProvider struct {
	id      string
	results map[string]domain.DecisionResult
}

func NewFixtureProvider(id string, results map[string]domain.DecisionResult) (*FixtureProvider, error) {
	if strings.TrimSpace(id) == "" {
		return nil, safeError(OutcomeInvalidProvider, ErrInvalidProvider)
	}
	f := &FixtureProvider{id: id, results: make(map[string]domain.DecisionResult, len(results))}
	for kind, result := range results {
		if strings.TrimSpace(kind) == "" || !validResult(result) {
			return nil, safeError(OutcomeMalformed, ErrMalformedResult)
		}
		if result.Provider != "" && result.Provider != id {
			return nil, safeError(OutcomeIdentityMismatch, ErrIdentityMismatch)
		}
		result.Provider = id
		f.results[kind] = result
	}
	return f, nil
}

func (f *FixtureProvider) ID() string { return f.id }

func (f *FixtureProvider) Capabilities(ctx context.Context) (domain.ProviderCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProviderCapabilities{}, safeError(OutcomeCanceled, err)
	}
	// No billable service is called: USD/decision zero declares that fact, not
	// the machine's electricity cost. An empty hardware declaration has no
	// minimum requirement; latency remains unknown rather than invented.
	return domain.ProviderCapabilities{
		ProviderID: f.id, Tags: []string{"semantic-decision", "deterministic"}, Local: true,
		CostUnit: "USD/decision", Hardware: &domain.HardwareRequirements{},
	}, nil
}

func (f *FixtureProvider) Decide(ctx context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.DecisionResult{}, safeError(OutcomeCanceled, err)
	}
	result, exists := f.results[req.Kind]
	if !exists {
		return domain.DecisionResult{}, safeError(OutcomeUnsupportedKind, ErrUnsupportedKind)
	}
	return result, nil
}

var _ CapabilityAdapter = (*FixtureProvider)(nil)
