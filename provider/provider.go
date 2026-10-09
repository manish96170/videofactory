// Package provider defines the Provider contract (PHASES.md section
// 4.1/4.2): core code depends only on this interface, never on a
// specific vendor (InvokeAI, Veo, Runway, a particular LLM, ...).
//
// Registry and Router select metadata in Phase 07. Real adapters are introduced
// in Phase 14 (local providers) and Phase 15 (cloud providers).
package provider

import (
	"context"

	"github.com/manish96170/videofactory/domain"
)

// Provider is implemented by anything that can report capabilities for
// routing decisions. ID must be stable, nonblank, fast, and nonblocking: it has
// no context. IDs are nonsecret handles, never credentials or request text.
// Capabilities must cooperate with the caller's context; discovery is synchronous
// with no hidden timeout or detached work. Returned metadata must remain stable
// while copied; no retained/asynchronous mutation during that copy is allowed.
// The caller owns the adapter lifecycle and concurrency safety, including
// concurrent discovery/execution. Registry locks never serialize adapter calls.
type Provider interface {
	ID() string
	Capabilities(ctx context.Context) (domain.ProviderCapabilities, error)
}
