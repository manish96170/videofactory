package resource

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"sync"

	"github.com/manish96170/videofactory/domain"
)

type MeasurementSource string

const (
	SourceNativeFamilyRSS MeasurementSource = "parent_native_family_rss"
	SourceOSProcessPeak   MeasurementSource = "parent_os_single_process_peak"
	SourceParentClock     MeasurementSource = "parent_clock"
)

type Completeness string

const (
	MeasurementPartial  Completeness = "partial"
	MeasurementComplete Completeness = "complete"
)

// MemoryPeak is parent-measured evidence. Completeness describes collection,
// not continuous coverage/proof of an instantaneous peak: sampled maxima are
// lower bounds even when collection completes, not kernel containment. Unknown
// measurements are nil, NEVER an invented zero. Known zero is representable.
type MemoryPeak struct {
	Bytes        int64             `json:"bytes"`
	Source       MeasurementSource `json:"source"`
	Completeness Completeness      `json:"completeness"`
}

// Measurement is shared by worker/model owners. FamilyRSS is native parent
// process-group sampling; ProcessPeakRSS is the OS-reported SINGLE process peak
// and must never be labelled family RSS. Timings are advisory parent-clock
// milliseconds (nil=unknown); owners inject clocks, not ambient history dates.
// TimingCompleteness applies to supplied values, not missing lifecycle phases.
// No worker-reported memory, handles, stop proofs, paths, prompts or raw I/O.
type Measurement struct {
	FamilyRSS          *MemoryPeak       `json:"familyRss,omitempty"`
	ProcessPeakRSS     *MemoryPeak       `json:"processPeakRss,omitempty"`
	LoadMs             *int64            `json:"loadMs,omitempty"`
	RunMs              *int64            `json:"runMs,omitempty"`
	UnloadMs           *int64            `json:"unloadMs,omitempty"`
	TimingSource       MeasurementSource `json:"timingSource,omitempty"`
	TimingCompleteness Completeness      `json:"timingCompleteness,omitempty"`
}

func copyInt(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func copyPeak(p *MemoryPeak) *MemoryPeak {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
func CloneMeasurement(m Measurement) Measurement {
	m.FamilyRSS = copyPeak(m.FamilyRSS)
	m.ProcessPeakRSS = copyPeak(m.ProcessPeakRSS)
	m.LoadMs = copyInt(m.LoadMs)
	m.RunMs = copyInt(m.RunMs)
	m.UnloadMs = copyInt(m.UnloadMs)
	return m
}
func complete(c Completeness) bool { return c == MeasurementPartial || c == MeasurementComplete }
func (m Measurement) Validate() error {
	for _, v := range []struct {
		peak   *MemoryPeak
		source MeasurementSource
	}{{m.FamilyRSS, SourceNativeFamilyRSS}, {m.ProcessPeakRSS, SourceOSProcessPeak}} {
		if v.peak != nil && (v.peak.Bytes < 0 || v.peak.Source != v.source || !complete(v.peak.Completeness)) {
			return ErrInvalidMeasurement
		}
	}
	timed := false
	for _, p := range []*int64{m.LoadMs, m.RunMs, m.UnloadMs} {
		if p != nil {
			timed = true
			if *p < 0 {
				return ErrInvalidMeasurement
			}
		}
	}
	if timed {
		if m.TimingSource != SourceParentClock || !complete(m.TimingCompleteness) {
			return ErrInvalidMeasurement
		}
	} else if m.TimingSource != "" || m.TimingCompleteness != "" {
		return ErrInvalidMeasurement
	}
	return nil
}

// HistoryKey is comparable and exact. Revisions/class/workload labels are
// supplied nonsecret stable handles; callers must not put prompts/paths in them.
type HistoryKey struct {
	ProviderID      string          `json:"providerId"`
	ModelID         string          `json:"modelId"`
	ModelRevision   string          `json:"modelRevision"`
	ProfileID       string          `json:"profileId"`
	ProfileRevision string          `json:"profileRevision"`
	HardwareClass   string          `json:"hardwareClass"`
	Workload        domain.Workload `json:"workload"`
}

func (k HistoryKey) Validate() error {
	if blank(k.ProviderID) || blank(k.ModelID) || blank(k.ModelRevision) || blank(k.ProfileID) || blank(k.ProfileRevision) || blank(k.HardwareClass) {
		return ErrInvalidRequest
	}
	return ValidateWorkload(k.Workload)
}

// HistorySummary retains independently labelled maxima and most-recent known
// timings. Unknown/sparse observations never erase known maxima/timings. Source
// and completeness accompany the maximum that supplied each value.
type HistorySummary struct {
	Key         HistoryKey  `json:"key"`
	Samples     uint64      `json:"samples"`
	Measurement Measurement `json:"measurement"`
}
type HistorySnapshot struct {
	Version uint64           `json:"version"`
	Hash    string           `json:"hash"`
	Entries []HistorySummary `json:"entries"`
}

// History stores bounded exact-key summaries without eviction. At capacity,
// new keys return ErrHistoryFull without forgetting learned high-water evidence;
// existing keys remain updatable. No wall-clock freshness or automatic storage.
// Construct with NewHistory; limit=0 explicitly selects the reference 256.
type History struct {
	mu      sync.Mutex
	limit   int
	version uint64
	entries map[HistoryKey]HistorySummary
	order   []HistoryKey
}

func NewHistory(limit int) (*History, error) {
	if limit < 0 {
		return nil, ErrInvalidConfig
	}
	if limit == 0 {
		limit = 256
	}
	return &History{limit: limit, entries: make(map[HistoryKey]HistorySummary)}, nil
}
func (h *History) Record(key HistoryKey, m Measurement) error {
	if err := key.Validate(); err != nil {
		return err
	}
	m = CloneMeasurement(m)
	if err := m.Validate(); err != nil {
		return err
	}
	if h == nil || h.entries == nil {
		return ErrInvalidConfig
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	old, exists := h.entries[key]
	if h.version == math.MaxUint64 || old.Samples == math.MaxUint64 {
		return ErrOverflow
	}
	if !exists {
		if len(h.entries) >= h.limit {
			return ErrHistoryFull
		}
		h.order = append(h.order, key)
		old.Key = key
	}
	old.Samples++
	old.Measurement.FamilyRSS = mergePeak(old.Measurement.FamilyRSS, m.FamilyRSS)
	old.Measurement.ProcessPeakRSS = mergePeak(old.Measurement.ProcessPeakRSS, m.ProcessPeakRSS)
	// Timing source is always the parent clock; completeness becomes partial
	// if any retained timing is partial (including mixed lifecycle samples).
	for _, pair := range []struct {
		dst **int64
		src *int64
	}{{&old.Measurement.LoadMs, m.LoadMs}, {&old.Measurement.RunMs, m.RunMs}, {&old.Measurement.UnloadMs, m.UnloadMs}} {
		if pair.src != nil {
			*pair.dst = copyInt(pair.src)
		}
	}
	if m.LoadMs != nil || m.RunMs != nil || m.UnloadMs != nil {
		old.Measurement.TimingSource = SourceParentClock
		if old.Measurement.TimingCompleteness == MeasurementPartial || m.TimingCompleteness == MeasurementPartial {
			old.Measurement.TimingCompleteness = MeasurementPartial
		} else {
			old.Measurement.TimingCompleteness = MeasurementComplete
		}
	}
	h.entries[key] = old
	h.version++
	return nil
}
func mergePeak(old, new *MemoryPeak) *MemoryPeak {
	if new == nil {
		return old
	}
	if old == nil || new.Bytes > old.Bytes || (new.Bytes == old.Bytes && new.Completeness == MeasurementComplete) {
		return copyPeak(new)
	}
	return old
}
func (h *History) Snapshot() HistorySnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := HistorySnapshot{Version: h.version, Entries: make([]HistorySummary, 0, len(h.entries))}
	for _, entry := range h.entries {
		entry.Measurement = CloneMeasurement(entry.Measurement)
		s.Entries = append(s.Entries, entry)
	}
	sort.Slice(s.Entries, func(i, j int) bool {
		a, _ := json.Marshal(s.Entries[i].Key)
		b, _ := json.Marshal(s.Entries[j].Key)
		return string(a) < string(b)
	})
	encoded, _ := json.Marshal(struct {
		Version uint64           `json:"version"`
		Entries []HistorySummary `json:"entries"`
	}{s.Version, s.Entries})
	hash := sha256.Sum256(encoded)
	s.Hash = hex.EncodeToString(hash[:])
	return s
}
func (h *History) Lookup(key HistoryKey) (HistorySummary, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.entries[key]
	s.Measurement = CloneMeasurement(s.Measurement)
	return s, ok
}
func (s HistorySnapshot) Lookup(key HistoryKey) (HistorySummary, bool) {
	for _, entry := range s.Entries {
		if entry.Key == key {
			entry.Measurement = CloneMeasurement(entry.Measurement)
			return entry, true
		}
	}
	return HistorySummary{}, false
}

// ObservedPeak uses the maximum of separately labelled parent measurements;
// neither sparse/partial samples nor single-process-only measurements lower it.
func (m Measurement) ObservedPeak() *int64 {
	var peak *int64
	for _, p := range []*MemoryPeak{m.FamilyRSS, m.ProcessPeakRSS} {
		if p != nil && (peak == nil || p.Bytes > *peak) {
			v := p.Bytes
			peak = &v
		}
	}
	return peak
}
