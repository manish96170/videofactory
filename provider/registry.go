package provider

import (
	"context"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/manish96170/videofactory/domain"
)

// Snapshot is serializable declaration metadata, never an adapter reference.
// ProviderID is in Capabilities; all nested metadata is owned by the recipient.
type Snapshot struct {
	Capabilities domain.ProviderCapabilities `json:"capabilities"`
	Health       domain.ProviderHealth       `json:"health"`
}

type entry struct {
	adapter  Provider
	snapshot Snapshot
}

// Registry is concurrency-safe and zero-value usable. Do not copy after use.
// It has no built-ins, polling, expiry, replacement, or removal. Discovery runs
// outside locks, with caller-controlled deadlines. Concurrent valid refreshes
// are last-publication-wins (completion at the publish lock); no generation or
// start-order freshness is promised. Refresh never overwrites health updates.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]entry
}

func NewRegistry() *Registry { return &Registry{} }

// Register discovers and validates all metadata before atomic publication.
// New entries start UNKNOWN, including successfully discovered adapters.
func (r *Registry) Register(ctx context.Context, adapter Provider) error {
	if err := ctx.Err(); err != nil {
		return safeError(CodeCanceled, err)
	}
	if nilInterface(adapter) {
		return safeError(CodeInvalidProvider, ErrInvalidProvider)
	}
	id := adapter.ID()
	if err := ctx.Err(); err != nil {
		return safeError(CodeCanceled, err)
	}
	if blank(id) {
		return safeError(CodeInvalidProvider, ErrInvalidProvider)
	}
	caps, err := discover(ctx, adapter, id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return safeError(CodeCanceled, err)
	}
	if _, exists := r.entries[id]; exists {
		return safeError(CodeDuplicate, ErrDuplicateProvider)
	}
	if r.entries == nil {
		r.entries = make(map[string]entry)
	}
	r.entries[id] = entry{adapter: adapter, snapshot: Snapshot{Capabilities: caps, Health: domain.ProviderHealthUnknown}}
	return nil
}

// Refresh changes only capabilities. Every failure leaves prior capabilities
// and health untouched, including ID drift or cancellation after late success.
func (r *Registry) Refresh(ctx context.Context, id string) error {
	adapter, _, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	caps, err := discover(ctx, adapter, id)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return safeError(CodeCanceled, err)
	}
	current := r.entries[id]
	current.snapshot.Capabilities = caps
	r.entries[id] = current
	return nil
}

// SetHealth publishes an explicit state, never calls the adapter, and never
// interprets discovery success as evidence of health.
func (r *Registry) SetHealth(ctx context.Context, id string, state domain.ProviderHealth) error {
	if err := ctx.Err(); err != nil {
		return safeError(CodeCanceled, err)
	}
	if blank(id) || !validHealth(state) {
		return safeError(CodeInvalidRequest, ErrInvalidRequest)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return safeError(CodeCanceled, err)
	}
	current, ok := r.entries[id]
	if !ok {
		return safeError(CodeNotFound, ErrProviderNotFound)
	}
	current.snapshot.Health = state
	r.entries[id] = current
	return nil
}

// Get returns one coherent metadata snapshot and the caller-owned adapter
// reference, without calling it. Returning an adapter is not eligibility.
func (r *Registry) Get(ctx context.Context, id string) (Provider, Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, Snapshot{}, safeError(CodeCanceled, err)
	}
	if blank(id) {
		return nil, Snapshot{}, safeError(CodeInvalidRequest, ErrInvalidRequest)
	}
	r.mu.RLock()
	current, ok := r.entries[id]
	snapshot := cloneSnapshot(current.snapshot)
	r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, Snapshot{}, safeError(CodeCanceled, err)
	}
	if !ok {
		return nil, Snapshot{}, safeError(CodeNotFound, ErrProviderNotFound)
	}
	return current.adapter, snapshot, nil
}

// List captures the entire registry under one read lock and returns independent
// snapshots in lexical ID order. It calls no adapter methods.
func (r *Registry) List(ctx context.Context) ([]Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, safeError(CodeCanceled, err)
	}
	r.mu.RLock()
	result := make([]Snapshot, 0, len(r.entries))
	for _, current := range r.entries {
		result = append(result, cloneSnapshot(current.snapshot))
	}
	r.mu.RUnlock()
	if err := ctx.Err(); err != nil {
		return nil, safeError(CodeCanceled, err)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Capabilities.ProviderID < result[j].Capabilities.ProviderID })
	if err := ctx.Err(); err != nil {
		return nil, safeError(CodeCanceled, err)
	}
	return result, nil
}

func discover(ctx context.Context, adapter Provider, id string) (domain.ProviderCapabilities, error) {
	if err := ctx.Err(); err != nil {
		return domain.ProviderCapabilities{}, safeError(CodeCanceled, err)
	}
	before := adapter.ID()
	if err := ctx.Err(); err != nil {
		return domain.ProviderCapabilities{}, safeError(CodeCanceled, err)
	}
	if before != id {
		return domain.ProviderCapabilities{}, safeError(CodeIdentityMismatch, ErrIdentityMismatch)
	}
	caps, callErr := adapter.Capabilities(ctx)
	// Always verify the adapter's ID, even when discovery failed. Capability
	// payloads accompanying errors are ignored; their ProviderID is authoritative
	// only on success. Raw causes cannot override actual identity drift.
	after := adapter.ID()
	var identityErr error
	if after != id || (callErr == nil && caps.ProviderID != id) {
		identityErr = ErrIdentityMismatch
	}
	if err := ctx.Err(); err != nil {
		return domain.ProviderCapabilities{}, safeError(CodeCanceled, err, identityErr, callErr)
	}
	if identityErr != nil {
		return domain.ProviderCapabilities{}, safeError(CodeIdentityMismatch, identityErr, callErr)
	}
	if callErr != nil {
		return domain.ProviderCapabilities{}, safeError(CodeDiscoveryFailed, ErrInvalidProvider, callErr)
	}
	caps = cloneCapabilities(caps)
	if !validCapabilities(caps) {
		return domain.ProviderCapabilities{}, safeError(CodeInvalidProvider, ErrInvalidProvider)
	}
	if err := ctx.Err(); err != nil {
		return domain.ProviderCapabilities{}, safeError(CodeCanceled, err)
	}
	return caps, nil
}

func validCapabilities(c domain.ProviderCapabilities) bool {
	if blank(c.ProviderID) || !nonnegativeFinite(c.CostPerUnit) ||
		(c.CostUnit != "" && blank(c.CostUnit)) || (c.CostPerUnit != 0 && blank(c.CostUnit)) ||
		(c.EstimatedLatencyMs != nil && *c.EstimatedLatencyMs < 0) ||
		(c.Hardware != nil && !nonnegativeFinite(c.Hardware.MinMemoryGB)) {
		return false
	}
	for _, tag := range c.Tags {
		if blank(tag) {
			return false
		}
	}
	return true
}

func validHealth(h domain.ProviderHealth) bool {
	return h == domain.ProviderHealthUnknown || h == domain.ProviderHealthHealthy ||
		h == domain.ProviderHealthUnhealthy || h == domain.ProviderHealthDisabled
}

func cloneSnapshot(s Snapshot) Snapshot { s.Capabilities = cloneCapabilities(s.Capabilities); return s }

func cloneCapabilities(c domain.ProviderCapabilities) domain.ProviderCapabilities {
	c.Tags = append([]string(nil), c.Tags...)
	if c.Hardware != nil {
		hardware := *c.Hardware
		c.Hardware = &hardware
	}
	if c.EstimatedLatencyMs != nil {
		latency := *c.EstimatedLatencyMs
		c.EstimatedLatencyMs = &latency
	}
	return c
}

func blank(s string) bool              { return strings.TrimSpace(s) == "" }
func nonnegativeFinite(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }

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
