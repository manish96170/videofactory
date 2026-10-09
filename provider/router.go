package provider

import (
	"context"
	"sort"

	"github.com/manish96170/videofactory/domain"
)

// Locality is explicit opt-in for remote routing. Zero defaults to local-only.
type Locality string

const (
	LocalOnly   Locality = "local"
	RemoteOnly  Locality = "remote"
	AnyLocality Locality = "any"
)

// CostLimit compares exact units, without conversion. Amount is finite and
// nonnegative; Unit must be nonblank even when the amount is zero.
type CostLimit struct {
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

// HardwareFacts are caller declarations, not detected or reserved resources.
// Nil pointers mean unknown; pointers to zero/false are explicitly known facts.
type HardwareFacts struct {
	AvailableMemoryGB *float64 `json:"availableMemoryGb,omitempty"`
	HasGPU            *bool    `json:"hasGpu,omitempty"`
}

// RouteRequest requires ALL nonblank, exact case-sensitive RequiredTags.
// PreferredIDs are distinct nonblank nonsecret handles: unknown or ineligible
// preferences are ignored, never used to relax a gate. MaxLatencyMs nil means
// no cap. Hardware facts apply only to local providers. Callers must not mutate
// slices/pointers while a request is being copied; each call owns its copy.
type RouteRequest struct {
	RequiredTags []string      `json:"requiredTags"`
	PreferredIDs []string      `json:"preferredIds,omitempty"`
	Locality     Locality      `json:"locality,omitempty"`
	MaxCost      *CostLimit    `json:"maxCost,omitempty"`
	MaxLatencyMs *int64        `json:"maxLatencyMs,omitempty"`
	Hardware     HardwareFacts `json:"hardware"`
}

// Clone copies every slice and nested pointer, for callers retaining a policy.
func (req RouteRequest) Clone() RouteRequest {
	req.RequiredTags = append([]string(nil), req.RequiredTags...)
	req.PreferredIDs = append([]string(nil), req.PreferredIDs...)
	if req.MaxCost != nil {
		cost := *req.MaxCost
		req.MaxCost = &cost
	}
	if req.MaxLatencyMs != nil {
		latency := *req.MaxLatencyMs
		req.MaxLatencyMs = &latency
	}
	if req.Hardware.AvailableMemoryGB != nil {
		memory := *req.Hardware.AvailableMemoryGB
		req.Hardware.AvailableMemoryGB = &memory
	}
	if req.Hardware.HasGPU != nil {
		gpu := *req.Hardware.HasGPU
		req.Hardware.HasGPU = &gpu
	}
	return req
}

// Validate reports only core codes, not raw tags, IDs, or limits.
func (req RouteRequest) Validate() error {
	if len(req.RequiredTags) == 0 || (req.Locality != "" && req.Locality != LocalOnly && req.Locality != RemoteOnly && req.Locality != AnyLocality) ||
		(req.MaxCost != nil && (!nonnegativeFinite(req.MaxCost.Amount) || blank(req.MaxCost.Unit))) ||
		(req.MaxLatencyMs != nil && *req.MaxLatencyMs < 0) ||
		(req.Hardware.AvailableMemoryGB != nil && !nonnegativeFinite(*req.Hardware.AvailableMemoryGB)) {
		return safeError(CodeInvalidRequest, ErrInvalidRequest)
	}
	for _, tag := range req.RequiredTags {
		if blank(tag) {
			return safeError(CodeInvalidRequest, ErrInvalidRequest)
		}
	}
	seen := make(map[string]bool, len(req.PreferredIDs))
	for _, id := range req.PreferredIDs {
		if blank(id) || seen[id] {
			return safeError(CodeInvalidRequest, ErrInvalidRequest)
		}
		seen[id] = true
	}
	return nil
}

// RejectionCode is safe core vocabulary; no raw tag/unit/adapter text is added.
type RejectionCode string

const (
	RejectHealth          RejectionCode = "health_not_healthy"
	RejectTags            RejectionCode = "required_tags_missing"
	RejectLocality        RejectionCode = "locality_not_allowed"
	RejectCostUnknown     RejectionCode = "cost_unknown"
	RejectCostUnit        RejectionCode = "cost_unit_mismatch"
	RejectCostLimit       RejectionCode = "cost_limit_exceeded"
	RejectLatencyUnknown  RejectionCode = "latency_unknown"
	RejectLatencyLimit    RejectionCode = "latency_limit_exceeded"
	RejectHardwareUnknown RejectionCode = "hardware_requirements_unknown"
	RejectMemoryUnknown   RejectionCode = "memory_available_unknown"
	RejectMemoryLimit     RejectionCode = "memory_insufficient"
	RejectGPUUnknown      RejectionCode = "gpu_available_unknown"
	RejectGPUUnavailable  RejectionCode = "gpu_unavailable"
)

// Rejection names only a registered nonsecret handle and core-owned reasons.
type Rejection struct {
	ProviderID string          `json:"providerId"`
	Codes      []RejectionCode `json:"codes"`
}

// RouteResult owns its slices (including nested rejection codes). EligibleIDs
// includes the first choice and every eligible fallback. FallbackIDs is an
// independent copy of EligibleIDs[1:]. Rejections are lexical-ID ordered, and
// codes follow health/tags/locality/cost/latency/hardware order.
type RouteResult struct {
	EligibleIDs []string    `json:"eligibleIds"`
	FallbackIDs []string    `json:"fallbackIds"`
	Rejections  []Rejection `json:"rejections"`
}

// Router uses declaration snapshots only. It performs no discovery, inference,
// retry, health polling, or resource reservation. The registry and adapter
// lifecycles are caller-owned. Construct with NewRouter; concurrent use is safe.
type Router struct{ registry *Registry }

func NewRouter(registry *Registry) (*Router, error) {
	if registry == nil {
		return nil, safeError(CodeInvalidConfig, ErrInvalidConfig)
	}
	return &Router{registry: registry}, nil
}

// Route filters one coherent registry snapshot before ranking: eligible explicit
// preferences first, then deterministic, local nondeterministic, remote, with
// lexical-ID ties. It calls NO adapter methods, including ID. Unknown preferences
// are ignored. No-eligible returns rejection details alongside ErrNoEligibleProviders.
func (r *Router) Route(ctx context.Context, req RouteRequest) (RouteResult, error) {
	if err := ctx.Err(); err != nil {
		return RouteResult{}, safeError(CodeCanceled, err)
	}
	req = req.Clone()
	if err := req.Validate(); err != nil {
		return RouteResult{}, err
	}
	if r == nil || r.registry == nil {
		return RouteResult{}, safeError(CodeInvalidConfig, ErrInvalidConfig)
	}
	snapshots, err := r.registry.List(ctx)
	if err != nil {
		return RouteResult{}, err
	}
	eligible := make([]Snapshot, 0, len(snapshots))
	result := RouteResult{EligibleIDs: []string{}, FallbackIDs: []string{}, Rejections: []Rejection{}}
	for _, snapshot := range snapshots {
		if codes := rejectionCodes(snapshot, req); len(codes) > 0 {
			result.Rejections = append(result.Rejections, Rejection{ProviderID: snapshot.Capabilities.ProviderID, Codes: codes})
		} else {
			eligible = append(eligible, snapshot)
		}
	}
	preferences := make(map[string]int, len(req.PreferredIDs))
	for i, id := range req.PreferredIDs {
		preferences[id] = i
	}
	sort.Slice(eligible, func(i, j int) bool {
		a, b := eligible[i].Capabilities, eligible[j].Capabilities
		pa, preferredA := preferences[a.ProviderID]
		pb, preferredB := preferences[b.ProviderID]
		if preferredA != preferredB {
			return preferredA
		}
		if preferredA {
			return pa < pb
		}
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		return a.ProviderID < b.ProviderID
	})
	for _, s := range eligible {
		result.EligibleIDs = append(result.EligibleIDs, s.Capabilities.ProviderID)
	}
	if len(result.EligibleIDs) > 1 {
		result.FallbackIDs = append(result.FallbackIDs, result.EligibleIDs[1:]...)
	}
	if err := ctx.Err(); err != nil {
		return RouteResult{}, safeError(CodeCanceled, err)
	}
	if len(eligible) == 0 {
		return result, safeError(CodeNoEligible, ErrNoEligibleProviders)
	}
	return result, nil
}

// Resolve checks exactly id against the latest captured metadata and policy. It
// NEVER substitutes a fallback. ID is checked outside registry locks, followed
// by a context check. This is point-in-time admission, not a lease: metadata can
// change after the snapshot and before execution; no reservation is implied.
func (r *Router) Resolve(ctx context.Context, id string, req RouteRequest) (Provider, error) {
	if err := ctx.Err(); err != nil {
		return nil, safeError(CodeCanceled, err)
	}
	req = req.Clone()
	if err := req.Validate(); err != nil {
		return nil, err
	}
	if r == nil || r.registry == nil {
		return nil, safeError(CodeInvalidConfig, ErrInvalidConfig)
	}
	adapter, snapshot, err := r.registry.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	codes := rejectionCodes(snapshot, req)
	if err := ctx.Err(); err != nil {
		return nil, safeError(CodeCanceled, err)
	}
	if len(codes) != 0 {
		return nil, safeError(CodeIneligible, ErrIneligible)
	}
	actualID := adapter.ID()
	if err := ctx.Err(); err != nil {
		return nil, safeError(CodeCanceled, err)
	}
	if actualID != id {
		return nil, safeError(CodeIdentityMismatch, ErrIdentityMismatch)
	}
	return adapter, nil
}

func rank(c domain.ProviderCapabilities) int {
	if c.Supports("deterministic") {
		return 0
	}
	if c.Local {
		return 1
	}
	return 2
}

func rejectionCodes(s Snapshot, req RouteRequest) []RejectionCode {
	c := s.Capabilities
	var codes []RejectionCode
	if s.Health != domain.ProviderHealthHealthy {
		codes = append(codes, RejectHealth)
	}
	for _, tag := range req.RequiredTags {
		if !c.Supports(tag) {
			codes = append(codes, RejectTags)
			break
		}
	}
	if ((req.Locality == "" || req.Locality == LocalOnly) && !c.Local) || (req.Locality == RemoteOnly && c.Local) {
		codes = append(codes, RejectLocality)
	}
	if req.MaxCost != nil {
		switch {
		case c.CostUnit == "":
			codes = append(codes, RejectCostUnknown)
		case c.CostUnit != req.MaxCost.Unit:
			codes = append(codes, RejectCostUnit)
		case c.CostPerUnit > req.MaxCost.Amount:
			codes = append(codes, RejectCostLimit)
		}
	}
	if req.MaxLatencyMs != nil {
		if c.EstimatedLatencyMs == nil {
			codes = append(codes, RejectLatencyUnknown)
		} else if *c.EstimatedLatencyMs > *req.MaxLatencyMs {
			codes = append(codes, RejectLatencyLimit)
		}
	}
	if c.Local {
		if c.Hardware == nil {
			codes = append(codes, RejectHardwareUnknown)
		} else {
			if c.Hardware.MinMemoryGB > 0 {
				if req.Hardware.AvailableMemoryGB == nil {
					codes = append(codes, RejectMemoryUnknown)
				} else if *req.Hardware.AvailableMemoryGB < c.Hardware.MinMemoryGB {
					codes = append(codes, RejectMemoryLimit)
				}
			}
			if c.Hardware.RequiresGPU {
				if req.Hardware.HasGPU == nil {
					codes = append(codes, RejectGPUUnknown)
				} else if !*req.Hardware.HasGPU {
					codes = append(codes, RejectGPUUnavailable)
				}
			}
		}
	}
	return codes
}
