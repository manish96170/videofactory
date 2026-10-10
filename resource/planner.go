package resource

import (
	"context"
	"errors"
	"reflect"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/provider"
)

type PlanStatus string

const (
	StatusLocalReady      PlanStatus = "local_ready"
	StatusWait            PlanStatus = "wait"
	StatusLocalIneligible PlanStatus = "local_ineligible"
	StatusRemoteAvailable PlanStatus = "remote_available"
)

// RemoteRouter is the existing Phase 07 metadata-only routing seam. An injected
// implementation must preserve all Phase 07 gates (the production Router does).
type RemoteRouter interface {
	Route(context.Context, provider.RouteRequest) (provider.RouteResult, error)
}
type SnapshotSource interface{ Snapshot() Snapshot }
type HistorySource interface{ Snapshot() HistorySnapshot }

type PlannerConfig struct {
	SafetyMarginPercent int64
	Router              RemoteRouter // optional, caller-owned, concurrency-safe
}

func DefaultPlannerConfig() PlannerConfig { return PlannerConfig{SafetyMarginPercent: 25} }

// RemotePolicy is explicit opt-in. Route MUST declare RemoteOnly; local/any
// policies are rejected rather than silently broadened/rewritten. Prefer means
// advice, NEVER authorization to spend or execute a provider.
type RemotePolicy struct {
	Route  provider.RouteRequest `json:"route"`
	Prefer bool                  `json:"prefer,omitempty"`
}
type PlanRequest struct {
	ID           domain.ID                `json:"id"`
	TargetID     domain.ID                `json:"targetId"`
	Capabilities domain.ModelCapabilities `json:"capabilities"` // trusted/frozen source, not LLM input
	ProfileID    string                   `json:"profileId"`
	Workload     domain.Workload          `json:"workload"`
	Remote       *RemotePolicy            `json:"remote,omitempty"`
}
type PlanResult struct {
	Status      PlanStatus           `json:"status"`
	LocalStatus PlanStatus           `json:"localStatus"`
	Plan        domain.ResourcePlan  `json:"plan"`
	Request     Request              `json:"request"`
	Remote      provider.RouteResult `json:"remote"`
}

// Planner makes recommendations only. snapshots/history are trusted injected
// sources; adapters are never called by the local planner. Config/facts are read
// coherently with current availability. No automatic fallback or reservations.
type Planner struct {
	snapshots SnapshotSource
	history   HistorySource
	cfg       PlannerConfig
}

func nilInterface(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan:
		return r.IsNil()
	}
	return false
}
func NewPlanner(snapshots SnapshotSource, history HistorySource, cfg PlannerConfig) (*Planner, error) {
	if nilInterface(snapshots) || cfg.SafetyMarginPercent < 0 {
		return nil, ErrInvalidConfig
	}
	if nilInterface(history) {
		history = nil
	}
	if nilInterface(cfg.Router) {
		cfg.Router = nil
	}
	return &Planner{snapshots: snapshots, history: history, cfg: cfg}, nil
}

func (p *Planner) Plan(ctx context.Context, req PlanRequest) (PlanResult, error) {
	if ctx == nil {
		return PlanResult{}, ErrInvalidRequest
	}
	if err := ctx.Err(); err != nil {
		return PlanResult{}, err
	}
	if p == nil || nilInterface(p.snapshots) {
		return PlanResult{}, ErrInvalidConfig
	}
	if blank(string(req.ID)) || blank(string(req.TargetID)) || blank(req.ProfileID) {
		return PlanResult{}, ErrInvalidRequest
	}
	if err := ValidateWorkload(req.Workload); err != nil {
		return PlanResult{}, err
	}
	req.Capabilities = CloneCapabilities(req.Capabilities)
	if req.Remote != nil {
		r := *req.Remote
		r.Route = r.Route.Clone()
		req.Remote = &r
		if r.Route.Locality != provider.RemoteOnly {
			return PlanResult{}, ErrInvalidRequest
		}
		if err := r.Route.Validate(); err != nil {
			return PlanResult{}, err
		}
		if p.cfg.Router == nil {
			return PlanResult{}, ErrInvalidConfig
		}
	}
	snapshot := p.snapshots.Snapshot()
	if err := ctx.Err(); err != nil {
		return PlanResult{}, err
	}
	if err := snapshot.Config.Validate(); err != nil {
		return PlanResult{}, err
	}
	history := HistorySnapshot{}
	if p.history != nil {
		history = p.history.Snapshot()
	}
	if err := ctx.Err(); err != nil {
		return PlanResult{}, err
	}
	est, err := EstimateRequest(req.Capabilities, req.ProfileID, req.Workload, snapshot.Config, history, p.cfg.SafetyMarginPercent)
	if canceled := ctx.Err(); canceled != nil {
		return PlanResult{}, canceled
	}
	result := PlanResult{Status: StatusLocalIneligible, LocalStatus: StatusLocalIneligible,
		Plan: domain.ResourcePlan{ID: req.ID, TargetID: req.TargetID, Rationale: string(StatusLocalIneligible)}}
	if err == nil || errors.Is(err, ErrOverBudget) || errors.Is(err, ErrIncompatible) {
		prov := est.Provenance
		prov.ObservedPeakRAMBytes = copyInt(prov.ObservedPeakRAMBytes)
		result.Request = est.Request
		result.Plan.RAMBytes = est.Request.RAMBytes
		result.Plan.CPU = est.Request.CPU
		result.Plan.GPU = est.Request.GPU
		result.Plan.Heavy = est.Request.Heavy
		result.Plan.Exclusive = est.Request.Exclusive
		result.Plan.Backend = est.Request.Backend
		result.Plan.Provenance = &prov
	}
	if err == nil {
		available, err := snapshot.AvailableNow(est.Request)
		if err != nil {
			return PlanResult{}, err
		}
		result.LocalStatus = StatusWait
		if available {
			result.LocalStatus = StatusLocalReady
		}
		result.Status = result.LocalStatus
	} else if !errors.Is(err, ErrOverBudget) && !errors.Is(err, ErrIncompatible) && !errors.Is(err, ErrLocalIneligible) && !errors.Is(err, ErrInvalidProfile) && !errors.Is(err, ErrOverflow) {
		return PlanResult{}, err
	}
	result.Plan.Rationale = string(result.LocalStatus)
	if req.Remote != nil {
		route, err := p.cfg.Router.Route(ctx, req.Remote.Route)
		if canceled := ctx.Err(); canceled != nil {
			return PlanResult{}, canceled
		}
		if err != nil && !errors.Is(err, provider.ErrNoEligibleProviders) {
			return PlanResult{}, err
		}
		result.Remote = cloneRouteResult(route)
		if err == nil && len(route.EligibleIDs) > 0 {
			if req.Remote.Prefer || result.LocalStatus != StatusLocalReady {
				result.Status = StatusRemoteAvailable
				result.Plan.PreferRemote = true
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return PlanResult{}, err
	}
	return result, nil
}

func cloneRouteResult(r provider.RouteResult) provider.RouteResult {
	r.EligibleIDs = append([]string(nil), r.EligibleIDs...)
	r.FallbackIDs = append([]string(nil), r.FallbackIDs...)
	r.Rejections = append([]provider.Rejection(nil), r.Rejections...)
	for i := range r.Rejections {
		r.Rejections[i].Codes = append([]provider.RejectionCode(nil), r.Rejections[i].Codes...)
	}
	return r
}
