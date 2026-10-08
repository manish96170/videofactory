// Package provider defines the Provider contract (PHASES.md section
// 4.1/4.2): core code depends only on this interface, never on a
// specific vendor (InvokeAI, Veo, Runway, a particular LLM, ...).
//
// Concrete providers are introduced in Phase 07 (router/registry),
// Phase 14 (local providers), and Phase 15 (cloud providers).
package provider

import (
	"context"

	"github.com/manish96170/videofactory/domain"
)

// Provider is implemented by anything that can report capabilities for
// routing decisions.
type Provider interface {
	ID() string
	Capabilities(ctx context.Context) (domain.ProviderCapabilities, error)
}
