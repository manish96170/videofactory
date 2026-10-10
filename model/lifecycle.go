package model

import (
	"context"
	"time"

	"github.com/manish96170/videofactory/resource"
)

// Context has result precedence, but every original failure remains unwrap-able.
func operationError(parent, child context.Context, code Code, causes ...error) error {
	if child != nil && child.Err() != nil {
		causes = append(causes, child.Err())
		code = CodeTimeout
	}
	if parent != nil && parent.Err() != nil {
		causes = append(causes, parent.Err())
		code = CodeCanceled
	}
	return failure(code, causes...)
}

func timing(start, end time.Time, load, complete bool) resource.Measurement {
	ms := end.Sub(start).Milliseconds()
	if ms < 0 {
		ms = -1
	} // invalid clocks are surfaced, never invented as zero
	m := resource.Measurement{TimingSource: resource.SourceParentClock, TimingCompleteness: resource.MeasurementPartial}
	if complete {
		m.TimingCompleteness = resource.MeasurementComplete
	}
	if load {
		m.LoadMs = &ms
	} else {
		m.UnloadMs = &ms
	}
	return m
}

func (m *Manager) load(parent context.Context, e *entry) (error, error) {
	ctx, cancel := context.WithTimeout(parent, m.cfg.LoadTimeout)
	defer cancel()
	start := m.cfg.Now()
	before := e.loader.Identity()
	if before != e.key {
		return operationError(parent, ctx, CodeIdentityMismatch, ErrIdentityMismatch), nil
	}
	if ctx.Err() != nil {
		return operationError(parent, ctx, CodeLoadFailed, ErrLoadFailed), nil
	}
	m.mu.Lock()
	e.held.stopped = false
	spec := cloneSpec(e.held.spec)
	m.mu.Unlock()
	err := e.loader.Load(ctx, spec)
	after := e.loader.Identity() // checked on EVERY callback return, even errors
	end := m.cfg.Now()
	var identityErr error
	if before != e.key || after != e.key {
		identityErr = ErrIdentityMismatch
	}
	ok := err == nil && identityErr == nil && ctx.Err() == nil && parent.Err() == nil
	advisory := m.record(e, e.held.estimate.Key, timing(start, end, true, ok))
	if ok && parent.Err() == nil && ctx.Err() == nil {
		return nil, advisory
	}
	code := CodeLoadFailed
	if identityErr != nil {
		code = CodeIdentityMismatch
	}
	return operationError(parent, ctx, code, ErrLoadFailed, err, identityErr, advisory), advisory
}

// Failed loads use fresh separately bounded cleanup even when parent is expired.
func (m *Manager) cleanupFailedLoad(parent context.Context, e *entry, cause error) error {
	m.mu.Lock()
	stopped := e.held.stopped
	e.state = StateUnloading
	m.mu.Unlock()
	if stopped {
		return m.refund(e, cause)
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), m.cfg.UnloadTimeout)
	defer cancel()
	stop, err := m.stop(ctx, e)
	if !stop {
		m.quarantine(e, CodeNotStopped)
		return operationError(parent, nil, CodeQuarantined, cause, ErrQuarantined, err)
	}
	return m.refund(e, operationError(parent, nil, errorCode(cause), cause, err))
}

func errorCode(err error) Code {
	if e, ok := err.(*Error); ok {
		return e.Code()
	}
	return CodeUnloadFailed
}

// stop honors explicit bool proof independently of callback error/timeout, but
// NEVER accepts proof with drifted pre/post identity. Error payloads cannot
// silently turn unknown resources into stopped resources.
func (m *Manager) stop(ctx context.Context, e *entry) (bool, error) {
	start := m.cfg.Now()
	before := e.loader.Identity()
	stopped, err := e.loader.Unload(ctx)
	after := e.loader.Identity()
	end := m.cfg.Now()
	var identityErr, stoppedErr error
	if before != e.key || after != e.key {
		identityErr = ErrIdentityMismatch
	}
	verified := stopped && identityErr == nil
	if !stopped {
		stoppedErr = ErrNotStopped
	}
	advisory := m.record(e, e.held.estimate.Key, timing(start, end, false, verified && err == nil && ctx.Err() == nil))
	if verified {
		m.mu.Lock()
		e.held.stopped = true
		m.mu.Unlock()
	}
	if err == nil && identityErr == nil && stoppedErr == nil && advisory == nil && ctx.Err() == nil {
		return verified, nil
	}
	code := CodeUnloadFailed
	if !stopped {
		code = CodeNotStopped
	}
	if identityErr != nil {
		code = CodeIdentityMismatch
	}
	if err == nil && identityErr == nil && stoppedErr == nil && advisory != nil {
		code = CodeHistoryRecord
	}
	return verified, operationError(nil, ctx, code, ErrUnloadFailed, err, identityErr, stoppedErr, advisory)
}

// refund runs only after known no-start or verified stop. A failed Release keeps
// the token and stop proof quarantined; explicit Unload retries bookkeeping.
func (m *Manager) refund(e *entry, cause error) error {
	m.mu.Lock()
	h := e.held
	e.state = StateUnloading
	m.mu.Unlock()
	err := h.lease.Release()
	if err != nil {
		m.quarantine(e, CodeLeaseRelease)
		return failure(CodeLeaseRelease, cause, ErrQuarantined, ErrLeaseRelease, err)
	}
	m.mu.Lock()
	e.held = nil
	e.state = StateUnloaded
	e.token = nil
	if cause != nil {
		e.lastCode = errorCode(cause)
	}
	m.mu.Unlock()
	return cause
}

// Unload refuses use/reserve/load/unload conflicts. On uncertain stop it retains
// EVERY reserved resource in quarantine. Retry explicitly; no background expiry.
func (m *Manager) Unload(parent context.Context, key Key) error {
	if err := contextError(parent); err != nil {
		return err
	}
	if !m.valid() {
		return failure(CodeInvalidConfig, ErrInvalidConfig)
	}
	m.mu.Lock()
	if err := contextError(parent); err != nil {
		m.mu.Unlock()
		return err
	}
	e := m.entries[key]
	if e == nil {
		m.mu.Unlock()
		return failure(CodeNotFound, ErrNotFound)
	}
	if busy(e) {
		m.mu.Unlock()
		return failure(CodeBusy, ErrBusy)
	}
	if e.held == nil {
		m.mu.Unlock()
		return nil
	}
	e.state = StateUnloading
	stopped := e.held.stopped
	m.mu.Unlock()
	if stopped {
		err := m.refund(e, nil)
		if parent.Err() != nil {
			return operationError(parent, nil, CodeCanceled, err)
		}
		return err
	}
	ctx, cancel := context.WithTimeout(parent, m.cfg.UnloadTimeout)
	defer cancel()
	stop, err := m.stop(ctx, e)
	if !stop {
		m.quarantine(e, CodeNotStopped)
		return operationError(parent, ctx, CodeQuarantined, ErrQuarantined, err)
	}
	err = m.refund(e, err)
	if parent.Err() != nil {
		return operationError(parent, ctx, CodeCanceled, err)
	}
	return err
}
