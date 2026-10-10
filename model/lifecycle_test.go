package model_test

import (
	"context"
	"encoding/json"
	"errors"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/model"
	"github.com/manish96170/videofactory/resource"
)

// Hooks execute outside the fake's mutex, just as manager callbacks must execute
// outside its mutex. Counts and identity remain safe during queued acquisitions.
type failureLoader struct {
	mu           sync.Mutex
	key          model.Key
	identities   int
	loads        int
	unloads      int
	identityHook func(int, model.Key) model.Key
	loadHook     func(context.Context, model.LoadSpec) error
	unloadHook   func(context.Context) (bool, error)
}

func (l *failureLoader) Identity() model.Key {
	l.mu.Lock()
	l.identities++
	n, key, hook := l.identities, l.key, l.identityHook
	l.mu.Unlock()
	if hook != nil {
		return hook(n, key)
	}
	return key
}

func (l *failureLoader) Load(ctx context.Context, spec model.LoadSpec) error {
	l.mu.Lock()
	l.loads++
	hook := l.loadHook
	l.mu.Unlock()
	if hook != nil {
		return hook(ctx, spec)
	}
	return nil
}

func (l *failureLoader) Unload(ctx context.Context) (bool, error) {
	l.mu.Lock()
	l.unloads++
	hook := l.unloadHook
	l.mu.Unlock()
	if hook != nil {
		return hook(ctx)
	}
	return true, nil
}

func (l *failureLoader) setKey(key model.Key) {
	l.mu.Lock()
	l.key = key
	l.mu.Unlock()
}

func (l *failureLoader) callbacks(t *testing.T, loads, unloads int) {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.loads != loads || l.unloads != unloads {
		t.Errorf("callbacks = load %d/unload %d, want %d/%d", l.loads, l.unloads, loads, unloads)
	}
}

type failureFixture struct {
	manager  *model.Manager
	budget   *resource.BudgetManager
	loader   *failureLoader
	key      model.Key
	profile  domain.ExecutionProfile
	workload domain.Workload
}

func newFailureFixture(t *testing.T, configure func(*model.Config), wrap func(*resource.BudgetManager) model.Budget) failureFixture {
	t.Helper()
	budget, err := resource.NewManager(resource.ReferenceConfig(4, 2, resource.AcceleratorFacts{
		Known: true, Backend: domain.BackendNone,
	}, 0, "failure-host"))
	if err != nil {
		t.Fatal(err)
	}
	history, err := resource.NewHistory(0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := model.DefaultConfig()
	cfg.LoadTimeout, cfg.UnloadTimeout = 40*time.Millisecond, 50*time.Millisecond
	cfg.Now = func() time.Time { return time.Unix(1_000, 0) }
	if configure != nil {
		configure(&cfg)
	}
	var supplied model.Budget = budget
	if wrap != nil {
		supplied = wrap(budget)
	}
	m, err := model.NewManager(supplied, history, cfg)
	if err != nil {
		t.Fatal(err)
	}
	key := model.Key{ProviderID: "local", ModelID: "failure-fixture", Revision: "r1"}
	profile := domain.ExecutionProfile{
		ID: "preview", Revision: "r1", Trusted: true, MemoryKnown: true,
		LoadRAMBytes: 128_000_000, ResidentRAMBytes: 64_000_000, WorkspaceRAMBytes: 64_000_000,
		CPU: 1, Backend: domain.BackendNone, Heavy: true,
		Envelope: domain.WorkloadEnvelope{
			Precisions: []string{"fp16"}, Qualities: []string{"preview"}, Offloads: []string{"none"},
		},
	}
	loader := &failureLoader{key: key}
	caps := domain.ModelCapabilities{
		ProviderID: key.ProviderID, ModelID: key.ModelID, Local: true,
		Requirements: &domain.ModelRequirements{
			Version: 1, Revision: key.Revision, ExecutionProfiles: []domain.ExecutionProfile{profile},
		},
	}
	if err := m.Register(context.Background(), caps, loader); err != nil {
		t.Fatal(err)
	}
	return failureFixture{m, budget, loader, key, profile,
		domain.Workload{Precision: "fp16", Quality: "preview", Offload: "none"}}
}

func (f failureFixture) acquire(ctx context.Context) (*model.UseHandle, error) {
	return f.manager.Acquire(ctx, f.key, f.profile.ID, f.workload)
}

func (f failureFixture) state(t *testing.T, want model.State, stopped bool, previousID string) string {
	t.Helper()
	s := f.manager.Snapshot()
	if len(s.Models) != 1 {
		t.Fatalf("model count = %d, want 1", len(s.Models))
	}
	m, b := s.Models[0], f.budget.Snapshot()
	if m.Key != f.key || m.State != want || m.StoppedVerified != stopped {
		t.Errorf("key/state/stop proof = %+v/%s/%v, want %+v/%s/%v", m.Key, m.State, m.StoppedVerified, f.key, want, stopped)
	}
	if len(b.Queue) != 0 || b.Exclusive {
		t.Errorf("unexpected queue/exclusive ownership: %+v", b)
	}
	if want == model.StateUnloaded {
		if m.Spec != nil || m.Estimate != nil || m.Reservation != nil || b.Used != (resource.Counters{}) || len(b.Active) != 0 || b.Available != b.Total {
			t.Errorf("unloaded model leaked resources: model=%+v budget=%+v", m, b)
		}
		return ""
	}
	// Independent constants: 128 MB peak plus 25%, one CPU, one heavy slot.
	wantReq := resource.Request{RAMBytes: 160_000_000, CPU: 1, Heavy: true, Backend: domain.BackendNone}
	wantUsed := resource.Counters{RAMBytes: 160_000_000, CPUUnits: 1_000_000, Slots: 1, Heavy: 1}
	wantAvailable := resource.Counters{RAMBytes: 7_340_000_000, CPUUnits: 1_000_000, Slots: 1}
	if m.Reservation == nil || *m.Reservation != wantReq || m.Estimate == nil || m.Estimate.Request != wantReq || m.Spec == nil || m.Spec.Key != f.key || m.Spec.Profile.ID != "preview" || m.Spec.Workload != f.workload {
		t.Errorf("incorrect held declaration: %+v", m)
	}
	if b.Used != wantUsed || b.Available != wantAvailable || len(b.Active) != 1 {
		t.Fatalf("reservation not retained exactly once: %+v", b)
	}
	a := b.Active[0]
	if a.ID == "" || (previousID != "" && a.ID != previousID) || a.Request != wantReq || a.Reserved != wantUsed {
		t.Errorf("allocation/token changed: %+v, previous ID %q", a, previousID)
	}
	return a.ID
}

func failureCanceledResult(t *testing.T, h *model.UseHandle, err error) {
	t.Helper()
	if h != nil || !errors.Is(err, context.Canceled) {
		t.Errorf("canceled acquire = %v, %v; want nil handle/context.Canceled", h, err)
	}
}

func TestFailureCallerCancellation(t *testing.T) {
	t.Run("pre_entry_and_nil_context", func(t *testing.T) {
		f := newFailureFixture(t, nil, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		h, err := f.acquire(ctx)
		failureCanceledResult(t, h, err)
		h, err = f.acquire(nil)
		if h != nil || !errors.Is(err, model.ErrInvalidRequest) {
			t.Errorf("nil context = %v, %v; want nil/ErrInvalidRequest", h, err)
		}
		f.state(t, model.StateUnloaded, false, "")
		f.loader.callbacks(t, 0, 0)
	})
	t.Run("queued_reservation", func(t *testing.T) {
		f := newFailureFixture(t, nil, nil)
		blocker, err := f.budget.Acquire(context.Background(), resource.Request{RAMBytes: 32_000_000, CPU: 1, Heavy: true, Backend: domain.BackendNone})
		if err != nil || blocker == nil {
			t.Fatalf("blocker = %v, %v", blocker, err)
		}
		defer blocker.Release()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		type result struct {
			h   *model.UseHandle
			err error
		}
		done := make(chan result, 1)
		go func() { h, err := f.acquire(ctx); done <- result{h, err} }()
		// Observe the REAL queued ticket, not merely entry into a fake Acquire.
		bound := time.NewTimer(2 * time.Second)
		defer bound.Stop()
		for len(f.budget.Snapshot().Queue) != 1 {
			select {
			case r := <-done:
				t.Fatalf("acquire returned instead of queueing: %v, %v", r.h, r.err)
			case <-bound.C:
				t.Fatal("reservation did not enter the budget queue")
			default:
				runtime.Gosched()
			}
		}
		if s := f.manager.Snapshot().Models[0]; s.State != model.StateReserving || s.Reservation != nil {
			t.Errorf("queued model owns an allocation or wrong intent: %+v", s)
		}
		cancel()
		select {
		case r := <-done:
			failureCanceledResult(t, r.h, r.err)
		case <-bound.C:
			t.Fatal("queued cancellation did not return")
		}
		b := f.budget.Snapshot()
		wantUsed := resource.Counters{RAMBytes: 32_000_000, CPUUnits: 1_000_000, Slots: 1, Heavy: 1}
		wantAvailable := resource.Counters{RAMBytes: 7_468_000_000, CPUUnits: 1_000_000, Slots: 1}
		if len(b.Queue) != 0 || b.Used != wantUsed || b.Available != wantAvailable || len(b.Active) != 1 || b.Active[0].ID != blocker.ID() {
			t.Errorf("canceled ticket leaked or refunded the blocker: %+v", b)
		}
		f.loader.callbacks(t, 0, 0)
		if err := blocker.Release(); err != nil {
			t.Fatal(err)
		}
		f.state(t, model.StateUnloaded, false, "")
	})
	t.Run("live_use_does_not_expire", func(t *testing.T) {
		f := newFailureFixture(t, nil, nil)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		h, err := f.acquire(ctx)
		if err != nil || h == nil {
			t.Fatalf("acquire = %v, %v", h, err)
		}
		id := f.state(t, model.StateInUse, false, "")
		cancel()
		<-ctx.Done()
		f.state(t, model.StateInUse, false, id)
		if err := h.Release(); err != nil {
			t.Fatal(err)
		}
		f.state(t, model.StateIdle, false, id)
		if err := f.manager.Unload(ctx, f.key); !errors.Is(err, context.Canceled) {
			t.Errorf("canceled unload = %v", err)
		}
		f.state(t, model.StateIdle, false, id)
		f.loader.callbacks(t, 1, 0)
		if err := f.manager.Unload(context.Background(), f.key); err != nil {
			t.Fatal(err)
		}
		f.state(t, model.StateUnloaded, false, "")
		f.loader.callbacks(t, 1, 1)
	})
}

func TestFailureLoadTimeoutCleanup(t *testing.T) {
	for _, tc := range []struct {
		name         string
		lateSuccess  bool
		stopped      bool
		cancelParent bool
	}{
		{"cooperative_timeout_verified_stop", false, true, false},
		{"late_success_verified_stop", true, true, false},
		{"late_success_unknown_stop", true, false, false},
		{"parent_cancel_unknown_stop", false, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFailureFixture(t, nil, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var loadCtx context.Context
			f.loader.loadHook = func(c context.Context, _ model.LoadSpec) error {
				loadCtx = c
				if deadline, ok := c.Deadline(); !ok || time.Until(deadline) > 40*time.Millisecond {
					t.Error("load callback lacks the configured cooperative deadline")
				}
				f.state(t, model.StateLoading, false, "")
				if tc.cancelParent {
					cancel()
				}
				<-c.Done()
				if tc.lateSuccess {
					return nil // A return after Done is NOT a valid successful load.
				}
				return c.Err()
			}
			f.loader.unloadHook = func(c context.Context) (bool, error) {
				if c == loadCtx || c.Err() != nil || c.Done() == nil {
					t.Error("cleanup did not receive a separate, active bounded context")
				}
				if deadline, ok := c.Deadline(); !ok || time.Until(deadline) > 50*time.Millisecond {
					t.Error("cleanup callback lacks the configured deadline")
				}
				f.state(t, model.StateUnloading, false, "")
				return tc.stopped, nil // nil alone must never refund unknown work.
			}
			h, err := f.acquire(ctx)
			wantCause := context.DeadlineExceeded
			if tc.cancelParent {
				wantCause = context.Canceled
			}
			if h != nil || !errors.Is(err, wantCause) || !errors.Is(err, model.ErrLoadFailed) {
				t.Errorf("expired load = %v, %v; want nil/%v/ErrLoadFailed", h, err, wantCause)
			}
			f.loader.callbacks(t, 1, 1)
			if tc.stopped {
				f.state(t, model.StateUnloaded, false, "")
				return
			}
			if !errors.Is(err, model.ErrQuarantined) || !errors.Is(err, model.ErrNotStopped) {
				t.Errorf("unknown cleanup lacks quarantine/stop-proof causes: %v", err)
			}
			id := f.state(t, model.StateQuarantined, false, "")
			h, err = f.acquire(context.Background())
			if h != nil || !errors.Is(err, model.ErrQuarantined) {
				t.Errorf("quarantine permitted reuse: %v, %v", h, err)
			}
			f.state(t, model.StateQuarantined, false, id)
			f.loader.callbacks(t, 1, 1)
			f.loader.unloadHook = nil
			if err := f.manager.Unload(context.Background(), f.key); err != nil {
				t.Fatal(err)
			}
			f.state(t, model.StateUnloaded, false, "")
			f.loader.callbacks(t, 1, 2)
		})
	}
}

type failureDiagnostic struct{ operation string }

func (e *failureDiagnostic) Error() string { return "synthetic-private-diagnostic: " + e.operation }

func TestFailureLoaderIdentityDrift(t *testing.T) {
	for _, operation := range []string{"load", "unload"} {
		for _, callbackFails := range []bool{false, true} {
			name := operation + "_success"
			if callbackFails {
				name = operation + "_error"
			}
			t.Run(name, func(t *testing.T) {
				f := newFailureFixture(t, nil, nil)
				private := &failureDiagnostic{operation}
				var callbackErr error
				if callbackFails {
					callbackErr = private
				}
				drift := f.key
				drift.Revision = "unexpected-revision"
				var err error
				if operation == "load" {
					f.loader.loadHook = func(context.Context, model.LoadSpec) error {
						f.loader.setKey(drift)
						return callbackErr
					}
					var h *model.UseHandle
					h, err = f.acquire(context.Background())
					if h != nil {
						t.Error("identity-drifted load returned a use handle")
					}
				} else {
					h, acquireErr := f.acquire(context.Background())
					if acquireErr != nil || h == nil {
						t.Fatalf("initial acquire = %v, %v", h, acquireErr)
					}
					if err := h.Release(); err != nil {
						t.Fatal(err)
					}
					f.loader.unloadHook = func(context.Context) (bool, error) {
						f.loader.setKey(drift)
						return true, callbackErr // true is NOT proof from another source.
					}
					err = f.manager.Unload(context.Background(), f.key)
				}
				if !errors.Is(err, model.ErrIdentityMismatch) || !errors.Is(err, model.ErrQuarantined) {
					t.Errorf("drift did not preserve permanent identity rejection/quarantine: %v", err)
				}
				var core *model.Error
				if !errors.As(err, &core) || core.Code() != model.CodeQuarantined || err.Error() != "model: quarantined" {
					t.Errorf("unexpected public core error: %v", err)
				}
				var diagnostic *failureDiagnostic
				if callbackFails && (!errors.Is(err, private) || !errors.As(err, &diagnostic) || diagnostic != private) {
					t.Error("identity rejection lost the original callback error or its concrete type")
				}
				if !callbackFails && errors.As(err, &diagnostic) {
					t.Error("successful callback manufactured an adapter failure")
				}
				encoded, marshalErr := json.Marshal(f.manager.Snapshot())
				if marshalErr != nil || strings.Contains(string(encoded), private.Error()) || strings.Contains(err.Error(), private.Error()) {
					t.Error("public error/snapshot leaked synthetic private callback diagnostics")
				}
				id := f.state(t, model.StateQuarantined, false, "")
				f.loader.callbacks(t, 1, 1)
				h, retryErr := f.acquire(context.Background())
				if h != nil || !errors.Is(retryErr, model.ErrQuarantined) {
					t.Errorf("drift quarantine allowed another use: %v, %v", h, retryErr)
				}
				f.state(t, model.StateQuarantined, false, id)
				f.loader.callbacks(t, 1, 1)
				// Restore the expected source before accepting its explicit stop proof.
				f.loader.setKey(f.key)
				f.loader.unloadHook = nil
				if err := f.manager.Unload(context.Background(), f.key); err != nil {
					t.Fatal(err)
				}
				f.state(t, model.StateUnloaded, false, "")
				f.loader.callbacks(t, 1, 2)
			})
		}
	}
}

type failureBudget struct {
	*resource.BudgetManager
	mu      sync.Mutex
	last    *failureLease
	release error
}

func (b *failureBudget) Acquire(ctx context.Context, req resource.Request) (resource.Lease, error) {
	lease, err := b.BudgetManager.Acquire(ctx, req)
	if err != nil {
		return nil, err
	}
	w := &failureLease{Lease: lease, fail: b.release}
	b.mu.Lock()
	b.last = w
	b.mu.Unlock()
	return w, nil
}

type failureLease struct {
	resource.Lease
	mu       sync.Mutex
	attempts int
	fail     error
}

func (l *failureLease) Release() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts++
	if l.attempts == 1 {
		return l.fail // Keep the REAL allocation and token; do not refund on error.
	}
	return l.Lease.Release()
}

func TestFailureLeaseReleaseRetry(t *testing.T) {
	private := &failureDiagnostic{"resource release"}
	var wrapped *failureBudget
	f := newFailureFixture(t, nil, func(b *resource.BudgetManager) model.Budget {
		wrapped = &failureBudget{BudgetManager: b, release: private}
		return wrapped
	})
	h, err := f.acquire(context.Background())
	if err != nil || h == nil {
		t.Fatalf("acquire = %v, %v", h, err)
	}
	id := f.state(t, model.StateInUse, false, "")
	if err := h.Release(); err != nil {
		t.Fatal(err)
	}
	err = f.manager.Unload(context.Background(), f.key)
	if !errors.Is(err, model.ErrLeaseRelease) || !errors.Is(err, model.ErrQuarantined) || !errors.Is(err, private) || err.Error() != "model: lease_release_failed" {
		t.Errorf("failed bookkeeping = %v; want safe release failure/quarantine/original cause", err)
	}
	var diagnostic *failureDiagnostic
	if !errors.As(err, &diagnostic) || diagnostic != private {
		t.Error("bookkeeping error lost its concrete cause")
	}
	f.state(t, model.StateQuarantined, true, id)
	f.loader.callbacks(t, 1, 1)
	wrapped.mu.Lock()
	lease := wrapped.last
	wrapped.mu.Unlock()
	lease.mu.Lock()
	firstAttempts := lease.attempts
	lease.mu.Unlock()
	if firstAttempts != 1 || lease.ID() != id {
		t.Errorf("wrong failed release attempts/token: %d/%q, want 1/%q", firstAttempts, lease.ID(), id)
	}
	if err := f.manager.Unload(context.Background(), f.key); err != nil {
		t.Fatal(err)
	}
	f.state(t, model.StateUnloaded, false, "")
	f.loader.callbacks(t, 1, 1) // Stop is already proven: retry bookkeeping ONLY.
	lease.mu.Lock()
	finalAttempts := lease.attempts
	lease.mu.Unlock()
	if finalAttempts != 2 {
		t.Errorf("release attempts = %d, want exactly 2", finalAttempts)
	}
}

func TestFailureFinalCallbackCancellation(t *testing.T) {
	for _, tc := range []struct {
		name         string
		reuse        bool
		identityCall int
		clockCall    int64
		wantLoads    int
		wantUnloads  int
	}{
		{"fresh_final_source", false, 5, 0, 1, 1},
		{"reuse_final_source", true, 7, 0, 1, 0},
		{"fresh_clock_before_load", false, 0, 1, 0, 0},
		{"fresh_clock_after_load", false, 0, 2, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var manager *model.Manager
			var clocks atomic.Int64
			f := newFailureFixture(t, func(cfg *model.Config) {
				cfg.Now = func() time.Time {
					n := clocks.Add(1)
					manager.Snapshot() // Reentrant callback must not deadlock.
					if tc.clockCall != 0 && n == tc.clockCall {
						cancel()
					}
					return time.Unix(1_000, 0)
				}
			}, nil)
			manager = f.manager
			var id string
			if tc.reuse {
				h, err := f.acquire(context.Background())
				if err != nil || h == nil {
					t.Fatalf("preload = %v, %v", h, err)
				}
				id = f.state(t, model.StateInUse, false, "")
				if err := h.Release(); err != nil {
					t.Fatal(err)
				}
			}
			var sourceCanceled atomic.Bool
			f.loader.identityHook = func(n int, key model.Key) model.Key {
				manager.Snapshot()
				if tc.identityCall != 0 && n == tc.identityCall {
					sourceCanceled.Store(true)
					cancel()
				}
				return key
			}
			h, err := f.acquire(ctx)
			failureCanceledResult(t, h, err)
			if tc.identityCall != 0 && !sourceCanceled.Load() {
				t.Error("fixture never reached the specified final source cancellation")
			}
			if tc.clockCall != 0 && clocks.Load() < tc.clockCall {
				t.Error("fixture never reached the specified clock cancellation")
			}
			f.loader.callbacks(t, tc.wantLoads, tc.wantUnloads)
			if !tc.reuse {
				f.state(t, model.StateUnloaded, false, "")
				return
			}
			f.state(t, model.StateIdle, false, id)
			// Canceled reuse must neither unload the old instance nor consume use ownership.
			h, err = f.acquire(context.Background())
			if err != nil || h == nil {
				t.Fatalf("reuse after cancellation = %v, %v", h, err)
			}
			f.state(t, model.StateInUse, false, id)
			f.loader.callbacks(t, 1, 0)
			if err := h.Release(); err != nil {
				t.Fatal(err)
			}
			if err := f.manager.Unload(context.Background(), f.key); err != nil {
				t.Fatal(err)
			}
			f.state(t, model.StateUnloaded, false, "")
			f.loader.callbacks(t, 1, 1)
		})
	}
}
