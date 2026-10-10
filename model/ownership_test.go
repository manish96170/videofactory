package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/model"
	"github.com/manish96170/videofactory/resource"
)

// All fake bookkeeping is synchronized; none of these tests uses smokeLoader.
// Bounds are declarations only: no models, processes, or large allocations.
type ownershipLoader struct {
	key      model.Key
	mu       sync.Mutex
	loads    int
	unloads  int
	onLoad   func(context.Context, model.LoadSpec) error
	onUnload func(context.Context) (bool, error)
}

func (l *ownershipLoader) Identity() model.Key { return l.key }
func (l *ownershipLoader) Load(ctx context.Context, spec model.LoadSpec) error {
	l.mu.Lock()
	l.loads++
	fn := l.onLoad
	l.mu.Unlock()
	if fn != nil {
		return fn(ctx, spec)
	}
	return nil
}
func (l *ownershipLoader) Unload(ctx context.Context) (bool, error) {
	l.mu.Lock()
	l.unloads++
	fn := l.onUnload
	l.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return true, nil
}
func (l *ownershipLoader) counts() (int, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loads, l.unloads
}

type ownershipBudget struct {
	*resource.BudgetManager
	mu       sync.Mutex
	acquires int
}

func (b *ownershipBudget) Acquire(ctx context.Context, r resource.Request) (resource.Lease, error) {
	b.mu.Lock()
	b.acquires++
	b.mu.Unlock()
	return b.BudgetManager.Acquire(ctx, r)
}
func (b *ownershipBudget) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acquires
}

type ownershipHistory struct {
	*resource.History
	mu   sync.Mutex
	keys []resource.HistoryKey
}

func (h *ownershipHistory) Record(k resource.HistoryKey, m resource.Measurement) error {
	h.mu.Lock()
	h.keys = append(h.keys, k)
	h.mu.Unlock()
	return h.History.Record(k, m)
}
func (h *ownershipHistory) recordedKeys() []resource.HistoryKey {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]resource.HistoryKey(nil), h.keys...)
}

type ownershipFixture struct {
	manager *model.Manager
	budget  *ownershipBudget
	history *ownershipHistory
	loader  *ownershipLoader
	caps    domain.ModelCapabilities
	key     model.Key
	w       domain.Workload
}

func ownershipProfile(id string) domain.ExecutionProfile {
	return domain.ExecutionProfile{
		ID: id, Revision: "profile-r1", Trusted: true, MemoryKnown: true,
		LoadRAMBytes: 128_000_000, ResidentRAMBytes: 96_000_000, WorkspaceRAMBytes: 64_000_000,
		CPU: .5, GPU: .25, Backend: domain.BackendMetal, Heavy: true, Exclusive: true,
		Envelope: domain.WorkloadEnvelope{
			Precisions: []string{"fp16", "fp32"}, Qualities: []string{"preview", "final"}, Offloads: []string{"none", "cpu"},
			Width: domain.IntRange{Min: 64, Max: 128}, Height: domain.IntRange{Min: 32, Max: 64},
			Frames: domain.IntRange{Min: 1, Max: 4}, ChunkSize: domain.IntRange{Min: 1, Max: 2},
		},
	}
}
func ownershipWorkload() domain.Workload {
	return domain.Workload{Precision: "fp16", Width: 64, Height: 32, Frames: 2, Quality: "preview", Offload: "none", ChunkSize: 1}
}
func ownershipKey() resource.HistoryKey {
	return resource.HistoryKey{
		ProviderID: "local", ModelID: "ownership-model", ModelRevision: "model-r1",
		ProfileID: "preview", ProfileRevision: "profile-r1", HardwareClass: "ownership-hardware", Workload: ownershipWorkload(),
	}
}
func ownershipRequest(ram int64) resource.Request {
	return resource.Request{RAMBytes: ram, CPU: .5, GPU: .25, Backend: domain.BackendMetal, Heavy: true, Exclusive: true}
}
func ownershipPeak(bytes int64) *resource.MemoryPeak {
	return &resource.MemoryPeak{Bytes: bytes, Source: resource.SourceNativeFamilyRSS, Completeness: resource.MeasurementComplete}
}
func ownershipInt(n int64) *int64 { return &n }

func ownershipNew(t *testing.T, limit int) ownershipFixture {
	t.Helper()
	cfg := resource.ReferenceConfig(4, 2, resource.AcceleratorFacts{
		Known: true, Backend: domain.BackendMetal, UnifiedMemory: true, Capacity: 1,
	}, 1, "ownership-hardware")
	b, err := resource.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, err := resource.NewHistory(limit)
	if err != nil {
		t.Fatal(err)
	}
	f := ownershipFixture{
		budget: &ownershipBudget{BudgetManager: b}, history: &ownershipHistory{History: h},
		key: model.Key{ProviderID: "local", ModelID: "ownership-model", Revision: "model-r1"}, w: ownershipWorkload(),
		caps: domain.ModelCapabilities{
			ProviderID: "local", ModelID: "ownership-model", Local: true, MinMemoryGB: .1, RequiresGPU: true,
			Tags: []string{"semantic-decision", "video"},
			Requirements: &domain.ModelRequirements{Version: 1, Revision: "model-r1",
				ExecutionProfiles: []domain.ExecutionProfile{ownershipProfile("preview"), ownershipProfile("alternate")}},
		},
	}
	var ticks atomic.Int64
	mc := model.DefaultConfig()
	mc.Now = func() time.Time { return time.Unix(1_000, ticks.Add(7)*int64(time.Millisecond)) }
	f.manager, err = model.NewManager(f.budget, f.history, mc)
	if err != nil {
		t.Fatal(err)
	}
	f.loader = &ownershipLoader{key: f.key}
	if err := f.manager.Register(context.Background(), f.caps, f.loader); err != nil {
		t.Fatal(err)
	}
	return f
}

// JSON is an independent ownership oracle, not a resource/model clone helper.
func ownershipJSONCopy[T any](t *testing.T, v T) T {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var copy T
	if err := json.Unmarshal(data, &copy); err != nil {
		t.Fatal(err)
	}
	return copy
}
func ownershipEqual(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %#v, want %#v", label, got, want)
	}
}
func ownershipAcquire(t *testing.T, f ownershipFixture) *model.UseHandle {
	t.Helper()
	h, err := f.manager.Acquire(context.Background(), f.key, "preview", f.w)
	if err != nil || h == nil {
		t.Fatalf("Acquire = %v, %v", h, err)
	}
	return h
}
func ownershipHeldError(b resource.Snapshot, ram int64) error {
	used := resource.Counters{RAMBytes: ram, CPUUnits: 500_000, GPUUnits: 250_000, Slots: 1, Heavy: 1}
	available := resource.Counters{RAMBytes: 7_500_000_000 - ram, CPUUnits: 1_500_000, GPUUnits: 750_000, Slots: 1}
	if b.Used != used || b.Available != available || !b.Exclusive || len(b.Queue) != 0 || len(b.Active) != 1 ||
		b.Active[0].Request != ownershipRequest(ram) || b.Active[0].Reserved != used || b.Active[0].ID == "" {
		return fmt.Errorf("full RAM/CPU/GPU/slot/heavy/exclusive reservation differs: %+v", b)
	}
	return nil
}
func ownershipHeld(t *testing.T, f ownershipFixture, state model.State, ram int64) {
	t.Helper()
	s := f.manager.Snapshot()
	if len(s.Models) != 1 || s.Models[0].State != state || s.Models[0].Reservation == nil ||
		*s.Models[0].Reservation != ownershipRequest(ram) || s.Models[0].StoppedVerified {
		t.Fatalf("unexpected model ownership: %+v", s)
	}
	if err := ownershipHeldError(f.budget.Snapshot(), ram); err != nil {
		t.Fatal(err)
	}
}
func ownershipRefunded(t *testing.T, f ownershipFixture) {
	t.Helper()
	s, b := f.manager.Snapshot().Models[0], f.budget.Snapshot()
	if s.State != model.StateUnloaded || s.Spec != nil || s.Estimate != nil || s.Reservation != nil ||
		b.Used != (resource.Counters{}) || b.Available != b.Total || len(b.Active) != 0 || len(b.Queue) != 0 || b.Exclusive {
		t.Fatalf("verified stop did not refund exactly: model=%+v budget=%+v", s, b)
	}
}
func ownershipPoisonProfile(p *domain.ExecutionProfile) {
	p.Envelope.Precisions[0] = "edited-precision"
	p.Envelope.Qualities[0] = "edited-quality"
	p.Envelope.Offloads[0] = "edited-offload"
	p.Envelope.Width.Max = 2048
	p.Envelope.Height.Max = 2048
	p.Envelope.Frames.Max = 2048
	p.Envelope.ChunkSize.Max = 2048
	p.ID, p.Revision, p.Trusted, p.MemoryKnown = "edited", "edited", false, false
	p.LoadRAMBytes, p.ResidentRAMBytes, p.WorkspaceRAMBytes = 1, 1, 1
	p.CPU, p.GPU, p.Backend, p.Heavy, p.Exclusive = 0, 0, domain.BackendNone, false, false
}
func ownershipPoisonCaps(c *domain.ModelCapabilities) {
	c.Tags[0] = "edited-tag"
	c.Requirements.Version, c.Requirements.Revision = 2, "edited"
	for i := range c.Requirements.ExecutionProfiles {
		ownershipPoisonProfile(&c.Requirements.ExecutionProfiles[i])
	}
	c.ProviderID, c.ModelID, c.Local, c.MinMemoryGB, c.RequiresGPU = "edited", "edited", false, 0, false
}

func TestOwnershipFrozenMetadataAndAdmission(t *testing.T) {
	f := ownershipNew(t, 0)
	wantCaps := ownershipJSONCopy(t, f.caps)
	wantSpec := model.LoadSpec{Key: f.key, Profile: ownershipProfile("preview"), Workload: ownershipWorkload()}
	ownershipEqual(t, "registration", f.manager.Snapshot(), model.Snapshot{Models: []model.ModelSnapshot{{
		Key: f.key, Capabilities: wantCaps, State: model.StateUnloaded,
	}}})
	if loads, unloads := f.loader.counts(); loads != 0 || unloads != 0 || f.budget.count() != 0 {
		t.Fatal("registration performed lifecycle/resource acquisition")
	}
	ownershipPoisonCaps(&f.caps)
	// Mutated caller envelopes must not admit an originally unsupported workload.
	out := f.w
	out.Width = 129
	if h, err := f.manager.Acquire(context.Background(), f.key, "preview", out); h != nil || !errors.Is(err, resource.ErrLocalIneligible) {
		t.Fatalf("caller-edited envelope changed admission: %v, %v", h, err)
	}
	if err := f.history.History.Record(ownershipKey(), resource.Measurement{FamilyRSS: ownershipPeak(200_000_000)}); err != nil {
		t.Fatal(err)
	}
	hs := f.history.Snapshot()
	wantEstimate := resource.Estimate{Request: ownershipRequest(250_000_000), Key: ownershipKey(), Provenance: domain.EstimateProvenance{
		ProviderID: "local", ModelID: "ownership-model", ModelRevision: "model-r1", ProfileID: "preview", ProfileRevision: "profile-r1",
		HardwareClass: "ownership-hardware", Workload: ownershipWorkload(), DeclaredPeakRAMBytes: 160_000_000,
		LegacyFloorRAMBytes: 100_000_000, ObservedPeakRAMBytes: ownershipInt(200_000_000), SafetyMarginPercent: 25,
		HistoryVersion: hs.Version, HistoryHash: hs.Hash,
	}}
	f.loader.onLoad = func(_ context.Context, spec model.LoadSpec) error {
		if !reflect.DeepEqual(spec, wantSpec) || f.budget.count() != 1 {
			return errors.New("callback did not receive exact frozen spec with one prior reservation")
		}
		if err := ownershipHeldError(f.budget.Snapshot(), 250_000_000); err != nil {
			return err
		}
		// Synchronous mutation tests the copy boundary, not a permitted adapter policy.
		ownershipPoisonProfile(&spec.Profile)
		return nil
	}
	h := ownershipAcquire(t, f)
	want := model.Snapshot{Models: []model.ModelSnapshot{{
		Key: f.key, Capabilities: wantCaps, State: model.StateInUse, Spec: &wantSpec, Estimate: &wantEstimate,
		Reservation: ownershipIntRequest(250_000_000), Observations: resource.Measurement{
			LoadMs: ownershipInt(7), TimingSource: resource.SourceParentClock, TimingCompleteness: resource.MeasurementComplete,
		},
	}}}
	// Freeze the entire expected tree before changing any returned metadata.
	want = ownershipJSONCopy(t, want)
	beforeBudget := ownershipJSONCopy(t, f.budget.Snapshot())
	ownershipEqual(t, "loaded snapshot", f.manager.Snapshot(), want)
	ownershipEqual(t, "handle key", h.Key(), f.key)
	ownershipEqual(t, "handle spec", h.Spec(), wantSpec)
	ownershipEqual(t, "handle estimate", h.Estimate(), wantEstimate)
	spec, est, snap := h.Spec(), h.Estimate(), f.manager.Snapshot()
	ownershipPoisonProfile(&spec.Profile)
	spec.Key.ModelID, spec.Workload.Width = "edited", 2048
	*est.Provenance.ObservedPeakRAMBytes = 1
	est.Request = ownershipRequest(1)
	est.Key.ModelRevision, est.Provenance.ProfileRevision = "edited", "edited"
	ownershipPoisonCaps(&snap.Models[0].Capabilities)
	ownershipPoisonProfile(&snap.Models[0].Spec.Profile)
	snap.Models[0].Spec.Key.ModelID, snap.Models[0].Spec.Workload.Width = "edited", 2048
	*snap.Models[0].Estimate.Provenance.ObservedPeakRAMBytes = 2
	snap.Models[0].Estimate.Request.RAMBytes = 2
	snap.Models[0].Reservation.RAMBytes = 2
	*snap.Models[0].Observations.LoadMs = 999
	ownershipEqual(t, "frozen full snapshot", f.manager.Snapshot(), want)
	ownershipEqual(t, "frozen handle spec", h.Spec(), wantSpec)
	ownershipEqual(t, "frozen handle estimate", h.Estimate(), wantEstimate)
	ownershipEqual(t, "authoritative allocation", f.budget.Snapshot(), beforeBudget)
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	changed := f.w
	changed.Width = 128 // Supported, but not the exact loaded workload.
	for _, tc := range []struct {
		name, profile string
		w             domain.Workload
	}{{"profile", "alternate", f.w}, {"exact_workload", "preview", changed}} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := f.manager.Acquire(context.Background(), f.key, tc.profile, tc.w); got != nil || !errors.Is(err, model.ErrNeedsUnload) {
				t.Fatalf("changed loaded identity = %v, %v", got, err)
			}
			ownershipHeld(t, f, model.StateIdle, 250_000_000)
			ownershipEqual(t, "unchanged allocation", f.budget.Snapshot(), beforeBudget)
		})
	}
	reused := ownershipAcquire(t, f)
	if loads, unloads := f.loader.counts(); loads != 1 || unloads != 0 || f.budget.count() != 1 {
		t.Fatal("metadata edits or idle reuse triggered another lifecycle/reservation")
	}
	ownershipEqual(t, "reuse exact spec", reused.Spec(), wantSpec)
	if err := reused.Release(); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Unload(context.Background(), f.key); err != nil {
		t.Fatal(err)
	}
	ownershipRefunded(t, f)
}

func ownershipIntRequest(ram int64) *resource.Request { r := ownershipRequest(ram); return &r }

func TestOwnershipHistoryRaisesIdleReservation(t *testing.T) {
	f := ownershipNew(t, 0)
	h := ownershipAcquire(t, f)
	if h.Estimate().Provenance.ObservedPeakRAMBytes != nil {
		t.Fatal("unknown peak became a known zero")
	}
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	key := ownershipKey()
	// Nearby keys must not bleed into this loaded instance's estimate.
	neighbors := make([]resource.HistoryKey, 7)
	for i := range neighbors {
		neighbors[i] = key
	}
	neighbors[0].ProviderID = "other-provider"
	neighbors[1].ModelID = "other-model"
	neighbors[2].ModelRevision = "model-r2"
	neighbors[3].ProfileID = "alternate"
	neighbors[4].ProfileRevision = "profile-r2"
	neighbors[5].HardwareClass = "other-hardware"
	neighbors[6].Workload.Frames = 3
	for _, k := range neighbors {
		if err := f.history.History.Record(k, resource.Measurement{FamilyRSS: ownershipPeak(600_000_000)}); err != nil {
			t.Fatal(err)
		}
	}
	reused := ownershipAcquire(t, f)
	if reused.Estimate().Request != ownershipRequest(200_000_000) || reused.Estimate().Key != key || reused.Estimate().Provenance.ObservedPeakRAMBytes != nil {
		t.Fatalf("history key scope leaked: %+v", reused.Estimate())
	}
	if err := reused.Release(); err != nil {
		t.Fatal(err)
	}
	before := ownershipJSONCopy(t, f.manager.Snapshot())
	allocation := ownershipJSONCopy(t, f.budget.Snapshot())
	if err := f.history.History.Record(key, resource.Measurement{FamilyRSS: ownershipPeak(200_000_000)}); err != nil {
		t.Fatal(err)
	}
	if got, err := f.manager.Acquire(context.Background(), f.key, "preview", f.w); got != nil || !errors.Is(err, model.ErrNeedsUnload) {
		t.Fatalf("raised estimate must require unload: %v, %v", got, err)
	}
	ownershipEqual(t, "idle instance preserved", f.manager.Snapshot(), before)
	ownershipEqual(t, "no nested reservation", f.budget.Snapshot(), allocation)
	if f.budget.count() != 1 {
		t.Fatal("idle growth attempted another budget Acquire")
	}
	if err := f.manager.Unload(context.Background(), f.key); err != nil {
		t.Fatal(err)
	}
	ownershipRefunded(t, f)
	fresh := ownershipAcquire(t, f)
	ownershipHeld(t, f, model.StateInUse, 250_000_000)
	if fresh.Estimate().Key != key || fresh.Estimate().Provenance.ObservedPeakRAMBytes == nil || *fresh.Estimate().Provenance.ObservedPeakRAMBytes != 200_000_000 {
		t.Fatalf("reloaded estimate ignored exact high water: %+v", fresh.Estimate())
	}
	ownershipEqual(t, "manager record keys", f.history.recordedKeys(), []resource.HistoryKey{key, key, key})
	if len(f.history.Snapshot().Entries) != 8 || f.budget.count() != 2 {
		t.Fatal("history entries or fresh allocation count differs")
	}
	if err := fresh.Release(); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Unload(context.Background(), f.key); err != nil {
		t.Fatal(err)
	}
	ownershipRefunded(t, f)
}

func TestOwnershipMeasurementAndHistoryFailures(t *testing.T) {
	t.Run("validation_copy_high_water_and_stale", func(t *testing.T) {
		f := ownershipNew(t, 0)
		h := ownershipAcquire(t, f)
		initial := f.manager.Snapshot().Models[0].Observations
		if initial.FamilyRSS != nil || initial.ProcessPeakRSS != nil || initial.RunMs != nil || initial.UnloadMs != nil || initial.LoadMs == nil || *initial.LoadMs != 7 {
			t.Fatalf("unknown measurements invented values: %+v", initial)
		}
		invalid := []resource.Measurement{
			{FamilyRSS: &resource.MemoryPeak{Bytes: 1, Source: resource.SourceOSProcessPeak, Completeness: resource.MeasurementComplete}},
			{ProcessPeakRSS: ownershipPeak(1)},
			{FamilyRSS: ownershipPeak(-1)},
			{RunMs: ownershipInt(-1), TimingSource: resource.SourceParentClock, TimingCompleteness: resource.MeasurementComplete},
			{RunMs: ownershipInt(1), TimingSource: "worker_clock", TimingCompleteness: resource.MeasurementComplete},
			{TimingSource: resource.SourceParentClock, TimingCompleteness: resource.MeasurementComplete},
		}
		for i, measurement := range invalid {
			before, hs, bs := ownershipJSONCopy(t, f.manager.Snapshot()), f.history.Snapshot(), f.budget.Snapshot()
			if err := h.RecordMeasurement(measurement); !errors.Is(err, model.ErrHistoryRecord) || !errors.Is(err, resource.ErrInvalidMeasurement) {
				t.Fatalf("invalid measurement %d = %v", i, err)
			}
			ownershipEqual(t, "invalid model unchanged", f.manager.Snapshot(), before)
			ownershipEqual(t, "invalid history unchanged", f.history.Snapshot(), hs)
			ownershipEqual(t, "invalid allocation unchanged", f.budget.Snapshot(), bs)
		}
		measurement := resource.Measurement{
			FamilyRSS:      ownershipPeak(150_000_000),
			ProcessPeakRSS: &resource.MemoryPeak{Bytes: 140_000_000, Source: resource.SourceOSProcessPeak, Completeness: resource.MeasurementPartial},
			RunMs:          ownershipInt(11), TimingSource: resource.SourceParentClock, TimingCompleteness: resource.MeasurementPartial,
		}
		want := ownershipJSONCopy(t, measurement)
		want.LoadMs = ownershipInt(7)
		if err := h.RecordMeasurement(measurement); err != nil {
			t.Fatal(err)
		}
		measurement.FamilyRSS.Bytes, measurement.ProcessPeakRSS.Bytes, *measurement.RunMs = 1, 1, 999
		ownershipEqual(t, "owned observation", f.manager.Snapshot().Models[0].Observations, want)
		owned := f.manager.Snapshot()
		owned.Models[0].Observations.FamilyRSS.Bytes = 1
		owned.Models[0].Observations.ProcessPeakRSS.Bytes = 1
		*owned.Models[0].Observations.RunMs = 999
		ownershipEqual(t, "returned observation isolation", f.manager.Snapshot().Models[0].Observations, want)
		for _, sparse := range []resource.Measurement{{}, {
			FamilyRSS: ownershipPeak(10), ProcessPeakRSS: &resource.MemoryPeak{Bytes: 20, Source: resource.SourceOSProcessPeak, Completeness: resource.MeasurementComplete},
		}, {RunMs: ownershipInt(0), TimingSource: resource.SourceParentClock, TimingCompleteness: resource.MeasurementComplete}} {
			if err := h.RecordMeasurement(sparse); err != nil {
				t.Fatal(err)
			}
		}
		want.RunMs = ownershipInt(0) // Known zero is distinct from unknown.
		ownershipEqual(t, "model retains peaks and sparse known timings", f.manager.Snapshot().Models[0].Observations, want)
		summary, ok := f.history.History.Lookup(ownershipKey())
		if !ok || summary.Samples != 5 {
			t.Fatalf("exact scoped history samples = %+v, %v", summary, ok)
		}
		ownershipEqual(t, "history retains peaks", summary.Measurement, want)
		if err := h.Release(); err != nil {
			t.Fatal(err)
		}
		current := ownershipAcquire(t, f)
		before, hs := ownershipJSONCopy(t, f.manager.Snapshot()), f.history.Snapshot()
		if err := h.RecordMeasurement(resource.Measurement{FamilyRSS: ownershipPeak(1)}); !errors.Is(err, model.ErrReleased) {
			t.Fatalf("stale measurement = %v", err)
		}
		if err := h.Release(); err != nil {
			t.Fatal(err)
		}
		ownershipEqual(t, "stale handle cannot clear new owner", f.manager.Snapshot(), before)
		ownershipEqual(t, "stale measurement not recorded", f.history.Snapshot(), hs)
		if err := current.Release(); err != nil {
			t.Fatal(err)
		}
		if err := current.RecordMeasurement(resource.Measurement{}); !errors.Is(err, model.ErrReleased) {
			t.Fatalf("released measurement = %v", err)
		}
		if err := f.manager.Unload(context.Background(), f.key); err != nil {
			t.Fatal(err)
		}
		ownershipRefunded(t, f)
	})
	t.Run("full_history_is_advisory_and_verified_stop_refunds", func(t *testing.T) {
		f := ownershipNew(t, 1)
		other := ownershipKey()
		other.ModelID = "retained-other-model"
		if err := f.history.History.Record(other, resource.Measurement{FamilyRSS: ownershipPeak(900_000_000)}); err != nil {
			t.Fatal(err)
		}
		retained := ownershipJSONCopy(t, f.history.Snapshot())
		h := ownershipAcquire(t, f)
		if err := h.ObservationError(); !errors.Is(err, model.ErrHistoryRecord) || !errors.Is(err, resource.ErrHistoryFull) {
			t.Fatalf("load advisory error = %v", err)
		}
		ownershipHeld(t, f, model.StateInUse, 200_000_000)
		if f.manager.Snapshot().Models[0].HistoryFailures != 1 {
			t.Fatal("successful load history failure not counted")
		}
		if err := h.RecordMeasurement(resource.Measurement{FamilyRSS: ownershipPeak(150_000_000)}); !errors.Is(err, model.ErrHistoryRecord) || !errors.Is(err, resource.ErrHistoryFull) {
			t.Fatalf("measurement capacity error = %v", err)
		}
		s := f.manager.Snapshot().Models[0]
		if s.HistoryFailures != 2 || s.LastCode != model.CodeHistoryRecord || s.Observations.FamilyRSS == nil || s.Observations.FamilyRSS.Bytes != 150_000_000 {
			t.Fatalf("rejected history lost local evidence/error: %+v", s)
		}
		ownershipHeld(t, f, model.StateInUse, 200_000_000)
		ownershipEqual(t, "no eviction", f.history.Snapshot(), retained)
		if err := h.Release(); err != nil {
			t.Fatal(err)
		}
		advisory := errors.New("adapter stopped with advisory error")
		f.loader.onUnload = func(context.Context) (bool, error) { return true, advisory }
		err := f.manager.Unload(context.Background(), f.key)
		if !errors.Is(err, advisory) || !errors.Is(err, model.ErrHistoryRecord) || !errors.Is(err, resource.ErrHistoryFull) {
			t.Fatalf("verified-stop advisory/record causes = %v", err)
		}
		ownershipRefunded(t, f)
		s = f.manager.Snapshot().Models[0]
		if s.HistoryFailures != 3 || s.Observations.UnloadMs == nil || *s.Observations.UnloadMs != 7 || s.Observations.TimingCompleteness != resource.MeasurementPartial {
			t.Fatalf("unload history failure/timing lost: %+v", s)
		}
		ownershipEqual(t, "retained high water after unload", f.history.Snapshot(), retained)
		ownershipEqual(t, "failed record exact keys", f.history.recordedKeys(), []resource.HistoryKey{ownershipKey(), ownershipKey(), ownershipKey()})
	})
}

func ownershipAwait[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case result := <-ch:
		return result
	case <-time.After(3 * time.Second):
		t.Fatal("bounded concurrent operation did not finish (possible callback-under-lock deadlock)")
		var zero T
		return zero
	}
}

type ownershipAcquireResult struct {
	h   *model.UseHandle
	err error
}

func ownershipBusyConcurrent(t *testing.T, f ownershipFixture, ctx context.Context) {
	t.Helper()
	a, u := make(chan ownershipAcquireResult, 1), make(chan error, 1)
	go func() { h, err := f.manager.Acquire(ctx, f.key, "preview", f.w); a <- ownershipAcquireResult{h, err} }()
	go func() { u <- f.manager.Unload(ctx, f.key) }()
	r := ownershipAwait(t, a)
	if r.h != nil || !errors.Is(r.err, model.ErrBusy) || !errors.Is(ownershipAwait(t, u), model.ErrBusy) {
		t.Fatalf("conflicting operation did not return Busy: %+v", r)
	}
}

func TestOwnershipConcurrentLifecycleCallbacksOutsideLocks(t *testing.T) {
	f := ownershipNew(t, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	enteredLoad, enteredUnload := make(chan struct{}), make(chan struct{})
	resumeLoad, resumeUnload := make(chan struct{}), make(chan struct{})
	var loadOnce, unloadOnce sync.Once
	t.Cleanup(func() {
		loadOnce.Do(func() { close(resumeLoad) })
		unloadOnce.Do(func() { close(resumeUnload) })
	})
	check := func(state model.State) error {
		// Reentrant calls must complete BEFORE signalling the fake barrier.
		s := f.manager.Snapshot()
		if len(s.Models) != 1 || s.Models[0].State != state {
			return fmt.Errorf("callback state = %+v, want %s", s, state)
		}
		return ownershipHeldError(f.budget.Snapshot(), 200_000_000)
	}
	f.loader.onLoad = func(ctx context.Context, _ model.LoadSpec) error {
		if err := check(model.StateLoading); err != nil {
			return err
		}
		close(enteredLoad)
		select {
		case <-resumeLoad:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	f.loader.onUnload = func(ctx context.Context) (bool, error) {
		if err := check(model.StateUnloading); err != nil {
			return false, err
		}
		close(enteredUnload)
		select {
		case <-resumeUnload:
			return true, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	loaded := make(chan ownershipAcquireResult, 1)
	go func() {
		h, err := f.manager.Acquire(ctx, f.key, "preview", f.w)
		loaded <- ownershipAcquireResult{h, err}
	}()
	ownershipAwait(t, enteredLoad)
	ownershipHeld(t, f, model.StateLoading, 200_000_000)
	allocation := ownershipJSONCopy(t, f.budget.Snapshot())
	ownershipBusyConcurrent(t, f, ctx)
	ownershipEqual(t, "loading conflicts retain allocation", f.budget.Snapshot(), allocation)
	loadOnce.Do(func() { close(resumeLoad) })
	r := ownershipAwait(t, loaded)
	if r.err != nil || r.h == nil {
		t.Fatalf("barrier load = %+v", r)
	}
	if err := r.h.Release(); err != nil {
		t.Fatal(err)
	}
	unloaded := make(chan error, 1)
	go func() { unloaded <- f.manager.Unload(ctx, f.key) }()
	ownershipAwait(t, enteredUnload)
	ownershipHeld(t, f, model.StateUnloading, 200_000_000)
	ownershipBusyConcurrent(t, f, ctx)
	ownershipEqual(t, "unloading conflicts retain allocation", f.budget.Snapshot(), allocation)
	unloadOnce.Do(func() { close(resumeUnload) })
	if err := ownershipAwait(t, unloaded); err != nil {
		t.Fatal(err)
	}
	ownershipRefunded(t, f)
	if loads, unloads := f.loader.counts(); loads != 1 || unloads != 1 || f.budget.count() != 1 {
		t.Fatalf("conflicts triggered callbacks/acquisition: %d/%d/%d", loads, unloads, f.budget.count())
	}
}

func ownershipReleaseWave(t *testing.T, f ownershipFixture, h, copied *model.UseHandle, stale bool) {
	t.Helper()
	start, results := make(chan struct{}), make(chan error, 17)
	for i := 0; i < 16; i++ {
		use := h
		if i%2 != 0 {
			use = copied
		}
		go func() {
			<-start
			for j := 0; j < 8; j++ {
				if err := use.Release(); err != nil {
					results <- err
					return
				}
				// Egress metadata remains safe while copies share release ownership.
				spec := use.Spec()
				ownershipPoisonProfile(&spec.Profile)
				_ = use.Estimate()
			}
			results <- nil
		}()
	}
	go func() {
		<-start
		for i := 0; i < 128; i++ {
			s := f.manager.Snapshot()
			v := s.Models[0]
			if v.State != model.StateInUse && (stale || v.State != model.StateIdle) {
				results <- fmt.Errorf("release changed wrong owner state: %s", v.State)
				return
			}
			if v.Spec == nil || v.Reservation == nil || *v.Reservation != ownershipRequest(200_000_000) || !reflect.DeepEqual(v.Spec.Profile, ownershipProfile("preview")) {
				results <- errors.New("concurrent snapshot lost or aliased admitted metadata")
				return
			}
			if err := ownershipHeldError(f.budget.Snapshot(), 200_000_000); err != nil {
				results <- err
				return
			}
			ownershipPoisonCaps(&s.Models[0].Capabilities)
			ownershipPoisonProfile(&s.Models[0].Spec.Profile)
			*s.Models[0].Observations.LoadMs = 999
		}
		results <- nil
	}()
	close(start)
	for i := 0; i < 17; i++ {
		if err := ownershipAwait(t, results); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOwnershipConcurrentRepeatedAndStaleRelease(t *testing.T) {
	f := ownershipNew(t, 0)
	h := ownershipAcquire(t, f)
	copied := *h
	allocation := ownershipJSONCopy(t, f.budget.Snapshot())
	ownershipReleaseWave(t, f, h, &copied, false)
	ownershipHeld(t, f, model.StateIdle, 200_000_000)
	current := ownershipAcquire(t, f)
	ownershipReleaseWave(t, f, h, &copied, true)
	ownershipHeld(t, f, model.StateInUse, 200_000_000)
	ownershipBusyConcurrent(t, f, context.Background())
	ownershipEqual(t, "release never refunds/reacquires", f.budget.Snapshot(), allocation)
	if loads, unloads := f.loader.counts(); loads != 1 || unloads != 0 || f.budget.count() != 1 {
		t.Fatal("repeated/copied/stale release changed lifecycle or reservation")
	}
	if err := current.Release(); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.Unload(context.Background(), f.key); err != nil {
		t.Fatal(err)
	}
	ownershipRefunded(t, f)
}
