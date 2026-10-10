package model

import "github.com/manish96170/videofactory/resource"

// UseHandle is exclusive use ownership, not ownership of the resource lease.
// Release is bounded, idempotent, concurrency-safe, and does NOT unload/free the
// backend. No GC finalizer or context expiry changes that. Private pointer tokens
// prevent stale/copied/repeated handles from clearing a subsequent use owner.
type UseHandle struct {
	manager          *Manager
	entry            *entry
	token            *useToken
	spec             LoadSpec
	estimate         resource.Estimate
	observationError error
}

func (h *UseHandle) Key() Key                    { return h.spec.Key }
func (h *UseHandle) Spec() LoadSpec              { return cloneSpec(h.spec) }
func (h *UseHandle) Estimate() resource.Estimate { return cloneEstimate(h.estimate) }

// ObservationError exposes advisory load-timing record failures (including full
// history) without treating a successfully loaded model as an acquire failure.
// These failures are also counted in Manager.Snapshot. Never log raw causes.
func (h *UseHandle) ObservationError() error { return h.observationError }

func (h *UseHandle) Release() error {
	if h == nil || h.manager == nil || h.token == nil {
		return failure(CodeReleased, ErrReleased)
	}
	m := h.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if h.entry.token == h.token {
		h.entry.token = nil
		h.entry.state = StateIdle
	}
	return nil
}

// RecordMeasurement accepts ONLY trusted parent-observed values, validates their
// source/completeness, and uses this handle's exact immutable Estimate.Key. It
// cannot relabel another model/profile/workload. Released/stale handles refuse.
// History errors are returned AND counted; observations remain copied on the
// model snapshot even if History is full. Advisory failure never frees a model.
// Caller must not mutate during cloning; no raw generation inputs belong here.
func (h *UseHandle) RecordMeasurement(measurement resource.Measurement) error {
	if h == nil || h.manager == nil || h.token == nil {
		return failure(CodeReleased, ErrReleased)
	}
	measurement = resource.CloneMeasurement(measurement)
	if err := measurement.Validate(); err != nil {
		return failure(CodeHistoryRecord, ErrHistoryRecord, err)
	}
	m := h.manager
	m.mu.Lock()
	if h.entry.token != h.token {
		m.mu.Unlock()
		return failure(CodeReleased, ErrReleased)
	}
	h.entry.recording++
	m.mu.Unlock()
	err := m.record(h.entry, h.estimate.Key, measurement)
	m.mu.Lock()
	h.entry.recording--
	m.mu.Unlock()
	return err
}

func mergePeak(a, b *resource.MemoryPeak) *resource.MemoryPeak {
	if b != nil && (a == nil || b.Bytes > a.Bytes || (b.Bytes == a.Bytes && b.Completeness == resource.MeasurementComplete)) {
		v := *b
		return &v
	}
	return a
}

// Preserve peaks and known times even when the shared history rejects a record.
func mergeObservation(a, b resource.Measurement) resource.Measurement {
	a.FamilyRSS = mergePeak(a.FamilyRSS, b.FamilyRSS)
	a.ProcessPeakRSS = mergePeak(a.ProcessPeakRSS, b.ProcessPeakRSS)
	if b.LoadMs != nil {
		a.LoadMs = b.LoadMs
	}
	if b.RunMs != nil {
		a.RunMs = b.RunMs
	}
	if b.UnloadMs != nil {
		a.UnloadMs = b.UnloadMs
	}
	if b.LoadMs != nil || b.RunMs != nil || b.UnloadMs != nil {
		a.TimingSource = resource.SourceParentClock
		if a.TimingCompleteness == resource.MeasurementPartial || b.TimingCompleteness == resource.MeasurementPartial {
			a.TimingCompleteness = resource.MeasurementPartial
		} else {
			a.TimingCompleteness = resource.MeasurementComplete
		}
	}
	return a
}

func (m *Manager) record(e *entry, key resource.HistoryKey, observation resource.Measurement) error {
	observation = resource.CloneMeasurement(observation)
	err := observation.Validate()
	if err == nil {
		err = m.history.Record(key, resource.CloneMeasurement(observation))
	}
	m.mu.Lock()
	if observation.Validate() == nil {
		e.observations = mergeObservation(e.observations, observation)
	}
	if err != nil {
		if e.historyFailures != ^uint64(0) {
			e.historyFailures++
		}
		e.lastCode = CodeHistoryRecord
	}
	m.mu.Unlock()
	if err != nil {
		return failure(CodeHistoryRecord, ErrHistoryRecord, err)
	}
	return nil
}
