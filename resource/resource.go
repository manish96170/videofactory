// Package resource defines the resource-management contract described in
// PHASES.md section 9: callers acquire a Lease before doing heavy work and
// release it when done. The Manager is the enforcement authority.
//
// "The LLM must never be the authority enforcing resource limits. The
// Resource Manager must enforce them." (PHASES.md section 3)
//
// Budgets are logical admission/accounting, not physical RAM/CPU/GPU containment.
package resource

import (
	"context"

	"github.com/manish96170/videofactory/domain"
)

// Request describes the resources a unit of work needs before it may run.
type Request struct {
	RAMBytes  int64                     `json:"ramBytes"`
	CPU       float64                   `json:"cpu"`
	GPU       float64                   `json:"gpu"`
	Exclusive bool                      `json:"exclusive,omitempty"`
	Heavy     bool                      `json:"heavy,omitempty"`
	Backend   domain.AcceleratorBackend `json:"backend,omitempty"`
}

// Lease represents a granted Request. Callers must Release it exactly
// once when done, typically via defer:
//
//	lease, err := manager.Acquire(ctx, req)
//	if err != nil {
//		return err
//	}
//	defer lease.Release()
//
// A successful Acquire returns a non-nil Lease and a nil error; a failed
// Acquire returns a nil Lease and a non-nil error. This package does not
// itself guarantee idempotent or concurrency-safe Release: BudgetManager does.
// Cancellation after successful handoff never automatically releases a lease.
// Release takes no context deliberately, since it is meant to be bounded local
// bookkeeping; an implementation that needs cancellable or long-running
// cleanup must prove work has stopped BEFORE Release, outside this bookkeeping.
type Lease interface {
	ID() string
	Release() error
}

// Manager is the resource enforcement authority every caller depends on.
// Acquire atomically reserves all resources before caller work starts.
type Manager interface {
	Acquire(ctx context.Context, req Request) (Lease, error)
}
