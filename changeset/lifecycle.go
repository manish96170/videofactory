package changeset

import (
	"context"
	"sync"

	"github.com/manish96170/videofactory/domain"
)

// Validated is an opaque process-local proposal. Its zero value is invalid.
// Copies share one lifecycle, preventing copied handles from bypassing cancel
// or one-shot apply. Snapshot/Audit return independent copies for clients.
type Validated struct{ state *validatedState }

type validatedState struct {
	mu       sync.Mutex
	preview  Preview
	result   domain.Project
	audit    []AuditRecord
	sequence uint64
	dropped  uint64
}

// Snapshot returns the reviewable proposal, result hash and advisory impact.
func (v *Validated) Snapshot() (Preview, error) {
	if v == nil || v.state == nil {
		return Preview{}, ErrInvalid
	}
	s := v.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return copyPreview(s.preview)
}

func copyPreview(p Preview) (Preview, error) {
	cs, err := copyChangeSet(p.ChangeSet)
	if err != nil {
		return Preview{}, err
	}
	p.ChangeSet = cs
	p.ChangedRoots = append([]domain.ID(nil), p.ChangedRoots...)
	p.InvalidatedShots = append([]domain.ID(nil), p.InvalidatedShots...)
	return p, nil
}

// Apply explicitly approves and consumes a validated proposal. Hash mismatch,
// cancellation and failure leave the proposal pending and return no project.
// Lifecycle operations on the same handle are serialized. A cancellation seen
// before the final commit point wins; cancellation after commit does not undo it.
func Apply(ctx context.Context, current domain.Project, v *Validated) (Result, error) {
	if err := checkContext(ctx); err != nil {
		return Result{}, err
	}
	if v == nil || v.state == nil {
		return Result{}, ErrInvalid
	}
	s := v.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkContext(ctx); err != nil {
		s.record("CANCELLED")
		return Result{}, err
	}
	if s.preview.ChangeSet.Status != domain.ChangeSetProposed {
		s.record("STATE_REJECTED")
		return Result{}, ErrState
	}
	_, hash, err := snapshotProject(current)
	if err != nil {
		s.record("INVALID")
		return Result{}, err
	}
	if hash != s.preview.BaseHash {
		s.record("STALE")
		return Result{}, ErrStale
	}
	out, _, err := snapshotProject(s.result)
	if err != nil {
		s.record("INVALID")
		return Result{}, err
	}
	preview, err := copyPreview(s.preview)
	if err != nil {
		s.record("INVALID")
		return Result{}, err
	}
	if err := checkContext(ctx); err != nil {
		s.record("CANCELLED")
		return Result{}, err
	}
	s.preview.ChangeSet.Status = domain.ChangeSetApplied
	preview.ChangeSet.Status = domain.ChangeSetApplied
	s.record("APPLIED")
	return Result{Project: out, Preview: preview}, nil
}

// Cancel rejects a pending proposal. Repeated cancellation is idempotent; an
// applied proposal cannot be cancelled. It never mutates project state.
func (v *Validated) Cancel(ctx context.Context) error {
	if err := checkContext(ctx); err != nil {
		return err
	}
	if v == nil || v.state == nil {
		return ErrInvalid
	}
	s := v.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := checkContext(ctx); err != nil {
		return err
	}
	if s.preview.ChangeSet.Status == domain.ChangeSetRejected {
		return nil
	}
	if s.preview.ChangeSet.Status != domain.ChangeSetProposed {
		s.record("STATE_REJECTED")
		return ErrState
	}
	s.preview.ChangeSet.Status = domain.ChangeSetRejected
	s.record("REJECTED")
	return nil
}

// Audit returns the newest MaxAuditRecords and the number of discarded older
// records. Failed Validate/provider calls have no handle and no audit history.
func (v *Validated) Audit() ([]AuditRecord, uint64) {
	if v == nil || v.state == nil {
		return nil, 0
	}
	s := v.state
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]AuditRecord(nil), s.audit...), s.dropped
}

func (s *validatedState) record(code string) {
	s.sequence++
	if len(s.audit) == MaxAuditRecords {
		copy(s.audit, s.audit[1:])
		s.audit = s.audit[:len(s.audit)-1]
		s.dropped++
	}
	s.audit = append(s.audit, AuditRecord{Sequence: s.sequence, ProjectID: s.preview.ChangeSet.ProjectID, ChangeSetID: s.preview.ChangeSet.ID, Code: code})
}
