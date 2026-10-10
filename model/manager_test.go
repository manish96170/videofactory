package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/model"
	"github.com/manish96170/videofactory/resource"
)

// This loader only records callbacks: the memory bounds are declarations, not
// heap allocations, and the real resource manager is the sole budget owner.
type smokeLoader struct {
	key          model.Key
	budget       *resource.BudgetManager
	loadErr      error
	stopped      bool
	loads        int
	unloads      int
	spec         model.LoadSpec
	budgetAtLoad resource.Snapshot
}

func (l *smokeLoader) Identity() model.Key { return l.key }
func (l *smokeLoader) Load(_ context.Context, spec model.LoadSpec) error {
	l.loads++
	l.spec = spec
	l.budgetAtLoad = l.budget.Snapshot()
	return l.loadErr
}
func (l *smokeLoader) Unload(context.Context) (bool, error) {
	l.unloads++
	return l.stopped, nil
}

type smokeFixture struct {
	manager  *model.Manager
	budget   *resource.BudgetManager
	loader   *smokeLoader
	key      model.Key
	profile  domain.ExecutionProfile
	workload domain.Workload
}

func newSmokeFixture(t *testing.T) smokeFixture {
	t.Helper()
	cfg := resource.ReferenceConfig(4, 2, resource.AcceleratorFacts{
		Known: true, Backend: domain.BackendNone, Capacity: 0,
	}, 0, "test-host")
	budget, err := resource.NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	history, err := resource.NewHistory(0)
	if err != nil {
		t.Fatal(err)
	}
	managerCfg := model.DefaultConfig()
	managerCfg.Now = func() time.Time { return time.Unix(1_000, 0) }
	manager, err := model.NewManager(budget, history, managerCfg)
	if err != nil {
		t.Fatal(err)
	}
	key := model.Key{ProviderID: "local", ModelID: "fixture", Revision: "r1"}
	profile := domain.ExecutionProfile{
		ID: "preview", Revision: "r1", Trusted: true, MemoryKnown: true,
		LoadRAMBytes: 128_000_000, ResidentRAMBytes: 64_000_000, WorkspaceRAMBytes: 64_000_000,
		CPU: 1, GPU: 0, Backend: domain.BackendNone, Heavy: true,
		Envelope: domain.WorkloadEnvelope{
			Precisions: []string{"fp16"}, Qualities: []string{"preview"}, Offloads: []string{"none"},
			Width: domain.IntRange{}, Height: domain.IntRange{}, Frames: domain.IntRange{}, ChunkSize: domain.IntRange{},
		},
	}
	caps := domain.ModelCapabilities{
		ProviderID: key.ProviderID, ModelID: key.ModelID, Local: true,
		Tags: []string{"semantic-decision"},
		Requirements: &domain.ModelRequirements{
			Version: 1, Revision: key.Revision, ExecutionProfiles: []domain.ExecutionProfile{profile},
		},
	}
	loader := &smokeLoader{key: key, budget: budget, stopped: true}
	if err := manager.Register(context.Background(), caps, loader); err != nil {
		t.Fatal(err)
	}
	return smokeFixture{
		manager: manager, budget: budget, loader: loader, key: key, profile: profile,
		workload: domain.Workload{Precision: "fp16", Quality: "preview", Offload: "none"},
	}
}

func assertSmokeState(t *testing.T, f smokeFixture, state model.State, allocationID string) string {
	t.Helper()
	snapshot := f.manager.Snapshot()
	if len(snapshot.Models) != 1 {
		t.Fatalf("model snapshot count = %d, want 1", len(snapshot.Models))
	}
	m := snapshot.Models[0]
	if m.Key != f.key || m.State != state {
		t.Fatalf("model key/state = %+v/%s, want %+v/%s", m.Key, m.State, f.key, state)
	}
	b := f.budget.Snapshot()
	if len(b.Queue) != 0 || b.Exclusive {
		t.Fatalf("unexpected queue/exclusive ownership: %+v", b)
	}
	if state == model.StateUnloaded {
		if m.Reservation != nil || m.Spec != nil || m.Estimate != nil || b.Used != (resource.Counters{}) || len(b.Active) != 0 || b.Available != b.Total {
			t.Fatalf("unloaded model retained resources: model=%+v budget=%+v", m, b)
		}
		return ""
	}
	wantRequest := resource.Request{RAMBytes: 160_000_000, CPU: 1, Backend: domain.BackendNone, Heavy: true}
	wantUsed := resource.Counters{RAMBytes: 160_000_000, CPUUnits: 1_000_000, Slots: 1, Heavy: 1}
	wantAvailable := resource.Counters{RAMBytes: 7_340_000_000, CPUUnits: 1_000_000, Slots: 1}
	if m.Reservation == nil || *m.Reservation != wantRequest || m.Estimate == nil || m.Estimate.Request != wantRequest || m.StoppedVerified {
		t.Fatalf("incorrect retained reservation: %+v", m)
	}
	wantSpec := model.LoadSpec{Key: f.key, Profile: f.profile, Workload: f.workload}
	if m.Spec == nil || !reflect.DeepEqual(*m.Spec, wantSpec) {
		t.Fatalf("incorrect admitted spec: %+v", m.Spec)
	}
	if b.Used != wantUsed || b.Available != wantAvailable || len(b.Active) != 1 {
		t.Fatalf("full peak allocation not held exactly once: %+v", b)
	}
	a := b.Active[0]
	if a.Request != wantRequest || a.Reserved != wantUsed || a.ID == "" || (allocationID != "" && a.ID != allocationID) {
		t.Fatalf("allocation changed or differs from reservation: %+v, previous ID=%q", a, allocationID)
	}
	return a.ID
}

func TestManagerRetainsReservationUntilExplicitUnload(t *testing.T) {
	f := newSmokeFixture(t)
	ctx := context.Background()
	assertSmokeState(t, f, model.StateUnloaded, "")
	if f.loader.loads != 0 || f.loader.unloads != 0 {
		t.Fatal("registration called the loader lifecycle")
	}
	h, err := f.manager.Acquire(ctx, f.key, f.profile.ID, f.workload)
	if err != nil || h == nil {
		t.Fatalf("acquire = %v, %v", h, err)
	}
	id := assertSmokeState(t, f, model.StateInUse, "")
	if f.loader.loads != 1 || f.loader.unloads != 0 || !reflect.DeepEqual(f.loader.spec, *f.manager.Snapshot().Models[0].Spec) {
		t.Fatal("load callback count/spec does not match the admitted model")
	}
	if !reflect.DeepEqual(f.loader.budgetAtLoad, f.budget.Snapshot()) {
		t.Fatal("full reservation was not held before the load callback")
	}
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	assertSmokeState(t, f, model.StateIdle, id)
	reused, err := f.manager.Acquire(ctx, f.key, f.profile.ID, f.workload)
	if err != nil || reused == nil {
		t.Fatalf("reuse = %v, %v", reused, err)
	}
	assertSmokeState(t, f, model.StateInUse, id)
	if f.loader.loads != 1 || f.loader.unloads != 0 {
		t.Fatal("same-profile/workload reuse invoked another loader callback")
	}
	if err := reused.Release(); err != nil {
		t.Fatal(err)
	}
	assertSmokeState(t, f, model.StateIdle, id)
	if err := f.manager.Unload(ctx, f.key); err != nil {
		t.Fatal(err)
	}
	assertSmokeState(t, f, model.StateUnloaded, "")
	if f.loader.loads != 1 || f.loader.unloads != 1 {
		t.Fatalf("final callback counts = load %d, unload %d", f.loader.loads, f.loader.unloads)
	}
}

func TestManagerQuarantinesPartialLoadUntilStopVerified(t *testing.T) {
	f := newSmokeFixture(t)
	ctx := context.Background()
	privateErr := errors.New("private adapter diagnostic must not be public")
	f.loader.loadErr = privateErr
	f.loader.stopped = false
	h, err := f.manager.Acquire(ctx, f.key, f.profile.ID, f.workload)
	if h != nil || !errors.Is(err, model.ErrQuarantined) || !errors.Is(err, privateErr) || !errors.Is(err, model.ErrLoadFailed) {
		t.Fatalf("failed partial load = %v, %v; want nil handle and quarantine/original cause", h, err)
	}
	id := assertSmokeState(t, f, model.StateQuarantined, "")
	if f.loader.loads != 1 || f.loader.unloads != 1 {
		t.Fatal("failed load did not attempt cleanup exactly once")
	}
	encoded, marshalErr := json.Marshal(f.manager.Snapshot())
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(err.Error(), privateErr.Error()) || strings.Contains(string(encoded), privateErr.Error()) {
		t.Fatal("public error or snapshot leaked private loader diagnostics")
	}
	h, err = f.manager.Acquire(ctx, f.key, f.profile.ID, f.workload)
	if h != nil || !errors.Is(err, model.ErrQuarantined) {
		t.Fatalf("quarantine allowed another acquire: %v, %v", h, err)
	}
	assertSmokeState(t, f, model.StateQuarantined, id)
	if f.loader.loads != 1 || f.loader.unloads != 1 {
		t.Fatal("refused acquire invoked a loader callback")
	}
	f.loader.stopped = true
	if err := f.manager.Unload(ctx, f.key); err != nil {
		t.Fatal(err)
	}
	assertSmokeState(t, f, model.StateUnloaded, "")
	if f.loader.loads != 1 || f.loader.unloads != 2 {
		t.Fatalf("cleanup retry callback counts = load %d, unload %d", f.loader.loads, f.loader.unloads)
	}
}

func TestManagerBusyAndStaleHandleRelease(t *testing.T) {
	f := newSmokeFixture(t)
	ctx := context.Background()
	old, err := f.manager.Acquire(ctx, f.key, f.profile.ID, f.workload)
	if err != nil || old == nil {
		t.Fatalf("acquire = %v, %v", old, err)
	}
	id := assertSmokeState(t, f, model.StateInUse, "")
	second, err := f.manager.Acquire(ctx, f.key, f.profile.ID, f.workload)
	if second != nil || !errors.Is(err, model.ErrBusy) {
		t.Fatalf("second acquire = %v, %v; want nil handle/ErrBusy", second, err)
	}
	if err := f.manager.Unload(ctx, f.key); !errors.Is(err, model.ErrBusy) {
		t.Fatalf("in-use unload = %v, want ErrBusy", err)
	}
	assertSmokeState(t, f, model.StateInUse, id)
	if f.loader.loads != 1 || f.loader.unloads != 0 {
		t.Fatal("busy operation invoked an extra loader callback")
	}
	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	if err := old.Release(); err != nil {
		t.Fatalf("repeated release = %v", err)
	}
	assertSmokeState(t, f, model.StateIdle, id)
	current, err := f.manager.Acquire(ctx, f.key, f.profile.ID, f.workload)
	if err != nil || current == nil {
		t.Fatalf("new use = %v, %v", current, err)
	}
	if err := old.Release(); err != nil {
		t.Fatalf("stale release = %v", err)
	}
	assertSmokeState(t, f, model.StateInUse, id)
	if err := f.manager.Unload(ctx, f.key); !errors.Is(err, model.ErrBusy) {
		t.Fatalf("stale release cleared new use: unload = %v", err)
	}
	if f.loader.loads != 1 || f.loader.unloads != 0 {
		t.Fatal("busy/stale-handle operations changed the backend lifecycle")
	}
	if err := current.Release(); err != nil {
		t.Fatal(err)
	}
	assertSmokeState(t, f, model.StateIdle, id)
	if err := f.manager.Unload(ctx, f.key); err != nil {
		t.Fatal(err)
	}
	assertSmokeState(t, f, model.StateUnloaded, "")
	if f.loader.loads != 1 || f.loader.unloads != 1 {
		t.Fatalf("final callback counts = load %d, unload %d", f.loader.loads, f.loader.unloads)
	}
}
