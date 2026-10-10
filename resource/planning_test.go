package resource

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/provider"
)

func testWorkload() domain.Workload {
	return domain.Workload{Precision: "fp16", Width: 640, Height: 480, Frames: 48, Quality: "preview", Offload: "none", ChunkSize: 0}
}
func testCapabilities() domain.ModelCapabilities {
	return domain.ModelCapabilities{ModelID: "model", ProviderID: "local", Tags: []string{"video", "motion"}, Local: true, MinMemoryGB: 1.1,
		Requirements: &domain.ModelRequirements{Version: 1, Revision: "weights-v1", ExecutionProfiles: []domain.ExecutionProfile{
			{ID: "preview", Revision: "bounds-v2", Trusted: true, MemoryKnown: true, LoadRAMBytes: 2 * GB, ResidentRAMBytes: GB, WorkspaceRAMBytes: 500_000_000, CPU: 1, GPU: .5, Backend: domain.BackendMetal,
				Envelope: domain.WorkloadEnvelope{Precisions: []string{"fp16", "int8"}, Width: domain.IntRange{Min: 320, Max: 640}, Height: domain.IntRange{Min: 240, Max: 480}, Frames: domain.IntRange{Min: 1, Max: 48}, Qualities: []string{"preview"}, Offloads: []string{"none", "cpu"}, ChunkSize: domain.IntRange{Min: 0, Max: 8}}},
		}}}
}
func keyForWorkload(w domain.Workload) HistoryKey {
	return HistoryKey{ProviderID: "local", ModelID: "model", ModelRevision: "weights-v1", ProfileID: "preview", ProfileRevision: "bounds-v2", HardwareClass: "test-unified", Workload: w}
}
func i64(v int64) *int64 { return &v }
func peak(v int64) *MemoryPeak {
	return &MemoryPeak{Bytes: v, Source: SourceNativeFamilyRSS, Completeness: MeasurementPartial}
}
func mustHistory(t *testing.T, n int) *History {
	t.Helper()
	h, e := NewHistory(n)
	if e != nil {
		t.Fatal(e)
	}
	return h
}
func mustRecord(t *testing.T, h *History, k HistoryKey, m Measurement) {
	t.Helper()
	if err := h.Record(k, m); err != nil {
		t.Fatal(err)
	}
}

func TestProfileCapsCloneHasIndependentOracle(t *testing.T) {
	c := testCapabilities()
	frozen := CloneCapabilities(c)
	c.Tags[0] = "bad"
	c.Requirements.Revision = "bad"
	c.Requirements.ExecutionProfiles[0].LoadRAMBytes = 1
	c.Requirements.ExecutionProfiles[0].Envelope.Precisions[0] = "bad"
	c.Requirements.ExecutionProfiles[0].Envelope.Qualities[0] = "bad"
	c.Requirements.ExecutionProfiles[0].Envelope.Offloads[0] = "bad"
	if frozen.Tags[0] != "video" || frozen.Requirements.Revision != "weights-v1" || frozen.Requirements.ExecutionProfiles[0].LoadRAMBytes != 2_000_000_000 || !reflect.DeepEqual(frozen.Requirements.ExecutionProfiles[0].Envelope.Precisions, []string{"fp16", "int8"}) || !reflect.DeepEqual(frozen.Requirements.ExecutionProfiles[0].Envelope.Qualities, []string{"preview"}) || !reflect.DeepEqual(frozen.Requirements.ExecutionProfiles[0].Envelope.Offloads, []string{"none", "cpu"}) {
		t.Fatalf("clone aliases caller: %+v", frozen)
	}
	p, err := Profile(frozen, "preview")
	if err != nil {
		t.Fatal(err)
	}
	p.Envelope.Precisions[0] = "again"
	p.Envelope.Qualities[0] = "again"
	p.Envelope.Offloads[0] = "again"
	if frozen.Requirements.ExecutionProfiles[0].Envelope.Precisions[0] != "fp16" || frozen.Requirements.ExecutionProfiles[0].Envelope.Qualities[0] != "preview" || frozen.Requirements.ExecutionProfiles[0].Envelope.Offloads[0] != "none" {
		t.Fatal("profile return aliases frozen caps")
	}
	if _, err := Profile(frozen, "unknown"); !errors.Is(err, ErrLocalIneligible) {
		t.Fatalf("substituted unknown profile: %v", err)
	}
	encoded, err := json.Marshal(frozen)
	if err != nil {
		t.Fatal(err)
	}
	var decoded domain.ModelCapabilities
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, frozen) {
		t.Fatal("requirements JSON roundtrip")
	}
}

func TestSupportedEnvelopeAndEveryLimit(t *testing.T) {
	p := testCapabilities().Requirements.ExecutionProfiles[0]
	w := testWorkload()
	if err := SupportsWorkload(p, w); err != nil {
		t.Fatal(err)
	}
	variations := []struct {
		name   string
		mutate func(*domain.Workload)
	}{
		{"precision", func(w *domain.Workload) { w.Precision = "fp32" }}, {"quality", func(w *domain.Workload) { w.Quality = "final" }},
		{"offload", func(w *domain.Workload) { w.Offload = "disk" }}, {"width lower", func(w *domain.Workload) { w.Width = 319 }},
		{"width upper", func(w *domain.Workload) { w.Width = 641 }}, {"height lower", func(w *domain.Workload) { w.Height = 239 }},
		{"height upper", func(w *domain.Workload) { w.Height = 481 }}, {"frames lower", func(w *domain.Workload) { w.Frames = 0 }},
		{"frames upper", func(w *domain.Workload) { w.Frames = 49 }}, {"chunk upper", func(w *domain.Workload) { w.ChunkSize = 9 }},
	}
	for _, v := range variations {
		t.Run(v.name, func(t *testing.T) {
			bad := w
			v.mutate(&bad)
			if err := SupportsWorkload(p, bad); !errors.Is(err, ErrLocalIneligible) {
				t.Fatalf("accepted unsupported: %+v %v", bad, err)
			}
		})
	}
	for _, change := range []func(*domain.ExecutionProfile){func(p *domain.ExecutionProfile) { p.Trusted = false }, func(p *domain.ExecutionProfile) { p.MemoryKnown = false }} {
		bad := CloneProfile(p)
		change(&bad)
		if err := SupportsWorkload(bad, w); !errors.Is(err, ErrLocalIneligible) {
			t.Fatalf("unknown trusted bounds admitted: %v", err)
		}
	}
	p.Envelope.Width = domain.IntRange{}
	p.Envelope.Height = domain.IntRange{}
	p.Envelope.Frames = domain.IntRange{}
	w.Width = 0
	w.Height = 0
	w.Frames = 0
	if err := SupportsWorkload(p, w); err != nil {
		t.Fatalf("explicit nonvisual envelope: %v", err)
	}
	for _, bad := range []domain.Workload{{}, {Precision: "fp16", Quality: "preview", Offload: "none", Width: -1}, {Precision: "fp16", Quality: "preview", Offload: "none", ChunkSize: -1}} {
		if ValidateWorkload(bad) == nil {
			t.Fatal("invalid workload accepted")
		}
	}
	w = testWorkload()
	w.Precision = string([]byte{0xff})
	if ValidateWorkload(w) == nil {
		t.Fatal("non-round-trippable UTF-8 workload accepted")
	}
}

func TestInvalidCapabilitiesAndBoundsFailClosed(t *testing.T) {
	mutations := []struct {
		name   string
		change func(*domain.ModelCapabilities)
	}{
		{"identity", func(c *domain.ModelCapabilities) { c.ModelID = " " }}, {"floor nan", func(c *domain.ModelCapabilities) { c.MinMemoryGB = math.NaN() }},
		{"floor infinity", func(c *domain.ModelCapabilities) { c.MinMemoryGB = math.Inf(1) }}, {"floor overflow", func(c *domain.ModelCapabilities) { c.MinMemoryGB = 1e20 }},
		{"version", func(c *domain.ModelCapabilities) { c.Requirements.Version = 2 }}, {"revision", func(c *domain.ModelCapabilities) { c.Requirements.Revision = "" }},
		{"empty profiles", func(c *domain.ModelCapabilities) { c.Requirements.ExecutionProfiles = nil }}, {"duplicate", func(c *domain.ModelCapabilities) {
			c.Requirements.ExecutionProfiles = append(c.Requirements.ExecutionProfiles, c.Requirements.ExecutionProfiles[0])
		}},
		{"negative bound", func(c *domain.ModelCapabilities) { c.Requirements.ExecutionProfiles[0].LoadRAMBytes = -1 }},
		{"bound addition overflow", func(c *domain.ModelCapabilities) {
			c.Requirements.ExecutionProfiles[0].ResidentRAMBytes = math.MaxInt64
		}},
		{"known zero", func(c *domain.ModelCapabilities) {
			p := &c.Requirements.ExecutionProfiles[0]
			p.LoadRAMBytes = 0
			p.ResidentRAMBytes = 0
			p.WorkspaceRAMBytes = 0
		}},
		{"cpu nan", func(c *domain.ModelCapabilities) { c.Requirements.ExecutionProfiles[0].CPU = math.NaN() }},
		{"gpu overflow", func(c *domain.ModelCapabilities) { c.Requirements.ExecutionProfiles[0].GPU = 1e20 }},
		{"metal without gpu", func(c *domain.ModelCapabilities) { c.Requirements.ExecutionProfiles[0].GPU = 0 }},
		{"gpu without backend", func(c *domain.ModelCapabilities) { c.Requirements.ExecutionProfiles[0].Backend = "" }},
		{"unknown range", func(c *domain.ModelCapabilities) { c.Requirements.ExecutionProfiles[0].Envelope.Width.Max = -1 }},
		{"missing precision", func(c *domain.ModelCapabilities) { c.Requirements.ExecutionProfiles[0].Envelope.Precisions = nil }},
		{"duplicate offload", func(c *domain.ModelCapabilities) {
			c.Requirements.ExecutionProfiles[0].Envelope.Offloads = []string{"none", "none"}
		}},
	}
	for _, v := range mutations {
		t.Run(v.name, func(t *testing.T) {
			c := testCapabilities()
			v.change(&c)
			if err := ValidateCapabilities(c); !errors.Is(err, ErrInvalidProfile) {
				t.Fatalf("accepted malformed declaration: %v", err)
			}
		})
	}
	legacy := testCapabilities()
	legacy.Requirements = nil
	if err := ValidateCapabilities(legacy); err != nil {
		t.Fatalf("legacy metadata compatibility: %v", err)
	}
	if _, err := EstimateRequest(legacy, "preview", testWorkload(), testConfig(), HistorySnapshot{}, 25); !errors.Is(err, ErrLocalIneligible) {
		t.Fatalf("legacy minimum treated as peak: %v", err)
	}
}

func TestEstimateIndependentPeakFloorAndIntegerMarginOracle(t *testing.T) {
	c := testCapabilities()
	w := testWorkload()
	cfg := testConfig()
	est, err := EstimateRequest(c, "preview", w, cfg, HistorySnapshot{}, 25)
	if err != nil {
		t.Fatal(err)
	}
	if est.Request != (Request{RAMBytes: 2_500_000_000, CPU: 1, GPU: .5, Heavy: true, Backend: domain.BackendMetal}) || est.Provenance.DeclaredPeakRAMBytes != 2_000_000_000 || est.Provenance.LegacyFloorRAMBytes != 1_100_000_000 || est.Key != keyForWorkload(w) {
		t.Fatalf("independent estimate: %+v", est)
	}
	c.Requirements.ExecutionProfiles[0].LoadRAMBytes = 1
	c.Requirements.ExecutionProfiles[0].ResidentRAMBytes = 2_000_000_000
	c.Requirements.ExecutionProfiles[0].WorkspaceRAMBytes = 1
	est, err = EstimateRequest(c, "preview", w, cfg, HistorySnapshot{}, 25)
	if err != nil || est.Request.RAMBytes != 2_500_000_002 {
		t.Fatalf("workspace/safety ceil oracle: %+v %v", est, err)
	}
	c.MinMemoryGB = 3
	est, err = EstimateRequest(c, "preview", w, cfg, HistorySnapshot{}, 25)
	if err != nil || est.Request.RAMBytes != 3_750_000_000 {
		t.Fatalf("legacy floor oracle: %+v %v", est, err)
	}
	c.MinMemoryGB = 0.0000000011
	c.Requirements.ExecutionProfiles[0].ResidentRAMBytes = 1
	c.Requirements.ExecutionProfiles[0].WorkspaceRAMBytes = 0
	est, err = EstimateRequest(c, "preview", w, cfg, HistorySnapshot{}, 0)
	if err != nil || est.Request.RAMBytes != 2 {
		t.Fatalf("legacy ceil byte oracle: %+v %v", est, err)
	}
	for _, tc := range []struct{ b, p, w int64 }{{1, 25, 2}, {100, 25, 125}, {101, 25, 127}, {math.MaxInt64, 0, math.MaxInt64}, {0, math.MaxInt64, 0}, {4, 50, 6}} {
		got, e := ApplySafetyMargin(tc.b, tc.p)
		if e != nil || got != tc.w {
			t.Errorf("margin(%d,%d)=%d,%v want %d", tc.b, tc.p, got, e, tc.w)
		}
	}
	if _, err := ApplySafetyMargin(math.MaxInt64, 1); !errors.Is(err, ErrOverflow) {
		t.Fatal("margin overflow accepted")
	}
	if _, err := ApplySafetyMargin(100, math.MaxInt64); !errors.Is(err, ErrOverflow) {
		t.Fatal("percent overflow accepted")
	}
	if _, err := ApplySafetyMargin(1, -1); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("negative margin")
	}
	c = testCapabilities()
	c.Requirements.ExecutionProfiles[0].LoadRAMBytes = math.MaxInt64
	if _, err := EstimateRequest(c, "preview", w, cfg, HistorySnapshot{}, 25); !errors.Is(err, ErrOverflow) {
		t.Fatal("estimate overflow")
	}
	c = testCapabilities()
	cfg.Accelerator.Backend = domain.BackendCUDA
	if _, err := EstimateRequest(c, "preview", w, cfg, HistorySnapshot{}, 25); !errors.Is(err, ErrIncompatible) {
		t.Fatal("Metal routed to CUDA")
	}
	cfg = testConfig()
	cfg.Accelerator = AcceleratorFacts{}
	cfg.WorkerGPU = 0
	if _, err := EstimateRequest(c, "preview", w, cfg, HistorySnapshot{}, 25); !errors.Is(err, ErrIncompatible) {
		t.Fatal("unknown accelerator admitted")
	}
}

func TestHistoryBoundedCopiedExactKeysAndProvenance(t *testing.T) {
	h := mustHistory(t, 2)
	w := testWorkload()
	key := keyForWorkload(w)
	m := Measurement{FamilyRSS: peak(3 * GB), ProcessPeakRSS: &MemoryPeak{Bytes: 2 * GB, Source: SourceOSProcessPeak, Completeness: MeasurementComplete}, LoadMs: i64(0), RunMs: i64(10), TimingSource: SourceParentClock, TimingCompleteness: MeasurementPartial}
	mustRecord(t, h, key, m)
	m.FamilyRSS.Bytes = 1
	m.ProcessPeakRSS.Bytes = 1
	*m.LoadMs = 999
	*m.RunMs = 999
	s := h.Snapshot()
	if s.Version != 1 || s.Hash == "" || len(s.Entries) != 1 {
		t.Fatalf("history snapshot: %+v", s)
	}
	entry := s.Entries[0]
	if entry.Measurement.FamilyRSS.Bytes != 3_000_000_000 || entry.Measurement.FamilyRSS.Source != SourceNativeFamilyRSS || entry.Measurement.ProcessPeakRSS.Bytes != 2_000_000_000 || entry.Measurement.ProcessPeakRSS.Source != SourceOSProcessPeak || *entry.Measurement.LoadMs != 0 || *entry.Measurement.RunMs != 10 || entry.Measurement.UnloadMs != nil {
		t.Fatal("measurement ingress alias or unknown converted to zero")
	}
	est, err := EstimateRequest(testCapabilities(), "preview", w, testConfig(), s, 25)
	if err != nil || est.Request.RAMBytes != 3_750_000_000 || *est.Provenance.ObservedPeakRAMBytes != 3_000_000_000 || est.Provenance.HistoryVersion != 1 || est.Provenance.HistoryHash != s.Hash {
		t.Fatalf("historical raise/provenance: %+v %v", est, err)
	}
	s.Entries[0].Measurement.FamilyRSS.Bytes = 999
	s.Entries[0].Key.ModelID = "tampered"
	lookup, ok := h.Lookup(key)
	if !ok || lookup.Measurement.FamilyRSS.Bytes != 3_000_000_000 {
		t.Fatal("snapshot egress alias")
	}
	lookup.Measurement.ProcessPeakRSS.Bytes = 999
	*lookup.Measurement.RunMs = 999
	lookup, _ = h.Lookup(key)
	if lookup.Measurement.ProcessPeakRSS.Bytes != 2_000_000_000 || *lookup.Measurement.RunMs != 10 {
		t.Fatal("lookup egress alias")
	}
	mustRecord(t, h, key, Measurement{FamilyRSS: peak(1 * GB)})
	mustRecord(t, h, key, Measurement{})
	est, err = EstimateRequest(testCapabilities(), "preview", w, testConfig(), h.Snapshot(), 25)
	if err != nil || est.Request.RAMBytes != 3_750_000_000 {
		t.Fatalf("sparse history lowered reservation: %+v %v", est, err)
	}
	other := key
	other.ModelRevision = "weights-v2"
	mustRecord(t, h, other, Measurement{})
	third := key
	third.ProfileRevision = "bounds-v3"
	beforeFull := h.Snapshot()
	if err := h.Record(third, Measurement{}); !errors.Is(err, ErrHistoryFull) {
		t.Errorf("full history accepted a new empty-measurement key: %v", err)
	}
	if !reflect.DeepEqual(h.Snapshot(), beforeFull) || len(h.entries) != 2 || !reflect.DeepEqual(h.order, []HistoryKey{key, other}) {
		t.Error("full history evicted learned evidence or published a rejected key")
	}
	if retained, ok := h.Lookup(key); !ok || retained.Measurement.FamilyRSS.Bytes != 3_000_000_000 {
		t.Error("full history forgot learned high-water evidence")
	}
	encoded, err := json.Marshal(h.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	var decoded HistorySnapshot
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, h.Snapshot()) {
		t.Fatal("history JSON/provenance changed")
	}
	// Repeated reads are deterministic; source-owned snapshot mutation is isolated.
	if h.Snapshot().Hash != h.Snapshot().Hash {
		t.Fatal("ambient freshness/hash")
	}
}

func TestHistoryFullRetainsHighWaterReservationAtCapacity(t *testing.T) {
	h := mustHistory(t, 1)
	w := testWorkload()
	keyA := keyForWorkload(w)
	mustRecord(t, h, keyA, Measurement{FamilyRSS: peak(3 * GB)})
	initial := h.Snapshot()
	initialJSON, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if initial.Version != 1 || len(initial.Entries) != 1 || initial.Entries[0].Key != keyA || initial.Entries[0].Measurement.FamilyRSS.Bytes != 3_000_000_000 {
		t.Fatal("test did not establish the declared high-water evidence")
	}
	for i, measurement := range []Measurement{{FamilyRSS: peak(GB)}, {}} {
		keyB := keyA
		keyB.ModelRevision = []string{"new-measured", "new-empty"}[i]
		if err := h.Record(keyB, measurement); !errors.Is(err, ErrHistoryFull) {
			t.Errorf("new key %d at capacity: got %v, want ErrHistoryFull", i, err)
		}
		after := h.Snapshot()
		afterJSON, err := json.Marshal(after)
		if err != nil {
			t.Fatal(err)
		}
		if after.Version != 1 || after.Hash != initial.Hash || string(afterJSON) != string(initialJSON) {
			t.Errorf("rejected key %d changed version/hash/serialized evidence", i)
		}
		if len(h.entries) != 1 || len(h.order) != 1 || h.order[0] != keyA {
			t.Errorf("rejected key %d changed bounded map/order", i)
		}
		if _, ok := h.Lookup(keyB); ok {
			t.Errorf("rejected key %d leaked into history", i)
		}
	}
	for i, measurement := range []Measurement{{}, {FamilyRSS: peak(GB)}, {}} {
		if err := h.Record(keyA, measurement); err != nil {
			t.Fatalf("existing key update %d rejected at capacity: %v", i, err)
		}
		retained, ok := h.Lookup(keyA)
		if !ok || retained.Measurement.FamilyRSS == nil || retained.Measurement.FamilyRSS.Bytes != 3_000_000_000 {
			t.Errorf("sparse/lower update %d reduced learned high-water evidence", i)
		}
		if h.Snapshot().Version != uint64(i+2) || retained.Samples != uint64(i+2) {
			t.Errorf("existing update %d did not publish its version/sample count", i)
		}
		est, err := EstimateRequest(testCapabilities(), "preview", w, testConfig(), h.Snapshot(), 25)
		if err != nil || est.Request.RAMBytes != 3_750_000_000 {
			t.Errorf("sparse/lower update %d reduced reservation: got %d, err %v, want 3750000000", i, est.Request.RAMBytes, err)
		}
		if len(h.entries) != 1 || len(h.order) != 1 || h.order[0] != keyA {
			t.Errorf("existing update %d exceeded bounded map/order", i)
		}
	}
	mustRecord(t, h, keyA, Measurement{FamilyRSS: peak(4 * GB)})
	est, err := EstimateRequest(testCapabilities(), "preview", w, testConfig(), h.Snapshot(), 25)
	if err != nil || est.Request.RAMBytes != 5_000_000_000 || h.Snapshot().Version != 5 {
		t.Fatalf("existing high-water increase rejected at capacity: %+v, %v", est, err)
	}
}

func TestExactHistoryKeyEveryAxisAndUnknownMeasurements(t *testing.T) {
	h := mustHistory(t, 0)
	key := keyForWorkload(testWorkload())
	mustRecord(t, h, key, Measurement{FamilyRSS: peak(3 * GB)})
	variations := []func(*HistoryKey){
		func(k *HistoryKey) { k.ProviderID = "other" }, func(k *HistoryKey) { k.ModelID = "other" }, func(k *HistoryKey) { k.ModelRevision = "v2" },
		func(k *HistoryKey) { k.ProfileID = "other" }, func(k *HistoryKey) { k.ProfileRevision = "v2" }, func(k *HistoryKey) { k.HardwareClass = "other" },
		func(k *HistoryKey) { k.Workload.Precision = "int8" }, func(k *HistoryKey) { k.Workload.Width = 320 }, func(k *HistoryKey) { k.Workload.Height = 240 },
		func(k *HistoryKey) { k.Workload.Frames = 24 }, func(k *HistoryKey) { k.Workload.Quality = "final" }, func(k *HistoryKey) { k.Workload.Offload = "cpu" }, func(k *HistoryKey) { k.Workload.ChunkSize = 1 },
	}
	for i, change := range variations {
		k := key
		change(&k)
		if _, ok := h.Snapshot().Lookup(k); ok {
			t.Errorf("key axis %d reused history", i)
		}
	}
	unknown := key
	unknown.ModelID = "unknown"
	mustRecord(t, h, unknown, Measurement{})
	s, _ := h.Lookup(unknown)
	if s.Measurement.ObservedPeak() != nil || s.Measurement.LoadMs != nil || s.Measurement.RunMs != nil || s.Measurement.UnloadMs != nil {
		t.Fatal("unknown measurement became zero")
	}
	knownZero := Measurement{FamilyRSS: peak(0)}
	if knownZero.ObservedPeak() == nil || *knownZero.ObservedPeak() != 0 {
		t.Fatal("known zero lost")
	}
	for _, bad := range []Measurement{{FamilyRSS: peak(-1)}, {FamilyRSS: &MemoryPeak{Bytes: 1, Source: SourceOSProcessPeak, Completeness: MeasurementPartial}}, {ProcessPeakRSS: peak(1)}, {FamilyRSS: &MemoryPeak{Bytes: 1, Source: SourceNativeFamilyRSS}}, {RunMs: i64(-1), TimingSource: SourceParentClock, TimingCompleteness: MeasurementPartial}, {RunMs: i64(1)}, {TimingSource: SourceParentClock}} {
		before := h.Snapshot().Version
		if err := h.Record(key, bad); !errors.Is(err, ErrInvalidMeasurement) || h.Snapshot().Version != before {
			t.Errorf("invalid measurement published: %+v %v", bad, err)
		}
	}
	if h.limit != 256 {
		t.Fatal("reference history limit")
	}
	if _, err := NewHistory(-1); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("negative history limit")
	}
}

func TestOSSingleProcessEvidenceRaisesWithoutClaimingFamilyPeak(t *testing.T) {
	h := mustHistory(t, 0)
	k := keyForWorkload(testWorkload())
	mustRecord(t, h, k, Measurement{FamilyRSS: peak(3 * GB), ProcessPeakRSS: &MemoryPeak{Bytes: 4 * GB, Source: SourceOSProcessPeak, Completeness: MeasurementComplete}})
	est, err := EstimateRequest(testCapabilities(), "preview", testWorkload(), testConfig(), h.Snapshot(), 25)
	if err != nil || est.Request.RAMBytes != 5_000_000_000 || *est.Provenance.ObservedPeakRAMBytes != 4_000_000_000 {
		t.Fatalf("OS process-only evidence ignored: %+v %v", est, err)
	}
	s, _ := h.Lookup(k)
	if s.Measurement.FamilyRSS.Bytes != 3_000_000_000 || s.Measurement.FamilyRSS.Completeness != MeasurementPartial || s.Measurement.ProcessPeakRSS.Source != SourceOSProcessPeak {
		t.Fatal("OS peak relabelled as family/complete sampling")
	}
}

func TestHistoryIndependentHashAndConcurrentRecords(t *testing.T) {
	h := mustHistory(t, 4)
	key := keyForWorkload(testWorkload())
	// Fixed independent preimage, not the implementation's serialized snapshot.
	emptyHash := sha256.Sum256([]byte(`{"version":0,"entries":[]}`))
	if h.Snapshot().Hash != hex.EncodeToString(emptyHash[:]) {
		t.Fatal("initial history hash does not match canonical preimage")
	}
	if len(h.Snapshot().Entries) != 0 || h.Snapshot().Version != 0 {
		t.Fatal("initial snapshot")
	}
	start := make(chan struct{})
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); <-start; errs <- h.Record(key, Measurement{FamilyRSS: peak(int64(i))}) }(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	s := h.Snapshot()
	if s.Version != 32 || len(s.Entries) != 1 || s.Entries[0].Samples != 32 || s.Entries[0].Measurement.FamilyRSS.Bytes != 31 {
		t.Fatalf("concurrent history: %+v", s)
	}
	other := mustHistory(t, 4)
	for i := 0; i < 32; i++ {
		mustRecord(t, other, key, Measurement{FamilyRSS: peak(int64(i))})
	}
	if s.Hash != other.Snapshot().Hash {
		t.Fatal("equivalent history order changed deterministic summary hash")
	}
}

func TestHistoryOverflowRejectsWithoutPublication(t *testing.T) {
	h := mustHistory(t, 1)
	k := keyForWorkload(testWorkload())
	mustRecord(t, h, k, Measurement{FamilyRSS: peak(1)})
	h.version = math.MaxUint64
	before := h.Snapshot()
	if err := h.Record(k, Measurement{FamilyRSS: peak(2)}); !errors.Is(err, ErrOverflow) || !reflect.DeepEqual(h.Snapshot(), before) {
		t.Fatal("version overflow published history")
	}
	h.version = 1
	entry := h.entries[k]
	entry.Samples = math.MaxUint64
	h.entries[k] = entry
	before = h.Snapshot()
	if err := h.Record(k, Measurement{}); !errors.Is(err, ErrOverflow) || !reflect.DeepEqual(h.Snapshot(), before) {
		t.Fatal("sample overflow published history")
	}
}

func testPlanRequest() PlanRequest {
	return PlanRequest{ID: "plan", TargetID: "shot", Capabilities: testCapabilities(), ProfileID: "preview", Workload: testWorkload()}
}
func mustPlanner(t *testing.T, m SnapshotSource, h HistorySource, cfg PlannerConfig) *Planner {
	t.Helper()
	p, e := NewPlanner(m, h, cfg)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func TestPlannerPermanentVersusTemporaryStatusAndAdvisoryOnly(t *testing.T) {
	m := mustManager(t, testConfig())
	h := mustHistory(t, 0)
	p := mustPlanner(t, m, h, DefaultPlannerConfig())
	req := testPlanRequest()
	ready, err := p.Plan(context.Background(), req)
	if err != nil || ready.Status != StatusLocalReady || ready.LocalStatus != StatusLocalReady || ready.Request.RAMBytes != 2_500_000_000 || m.Snapshot().Used != (Counters{}) {
		t.Fatalf("local ready/advice: %+v %v", ready, err)
	}
	held := mustAcquire(t, m, Request{RAMBytes: 2 * GB})
	waiting, err := p.Plan(context.Background(), req)
	if err != nil || waiting.Status != StatusWait || waiting.Plan.PreferRemote {
		t.Fatalf("temporary heavy wait: %+v %v", waiting, err)
	}
	mustRelease(t, held)
	req.Capabilities.MinMemoryGB = 7
	ineligible, err := p.Plan(context.Background(), req)
	if err != nil || ineligible.Status != StatusLocalIneligible || ineligible.Request.RAMBytes != 8_750_000_000 || ineligible.Plan.Provenance == nil {
		t.Fatalf("permanent peak: %+v %v", ineligible, err)
	}
	req = testPlanRequest()
	req.Capabilities.Requirements = nil
	ineligible, err = p.Plan(context.Background(), req)
	if err != nil || ineligible.Status != StatusLocalIneligible || ineligible.Plan.Provenance != nil {
		t.Fatalf("unknown profile: %+v %v", ineligible, err)
	}
	req = testPlanRequest()
	req.Workload.Quality = "final"
	ineligible, err = p.Plan(context.Background(), req)
	if err != nil || ineligible.Status != StatusLocalIneligible {
		t.Fatalf("unsupported workload: %+v %v", ineligible, err)
	}
	// Mutation of the recommendation is never used by admission recomputation.
	ready.Plan.RAMBytes = 1
	ready.Plan.Provenance.DeclaredPeakRAMBytes = 1
	req = testPlanRequest()
	again, err := p.Plan(context.Background(), req)
	if err != nil || again.Plan.RAMBytes != 2_500_000_000 || again.Plan.Provenance.DeclaredPeakRAMBytes != 2_000_000_000 {
		t.Fatal("ResourcePlan edited estimate authority")
	}
	data, err := json.Marshal(again)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PlanResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(decoded, again) {
		t.Fatal("plan/provenance JSON")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if result, err := p.Plan(ctx, req); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, PlanResult{}) {
		t.Fatalf("canceled plan: %+v %v", result, err)
	}
	if _, err := p.Plan(nil, req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("nil planner context")
	}
	if _, err := NewPlanner(m, h, PlannerConfig{SafetyMarginPercent: -1}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("negative config margin")
	}
}

type routeFixture struct {
	id   string
	caps domain.ProviderCapabilities
}

func (f *routeFixture) ID() string { return f.id }
func (f *routeFixture) Capabilities(ctx context.Context) (domain.ProviderCapabilities, error) {
	return f.caps, nil
}
func remoteCaps(id string) domain.ProviderCapabilities {
	return domain.ProviderCapabilities{ProviderID: id, Tags: []string{"video", "motion"}, Local: false, CostPerUnit: 1, CostUnit: "USD/shot", EstimatedLatencyMs: i64(100)}
}
func TestPlannerPreservesPhase07RemoteGatesWithFakeProviders(t *testing.T) {
	reg := provider.NewRegistry()
	ctx := context.Background()
	for _, id := range []string{"good-a", "good-b", "unhealthy", "unknown-health", "missing-tag", "local", "cost-unknown", "cost-unit", "too-costly", "latency-unknown", "too-slow"} {
		caps := remoteCaps(id)
		switch id {
		case "missing-tag":
			caps.Tags = []string{"video"}
		case "local":
			caps.Local = true
			caps.Hardware = &domain.HardwareRequirements{}
		case "cost-unknown":
			caps.CostPerUnit = 0
			caps.CostUnit = ""
		case "cost-unit":
			caps.CostUnit = "EUR/shot"
		case "too-costly":
			caps.CostPerUnit = 2
		case "latency-unknown":
			caps.EstimatedLatencyMs = nil
		case "too-slow":
			caps.EstimatedLatencyMs = i64(101)
		}
		if err := reg.Register(ctx, &routeFixture{id, caps}); err != nil {
			t.Fatal(err)
		}
		health := domain.ProviderHealthHealthy
		if id == "unhealthy" {
			health = domain.ProviderHealthUnhealthy
		}
		if id != "unknown-health" {
			if err := reg.SetHealth(ctx, id, health); err != nil {
				t.Fatal(err)
			}
		}
	}
	router, err := provider.NewRouter(reg)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultPlannerConfig()
	cfg.Router = router
	m := mustManager(t, testConfig())
	p := mustPlanner(t, m, nil, cfg)
	req := testPlanRequest()
	req.Capabilities.Requirements = nil
	policy := provider.RouteRequest{RequiredTags: []string{"video", "motion"}, PreferredIDs: []string{"too-costly", "good-b"}, Locality: provider.RemoteOnly, MaxCost: &provider.CostLimit{Amount: 1, Unit: "USD/shot"}, MaxLatencyMs: i64(100)}
	req.Remote = &RemotePolicy{Route: policy}
	result, err := p.Plan(ctx, req)
	if err != nil || result.Status != StatusRemoteAvailable || result.LocalStatus != StatusLocalIneligible || !result.Plan.PreferRemote || !reflect.DeepEqual(result.Remote.EligibleIDs, []string{"good-b", "good-a"}) || !reflect.DeepEqual(result.Remote.FallbackIDs, []string{"good-a"}) {
		t.Fatalf("remote gating/ranking: %+v %v", result, err)
	}
	expected := []provider.Rejection{
		{ProviderID: "cost-unit", Codes: []provider.RejectionCode{provider.RejectCostUnit}}, {ProviderID: "cost-unknown", Codes: []provider.RejectionCode{provider.RejectCostUnknown}},
		{ProviderID: "latency-unknown", Codes: []provider.RejectionCode{provider.RejectLatencyUnknown}}, {ProviderID: "local", Codes: []provider.RejectionCode{provider.RejectLocality}},
		{ProviderID: "missing-tag", Codes: []provider.RejectionCode{provider.RejectTags}}, {ProviderID: "too-costly", Codes: []provider.RejectionCode{provider.RejectCostLimit}},
		{ProviderID: "too-slow", Codes: []provider.RejectionCode{provider.RejectLatencyLimit}}, {ProviderID: "unhealthy", Codes: []provider.RejectionCode{provider.RejectHealth}},
		{ProviderID: "unknown-health", Codes: []provider.RejectionCode{provider.RejectHealth}},
	}
	if !reflect.DeepEqual(result.Remote.Rejections, expected) {
		t.Fatalf("remote rejection independent oracle: got %+v want %+v", result.Remote.Rejections, expected)
	}
	result.Remote.EligibleIDs[0] = "bad"
	result.Remote.Rejections[0].Codes[0] = provider.RejectTags
	result, err = p.Plan(ctx, req)
	if err != nil || result.Remote.EligibleIDs[0] != "good-b" || result.Remote.Rejections[0].Codes[0] != provider.RejectCostUnit {
		t.Fatal("route result alias")
	}
	req.Remote.Route.MaxCost.Amount = 0
	result, err = p.Plan(ctx, req)
	if err != nil || result.Status != StatusLocalIneligible || result.Plan.PreferRemote || len(result.Remote.EligibleIDs) != 0 {
		t.Fatalf("fallback relaxed paid gate: %+v %v", result, err)
	}
	policy.MaxCost = &provider.CostLimit{Amount: 1, Unit: "USD/shot"}
	req.Remote.Route = policy
	req.Remote.Route.Locality = provider.AnyLocality
	if _, err := p.Plan(ctx, req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("any locality silently broadened remote policy")
	}
	req.Remote.Route.Locality = provider.LocalOnly
	if _, err := p.Plan(ctx, req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal("local-only policy broadened")
	}
	req.Remote = nil
	result, err = p.Plan(ctx, req)
	if err != nil || result.Status != StatusLocalIneligible || len(result.Remote.EligibleIDs) != 0 {
		t.Fatal("implicit remote fallback")
	}
	req = testPlanRequest()
	req.Remote = &RemotePolicy{Route: policy}
	result, err = p.Plan(ctx, req)
	if err != nil || result.Status != StatusLocalReady || result.Plan.PreferRemote {
		t.Fatalf("nonpreferred remote displaced ready local: %+v %v", result, err)
	}
	req.Remote.Prefer = true
	result, err = p.Plan(ctx, req)
	if err != nil || result.Status != StatusRemoteAvailable || result.LocalStatus != StatusLocalReady || m.Snapshot().Used != (Counters{}) {
		t.Fatal("PreferRemote executes/reserves or fails to advise")
	}
}

type cancelRouter struct{ cancel context.CancelFunc }

func (r cancelRouter) Route(ctx context.Context, p provider.RouteRequest) (provider.RouteResult, error) {
	r.cancel()
	return provider.RouteResult{EligibleIDs: []string{"remote"}}, nil
}

type cancelSnapshots struct {
	SnapshotSource
	cancel context.CancelFunc
}

func (s cancelSnapshots) Snapshot() Snapshot {
	s.cancel()
	return s.SnapshotSource.Snapshot()
}

type cancelHistory struct {
	HistorySource
	cancel context.CancelFunc
}

func (s cancelHistory) Snapshot() HistorySnapshot {
	s.cancel()
	return s.HistorySource.Snapshot()
}
func TestPlannerCancellationAfterInjectedRouterAndConfigContexts(t *testing.T) {
	m := mustManager(t, testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := DefaultPlannerConfig()
	cfg.Router = cancelRouter{cancel}
	p := mustPlanner(t, m, nil, cfg)
	req := testPlanRequest()
	req.Remote = &RemotePolicy{Route: provider.RouteRequest{RequiredTags: []string{"video"}, Locality: provider.RemoteOnly}, Prefer: true}
	if result, err := p.Plan(ctx, req); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, PlanResult{}) {
		t.Fatalf("late cancel: %+v %v", result, err)
	}
	p = mustPlanner(t, m, nil, DefaultPlannerConfig())
	if _, err := p.Plan(context.Background(), req); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("remote policy without injected router")
	}
	var nilManager *BudgetManager
	if _, err := NewPlanner(nilManager, nil, DefaultPlannerConfig()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal("typed nil snapshots")
	}
	ctx, cancel = context.WithCancel(context.Background())
	p = mustPlanner(t, cancelSnapshots{m, cancel}, nil, DefaultPlannerConfig())
	req = testPlanRequest()
	if result, err := p.Plan(ctx, req); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, PlanResult{}) {
		t.Fatalf("snapshot cancellation: %+v %v", result, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	p = mustPlanner(t, m, cancelHistory{mustHistory(t, 0), cancel}, DefaultPlannerConfig())
	if result, err := p.Plan(ctx, req); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(result, PlanResult{}) {
		t.Fatalf("history cancellation: %+v %v", result, err)
	}
}
