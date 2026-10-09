package decision

import (
	"context"
	"errors"

	"github.com/manish96170/videofactory/provider"
)

// ProviderResolver bridges the generic metadata router to the existing decision
// Resolver. It never routes/substitutes implicitly: pass Route.EligibleIDs
// explicitly to DefaultConfig (the engine's existing eight-provider cap applies).
// Each exact resolution rechecks current admission and requires a real Decide
// implementation, not merely a semantic-decision declaration. No engine policy,
// adapter lifecycle, or attempt timeout is changed.
type ProviderResolver struct {
	router *provider.Router
	policy provider.RouteRequest
}

// NewProviderResolver copies the complete policy, including nested facts and
// limits, and always adds semantic-decision to ALL-required-tags admission.
// An empty RequiredTags policy is allowed here: semantic-decision is mandatory.
func NewProviderResolver(router *provider.Router, policy provider.RouteRequest) (*ProviderResolver, error) {
	if router == nil {
		return nil, safeError(OutcomeInvalidConfig, ErrInvalidConfig)
	}
	policy = policy.Clone()
	found := false
	for _, tag := range policy.RequiredTags {
		if tag == "semantic-decision" {
			found = true
		}
	}
	if !found {
		policy.RequiredTags = append(policy.RequiredTags, "semantic-decision")
	}
	if err := policy.Validate(); err != nil {
		return nil, safeError(OutcomeInvalidConfig, ErrInvalidConfig, err)
	}
	return &ProviderResolver{router: router, policy: policy}, nil
}

func (r *ProviderResolver) Resolve(ctx context.Context, id string) (Provider, error) {
	if err := ctx.Err(); err != nil {
		return nil, safeError(OutcomeCanceled, err)
	}
	if r == nil || r.router == nil {
		return nil, safeError(OutcomeInvalidProvider, ErrInvalidProvider)
	}
	adapter, err := r.router.Resolve(ctx, id, r.policy)
	if err != nil {
		return nil, providerResolutionError(err)
	}
	p, ok := adapter.(Provider)
	if !ok || nilInterface(p) {
		return nil, safeError(OutcomeInvalidProvider, ErrInvalidProvider)
	}
	actualID := p.ID()
	if err := ctx.Err(); err != nil {
		return nil, safeError(OutcomeCanceled, err)
	}
	if actualID != id {
		return nil, safeError(OutcomeIdentityMismatch, ErrIdentityMismatch)
	}
	return p, nil
}

// Permanent admission/provenance sentinels precede any embedded transient cause
// in Phase 06 failureOutcome. Preserve original causes without logging raw text.
func providerResolutionError(err error) error {
	switch {
	case errors.Is(err, provider.ErrIdentityMismatch):
		return safeError(OutcomeIdentityMismatch, ErrIdentityMismatch, err)
	case errors.Is(err, provider.ErrProviderNotFound):
		return safeError(OutcomeNotFound, ErrProviderNotFound, err)
	case errors.Is(err, provider.ErrInvalidProvider), errors.Is(err, provider.ErrIneligible),
		errors.Is(err, provider.ErrInvalidRequest), errors.Is(err, provider.ErrInvalidConfig), errors.Is(err, provider.ErrNoEligibleProviders):
		return safeError(OutcomeInvalidProvider, ErrInvalidProvider, err)
	default:
		return safeError(OutcomeProviderFailure, err)
	}
}

var _ Resolver = (*ProviderResolver)(nil)
