package decision

import (
	"context"
	"reflect"
	"strings"
	"sync"

	"github.com/manish96170/videofactory/provider"
)

// Resolver is the selection seam for a future provider registry/router. It
// resolves explicit nonsecret provider handles, not model names or resources.
// Implementations must cooperate with ctx and be safe for concurrent callers.
// They own capability admission; Registry enforces it for decision-local use.
type Resolver interface {
	Resolve(ctx context.Context, id string) (Provider, error)
}

// CapabilityAdapter implements both existing decision and capability contracts.
// ID must be stable and match Capabilities.ProviderID. Lifecycle and concurrent
// safety of the referenced adapter remain the caller's responsibility.
type CapabilityAdapter interface {
	Provider
	provider.Provider
}

// Registry admits semantic-decision adapters only. Its zero value is usable.
// There are no built-ins, overrides, cost/resource retention, or adapter calls
// under the map lock. Resolve returns caller-owned provider references.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry constructs an empty registry; no providers are implicitly added.
func NewRegistry() *Registry { return &Registry{} }

// Register checks identity/capability admission before atomically inserting the
// reference. Existing IDs are never replaced, even by the same adapter.
func (r *Registry) Register(ctx context.Context, adapter CapabilityAdapter) error {
	if err := ctx.Err(); err != nil {
		return safeError(OutcomeCanceled, err)
	}
	if nilInterface(adapter) {
		return safeError(OutcomeInvalidProvider, ErrInvalidProvider)
	}
	id := adapter.ID()
	if strings.TrimSpace(id) == "" {
		return safeError(OutcomeInvalidProvider, ErrInvalidProvider)
	}
	caps, err := adapter.Capabilities(ctx)
	if parentErr := ctx.Err(); parentErr != nil {
		return safeError(OutcomeCanceled, parentErr, err)
	}
	if err != nil {
		return safeError(OutcomeInvalidProvider, ErrInvalidProvider, err)
	}
	if adapter.ID() != id || caps.ProviderID != id {
		return safeError(OutcomeIdentityMismatch, ErrIdentityMismatch)
	}
	if !caps.Supports("semantic-decision") {
		return safeError(OutcomeInvalidProvider, ErrInvalidProvider)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return safeError(OutcomeCanceled, err)
	}
	if _, exists := r.providers[id]; exists {
		return safeError(OutcomeDuplicate, ErrDuplicateProvider)
	}
	if r.providers == nil {
		r.providers = make(map[string]Provider)
	}
	r.providers[id] = adapter
	return nil
}

// Resolve returns a registered reference without calling any adapter methods.
func (r *Registry) Resolve(ctx context.Context, id string) (Provider, error) {
	if err := ctx.Err(); err != nil {
		return nil, safeError(OutcomeCanceled, err)
	}
	r.mu.RLock()
	p := r.providers[id]
	r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, safeError(OutcomeCanceled, err)
	}
	if p == nil {
		return nil, safeError(OutcomeNotFound, ErrProviderNotFound)
	}
	return p, nil
}

var _ Resolver = (*Registry)(nil)

func nilInterface(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
