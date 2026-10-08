// Package resource defines the resource-management contract described in
// PHASES.md section 9: callers acquire a Lease before doing heavy work and
// release it when done. The Manager is the enforcement authority.
//
// "The LLM must never be the authority enforcing resource limits. The
// Resource Manager must enforce them." (PHASES.md section 3)
//
// This package only defines the contract. The real budget-aware Manager
// (memory/CPU/GPU accounting, queueing, process monitoring) is built in
// Phase 08.
package resource

import "context"

// Request describes the resources a unit of work needs before it may run.
type Request struct {
	RAMBytes  int64
	CPU       float64
	GPU       float64
	Exclusive bool
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
// itself guarantee idempotent or concurrency-safe Release: Phase 08's
// Manager implementation defines and documents those guarantees. Release
// takes no context deliberately, since it is meant to be bounded local
// bookkeeping; an implementation that needs cancellable or long-running
// cleanup should perform that asynchronously rather than blocking inside
// Release.
type Lease interface {
	ID() string
	Release() error
}

// Manager is the resource enforcement authority every caller depends on.
// Implementations are introduced in Phase 08.
type Manager interface {
	Acquire(ctx context.Context, req Request) (Lease, error)
}
