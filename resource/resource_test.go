package resource

import (
	"context"
	"errors"
	"testing"
)

// fakeLease/fakeManager exist only to prove the Manager/Lease interfaces
// are satisfiable and ergonomic to implement, including the over-budget,
// cancellation, and release-restores-capacity paths. The real
// budget-aware Manager (memory/CPU/GPU accounting, queueing, process
// monitoring) is built in Phase 08.
type fakeLease struct {
	id       string
	bytes    int64
	mgr      *fakeManager
	released bool
}

func (l *fakeLease) ID() string { return l.id }

func (l *fakeLease) Release() error {
	if l.released {
		return nil
	}
	l.released = true
	l.mgr.used -= l.bytes
	return nil
}

type fakeManager struct {
	budgetBytes int64
	used        int64
	nextID      int
}

func (m *fakeManager) Acquire(ctx context.Context, req Request) (Lease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.used+req.RAMBytes > m.budgetBytes {
		return nil, errors.New("resource: request exceeds budget")
	}
	m.used += req.RAMBytes
	m.nextID++
	return &fakeLease{id: "lease-1", bytes: req.RAMBytes, mgr: m}, nil
}

var (
	_ Manager = &fakeManager{}
	_ Lease   = &fakeLease{}
)

func TestFakeManagerAcquireWithinBudget(t *testing.T) {
	mgr := &fakeManager{budgetBytes: 8 << 30} // 8 GB

	lease, err := mgr.Acquire(context.Background(), Request{RAMBytes: 4 << 30})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	defer lease.Release()

	if lease.ID() == "" {
		t.Fatalf("expected non-empty lease ID")
	}
}

func TestFakeManagerRejectsOverBudgetRequest(t *testing.T) {
	mgr := &fakeManager{budgetBytes: 4 << 30} // 4 GB

	_, err := mgr.Acquire(context.Background(), Request{RAMBytes: 6 << 30})
	if err == nil {
		t.Fatalf("expected over-budget request to fail")
	}
}

func TestFakeManagerRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mgr := &fakeManager{budgetBytes: 8 << 30}
	if _, err := mgr.Acquire(ctx, Request{RAMBytes: 1 << 30}); err == nil {
		t.Fatalf("expected cancelled context to produce an error")
	}
}

// TestFakeManagerReleaseRestoresCapacity exercises the full
// acquire -> release -> acquire lifecycle the Lease/Manager contract
// implies: releasing a lease must give its capacity back to the manager
// so a subsequent request of the same size can succeed.
func TestFakeManagerReleaseRestoresCapacity(t *testing.T) {
	mgr := &fakeManager{budgetBytes: 4 << 30} // 4 GB

	lease, err := mgr.Acquire(context.Background(), Request{RAMBytes: 4 << 30})
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}

	if _, err := mgr.Acquire(context.Background(), Request{RAMBytes: 1}); err == nil {
		t.Fatalf("expected second Acquire to fail while budget is fully used")
	}

	if err := lease.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}

	second, err := mgr.Acquire(context.Background(), Request{RAMBytes: 4 << 30})
	if err != nil {
		t.Fatalf("Acquire after Release: %v", err)
	}
	defer second.Release()
}

func TestFakeLeaseReleaseIsSafeToCallTwice(t *testing.T) {
	mgr := &fakeManager{budgetBytes: 4 << 30}
	lease, err := mgr.Acquire(context.Background(), Request{RAMBytes: 1 << 30})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	if err := lease.Release(); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatalf("second Release: %v", err)
	}
	if mgr.used != 0 {
		t.Fatalf("expected budget fully restored after release, got used=%d", mgr.used)
	}
}
