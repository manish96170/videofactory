package resource

import (
	"context"
	"errors"
	"math"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func testConfig() Config {
	return ReferenceConfig(8, 4, AcceleratorFacts{Known: true, Backend: domain.BackendMetal, UnifiedMemory: true, Capacity: 1}, 1, "test-unified")
}
func mustManager(t *testing.T, c Config) *BudgetManager {
	t.Helper()
	m, err := NewManager(c)
	if err != nil {
		t.Fatal(err)
	}
	return m
}
func mustAcquire(t *testing.T, m Manager, r Request) Lease {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	l, err := m.Acquire(ctx, r)
	if err != nil || l == nil {
		t.Fatalf("Acquire: %v, %v", l, err)
	}
	return l
}
func mustRelease(t *testing.T, l Lease) {
	t.Helper()
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
}

type acquisition struct {
	lease Lease
	err   error
}

func asyncAcquire(m Manager, ctx context.Context, r Request) <-chan acquisition {
	ch := make(chan acquisition, 1)
	go func() { l, e := m.Acquire(ctx, r); ch <- acquisition{l, e} }()
	return ch
}
func receive(t *testing.T, ch <-chan acquisition) acquisition {
	t.Helper()
	select {
	case a := <-ch:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("acquisition did not finish")
		return acquisition{}
	}
}

// Yield until an observable barrier, not a guessed scheduling sleep.
func waitQueue(t *testing.T, m *BudgetManager, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for len(m.Snapshot().Queue) != n {
		if time.Now().After(deadline) {
			t.Fatalf("queue barrier: got %d, want %d", len(m.Snapshot().Queue), n)
		}
		runtime.Gosched()
	}
}

func TestReferenceConfigurationAndValidation(t *testing.T) {
	c := testConfig()
	if c.TotalRAMBytes != 16_000_000_000 || c.SystemReserveBytes != 3_000_000_000 || c.UserReserveBytes != 4_000_000_000 || c.DirectorReserveBytes != 1_500_000_000 || c.WorkerRAMBytes != 7_500_000_000 || c.MaxParallel != 2 || c.MaxHeavy != 1 || c.QueueLimit != 64 || c.HeavyRAMThreshold != 2_000_000_000 {
		t.Fatalf("wrong reference: %+v", c)
	}
	cases := []struct {
		name   string
		change func(*Config)
	}{
		{"total", func(c *Config) { c.TotalRAMBytes = 0 }}, {"reserve negative", func(c *Config) { c.SystemReserveBytes = -1 }},
		{"reserve overflow", func(c *Config) {
			c.TotalRAMBytes = math.MaxInt64
			c.SystemReserveBytes = math.MaxInt64
			c.UserReserveBytes = math.MaxInt64
		}},
		{"worker exceeds remaining", func(c *Config) { c.WorkerRAMBytes = 7_500_000_001 }}, {"zero worker", func(c *Config) { c.WorkerRAMBytes = 0 }},
		{"unknown cpu", func(c *Config) { c.CPUCapacity = 0 }}, {"cpu over physical", func(c *Config) { c.WorkerCPU = 9 }},
		{"gpu over physical", func(c *Config) { c.WorkerGPU = 2 }}, {"nan", func(c *Config) { c.WorkerCPU = math.NaN() }},
		{"inf", func(c *Config) { c.Accelerator.Capacity = math.Inf(1) }}, {"quantum overflow", func(c *Config) { c.CPUCapacity = 1e20 }},
		{"subquantum cpu", func(c *Config) { c.WorkerCPU = .0000001 }}, {"negative gpu", func(c *Config) { c.WorkerGPU = -1 }},
		{"unknown gpu facts", func(c *Config) { c.Accelerator.Known = false }}, {"unknown backend", func(c *Config) { c.Accelerator.Backend = domain.BackendUnknown }},
		{"cpu only gpu", func(c *Config) { c.Accelerator.Backend = domain.BackendNone }}, {"slots", func(c *Config) { c.MaxParallel = 0 }},
		{"heavy", func(c *Config) { c.MaxHeavy = 3 }}, {"queue", func(c *Config) { c.QueueLimit = -1 }},
		{"threshold", func(c *Config) { c.HeavyRAMThreshold = 0 }}, {"class", func(c *Config) { c.HardwareClass = " " }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bad := c
			tc.change(&bad)
			if _, err := NewManager(bad); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("accepted invalid config: %v", err)
			}
		})
	}
	unknown := c
	unknown.Accelerator = AcceleratorFacts{}
	unknown.WorkerGPU = 0
	if err := unknown.Validate(); err != nil {
		t.Fatalf("explicit unknown GPU with zero quota: %v", err)
	}
	discrete := c
	discrete.Accelerator.UnifiedMemory = false
	discrete.Accelerator.Backend = domain.BackendCUDA
	if err := discrete.Validate(); err != nil {
		t.Fatalf("discrete: %v", err)
	}
}

func TestExactAllocationAndPermanentPreQueueRejections(t *testing.T) {
	m := mustManager(t, testConfig())
	l := mustAcquire(t, m, Request{RAMBytes: 7_500_000_000, CPU: 4, GPU: 1, Backend: domain.BackendMetal})
	s := m.Snapshot()
	if s.Used != (Counters{7_500_000_000, 4_000_000, 1_000_000, 1, 1}) || s.Available != (Counters{0, 0, 0, 1, 0}) || !s.Active[0].Request.Heavy {
		t.Fatalf("accounting: %+v", s)
	}
	cases := []struct {
		r    Request
		want error
	}{
		{Request{RAMBytes: 7_500_000_001}, ErrOverBudget}, {Request{CPU: 4.00000001}, ErrOverBudget},
		{Request{GPU: 1.00000001, Backend: domain.BackendMetal}, ErrOverBudget}, {Request{RAMBytes: -1}, ErrInvalidRequest},
		{Request{CPU: math.NaN()}, ErrInvalidRequest}, {Request{GPU: math.Inf(1)}, ErrInvalidRequest},
		{Request{CPU: -1}, ErrInvalidRequest}, {Request{GPU: -1}, ErrInvalidRequest}, {Request{CPU: 1e20}, ErrInvalidRequest},
		{Request{GPU: .1}, ErrIncompatible}, {Request{GPU: .1, Backend: domain.BackendCUDA}, ErrIncompatible},
		{Request{GPU: .1, Backend: domain.BackendUnknown}, ErrIncompatible}, {Request{Backend: domain.BackendCUDA}, ErrIncompatible},
	}
	for _, tc := range cases {
		got, err := m.Acquire(context.Background(), tc.r)
		if got != nil || !errors.Is(err, tc.want) {
			t.Errorf("%+v: got lease=%v err=%v want %v", tc.r, got, err, tc.want)
		}
	}
	if len(m.Snapshot().Queue) != 0 {
		t.Fatal("permanent errors were queued")
	}
	mustRelease(t, l)
	if s := m.Snapshot(); s.Used != (Counters{}) || s.Available != s.Total {
		t.Fatalf("not restored: %+v", s)
	}
}

func TestMaxIntegerRAMDoesNotOverflowConcurrentAdmission(t *testing.T) {
	c := testConfig()
	c.TotalRAMBytes = math.MaxInt64
	c.WorkerRAMBytes = math.MaxInt64
	c.SystemReserveBytes, c.UserReserveBytes, c.DirectorReserveBytes = 0, 0, 0
	c.MaxHeavy = 2
	m := mustManager(t, c)
	held := mustAcquire(t, m, Request{RAMBytes: math.MaxInt64})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	next := asyncAcquire(m, ctx, Request{RAMBytes: 1})
	waitQueue(t, m, 1)
	if m.Snapshot().Used.RAMBytes != math.MaxInt64 || m.Snapshot().Available.RAMBytes != 0 {
		t.Fatal("RAM sum wrapped and admitted an extra byte")
	}
	mustRelease(t, held)
	a := receive(t, next)
	if a.err != nil || a.lease == nil || m.Snapshot().Used.RAMBytes != 1 {
		t.Fatalf("max integer release/admission: %+v %+v", a, m.Snapshot())
	}
	mustRelease(t, a.lease)
}

func TestFractionalConservativeQuotasAndNoReleaseDrift(t *testing.T) {
	c := testConfig()
	c.WorkerCPU = .3000009
	c.MaxParallel = 3
	c.MaxHeavy = 3
	m := mustManager(t, c)
	if m.Snapshot().Total.CPUUnits != 300000 {
		t.Fatal("capacity did not floor")
	}
	for run := 0; run < 100; run++ {
		leases := []Lease{mustAcquire(t, m, Request{CPU: .1}), mustAcquire(t, m, Request{CPU: .1}), mustAcquire(t, m, Request{CPU: .1})}
		if m.Snapshot().Used.CPUUnits != 300000 {
			t.Fatal("fractional accounting wrong")
		}
		for _, l := range leases {
			mustRelease(t, l)
		}
		if m.Snapshot().Used.CPUUnits != 0 {
			t.Fatal("release drift")
		}
	}
	l := mustAcquire(t, m, Request{CPU: math.Nextafter(.1, math.Inf(1)), GPU: .0000001, Backend: domain.BackendMetal})
	if got := m.Snapshot().Used; got.CPUUnits != 100001 || got.GPUUnits != 1 {
		t.Fatalf("did not ceil exact declarations: %+v", got)
	}
	mustRelease(t, l)
	for _, value := range []float64{math.Inf(1), math.NaN(), -1, math.MaxFloat64, 1e13} {
		if _, err := quantize(value, true); err == nil {
			t.Errorf("accepted overflow/invalid %v", value)
		}
	}
	// The last declaration below the boundary is valid; the next float is not.
	boundary := float64(math.MaxInt64) / 1e6
	if _, err := quantize(boundary, true); err != nil {
		t.Fatalf("safe boundary rejected: %v", err)
	}
	if _, err := quantize(math.Nextafter(boundary, math.Inf(1)), true); !errors.Is(err, ErrOverflow) {
		t.Fatalf("unsafe conversion boundary: %v", err)
	}
}

func TestHeavyClassificationSlotsAndExclusive(t *testing.T) {
	for _, r := range []Request{{RAMBytes: 2_000_000_000}, {Heavy: true}, {GPU: .1, Backend: domain.BackendMetal}} {
		m := mustManager(t, testConfig())
		l := mustAcquire(t, m, r)
		ctx, cancel := context.WithCancel(context.Background())
		ch := asyncAcquire(m, ctx, Request{Heavy: true})
		waitQueue(t, m, 1)
		if m.Snapshot().Used.Heavy != 1 {
			t.Fatal("heavy flag bypassed")
		}
		cancel()
		if a := receive(t, ch); a.lease != nil || !errors.Is(a.err, context.Canceled) {
			t.Fatalf("cancel: %+v", a)
		}
		mustRelease(t, l)
	}
	m := mustManager(t, testConfig())
	a := mustAcquire(t, m, Request{})
	b := mustAcquire(t, m, Request{})
	ctx, cancel := context.WithCancel(context.Background())
	ch := asyncAcquire(m, ctx, Request{})
	waitQueue(t, m, 1)
	if m.Snapshot().Used.Slots != 2 {
		t.Fatal("zero requests bypass slot count")
	}
	cancel()
	receive(t, ch)
	mustRelease(t, a)
	mustRelease(t, b)
	light := mustAcquire(t, m, Request{RAMBytes: 1})
	exCtx, exCancel := context.WithCancel(context.Background())
	defer exCancel()
	ex := asyncAcquire(m, exCtx, Request{Exclusive: true})
	waitQueue(t, m, 1)
	laterCtx, laterCancel := context.WithCancel(context.Background())
	defer laterCancel()
	later := asyncAcquire(m, laterCtx, Request{})
	waitQueue(t, m, 2)
	mustRelease(t, light)
	exclusive := receive(t, ex)
	if exclusive.err != nil || exclusive.lease == nil || !m.Snapshot().Exclusive || len(m.Snapshot().Queue) != 1 {
		t.Fatalf("exclusive: %+v %+v", exclusive, m.Snapshot())
	}
	mustRelease(t, exclusive.lease)
	last := receive(t, later)
	if last.err != nil {
		t.Fatal(last.err)
	}
	mustRelease(t, last.lease)
}

func TestFIFOHeadCancellationQueueBoundAndNoBypass(t *testing.T) {
	c := testConfig()
	c.QueueLimit = 2
	c.MaxParallel = 4
	m := mustManager(t, c)
	held := mustAcquire(t, m, Request{RAMBytes: 6 * GB})
	headCtx, headCancel := context.WithCancel(context.Background())
	defer headCancel()
	head := asyncAcquire(m, headCtx, Request{RAMBytes: 2 * GB})
	waitQueue(t, m, 1)
	tailCtx, tailCancel := context.WithCancel(context.Background())
	defer tailCancel()
	tail := asyncAcquire(m, tailCtx, Request{RAMBytes: 1})
	waitQueue(t, m, 2)
	if m.Snapshot().Used.Slots != 1 {
		t.Fatal("small tail bypassed head")
	}
	if l, e := m.Acquire(context.Background(), Request{}); l != nil || !errors.Is(e, ErrQueueFull) {
		t.Fatalf("queue full: %v %v", l, e)
	}
	headCancel()
	if a := receive(t, head); a.lease != nil || !errors.Is(a.err, context.Canceled) {
		t.Fatalf("head cancellation: %+v", a)
	}
	a := receive(t, tail)
	if a.err != nil {
		t.Fatal(a.err)
	}
	if len(m.Snapshot().Queue) != 0 || m.Snapshot().Used.Slots != 2 {
		t.Fatal("cancel did not wake next")
	}
	mustRelease(t, a.lease)
	mustRelease(t, held)
}

func TestFIFOGrantOrderAndCancelNonHead(t *testing.T) {
	c := testConfig()
	c.MaxParallel = 1
	m := mustManager(t, c)
	held := mustAcquire(t, m, Request{})
	oneCtx, oneCancel := context.WithCancel(context.Background())
	defer oneCancel()
	one := asyncAcquire(m, oneCtx, Request{})
	waitQueue(t, m, 1)
	twoCtx, twoCancel := context.WithCancel(context.Background())
	two := asyncAcquire(m, twoCtx, Request{})
	waitQueue(t, m, 2)
	threeCtx, threeCancel := context.WithCancel(context.Background())
	defer threeCancel()
	three := asyncAcquire(m, threeCtx, Request{})
	waitQueue(t, m, 3)
	twoCancel()
	if a := receive(t, two); !errors.Is(a.err, context.Canceled) {
		t.Fatalf("nonhead cancel: %+v", a)
	}
	waitQueue(t, m, 2)
	mustRelease(t, held)
	a := receive(t, one)
	if a.err != nil || len(m.Snapshot().Queue) != 1 {
		t.Fatalf("first: %+v", a)
	}
	mustRelease(t, a.lease)
	b := receive(t, three)
	if b.err != nil {
		t.Fatal(b.err)
	}
	mustRelease(t, b.lease)
}

// Immediate grant performs Err at entry, after locking, and before handoff.
// Deterministically cancel at the latter to pin rollback, not random sleeps.
type handoffContext struct {
	context.Context
	cancel context.CancelFunc
	calls  atomic.Int32
}

func (c *handoffContext) Err() error {
	if c.calls.Add(1) == 3 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestObservedGrantCancellationRollbackAndOwnershipAfterHandoff(t *testing.T) {
	m := mustManager(t, testConfig())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	racing := &handoffContext{Context: ctx, cancel: cancel}
	l, err := m.Acquire(racing, Request{RAMBytes: GB, CPU: 1, GPU: .5, Backend: domain.BackendMetal, Exclusive: true})
	if l != nil || !errors.Is(err, context.Canceled) || m.Snapshot().Used != (Counters{}) || m.Snapshot().Exclusive {
		t.Fatalf("rollback: %v %v %+v", l, err, m.Snapshot())
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	l, err = m.Acquire(ctx2, Request{RAMBytes: GB})
	if err != nil {
		t.Fatal(err)
	}
	cancel2()
	if m.Snapshot().Used.RAMBytes != GB {
		t.Fatal("handoff lease auto-expired")
	}
	mustRelease(t, l)
	if l, e := m.Acquire(ctx2, Request{}); l != nil || !errors.Is(e, context.Canceled) {
		t.Fatalf("already canceled: %v %v", l, e)
	}
	if l, e := m.Acquire(nil, Request{}); l != nil || !errors.Is(e, ErrInvalidRequest) {
		t.Fatalf("nil context: %v %v", l, e)
	}
}

func TestQueuedGrantCancellationRollsBackAllResourcesAndWakesNext(t *testing.T) {
	c := testConfig()
	c.MaxParallel = 1
	m := mustManager(t, c)
	held := mustAcquire(t, m, Request{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := asyncAcquire(m, ctx, Request{RAMBytes: GB, CPU: 1, GPU: .5, Backend: domain.BackendMetal, Exclusive: true})
	waitQueue(t, m, 1)
	nextCtx, nextCancel := context.WithCancel(context.Background())
	defer nextCancel()
	next := asyncAcquire(m, nextCtx, Request{RAMBytes: 1})
	waitQueue(t, m, 2)
	// The ownership mutex is the barrier: notify a real queued grant, then
	// cancel before Acquire can take that mutex for handoff. No guessed sleeps.
	m.mu.Lock()
	m.release(held.(*budgetLease).token)
	m.schedule()
	granted := m.used
	cancel()
	m.mu.Unlock()
	if granted != (Counters{RAMBytes: GB, CPUUnits: 1_000_000, GPUUnits: 500_000, Slots: 1, Heavy: 1}) {
		t.Fatalf("test did not race a full grant: %+v", granted)
	}
	if a := receive(t, first); a.lease != nil || !errors.Is(a.err, context.Canceled) {
		t.Fatalf("canceled grant handed off: %+v", a)
	}
	a := receive(t, next)
	if a.err != nil || a.lease == nil || m.Snapshot().Used != (Counters{RAMBytes: 1, Slots: 1}) || m.Snapshot().Exclusive {
		t.Fatalf("rollback failed to wake next/clear quotas: %+v %+v", a, m.Snapshot())
	}
	mustRelease(t, held) // old, already internally released token remains harmless
	mustRelease(t, a.lease)
}

func TestConcurrentAllocationReleaseAndUniqueOwnedTokens(t *testing.T) {
	c := testConfig()
	c.MaxParallel = 64
	c.MaxHeavy = 64
	c.QueueLimit = 64
	m := mustManager(t, c)
	const n = 48
	start := make(chan struct{})
	results := make(chan acquisition, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			l, e := m.Acquire(context.Background(), Request{RAMBytes: 1, CPU: .01})
			results <- acquisition{l, e}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	leases := make([]Lease, 0, n)
	ids := map[string]bool{}
	for a := range results {
		if a.err != nil || a.lease == nil {
			t.Fatalf("acquire: %+v", a)
		}
		if ids[a.lease.ID()] {
			t.Fatal("duplicate allocation token")
		}
		ids[a.lease.ID()] = true
		leases = append(leases, a.lease)
	}
	if m.Snapshot().Used != (Counters{RAMBytes: 48, CPUUnits: 480000, Slots: 48}) {
		t.Fatalf("concurrent totals: %+v", m.Snapshot().Used)
	}
	start = make(chan struct{})
	errs := make(chan error, n*4)
	for _, l := range leases {
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func(l Lease) { defer wg.Done(); <-start; errs <- l.Release() }(l)
		}
	}
	close(start)
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Error(e)
		}
	}
	if m.Snapshot().Used != (Counters{}) {
		t.Fatal("concurrent/idempotent release leaked or double refunded")
	}
	fresh := mustAcquire(t, m, Request{RAMBytes: 100})
	for _, l := range leases {
		mustRelease(t, l)
	}
	if m.Snapshot().Used.RAMBytes != 100 {
		t.Fatal("old token released fresh allocation")
	}
	if ids[fresh.ID()] {
		t.Fatal("token reused")
	}
	mustRelease(t, fresh)
	m2 := mustManager(t, c)
	other := mustAcquire(t, m2, Request{})
	if ids[other.ID()] {
		t.Fatal("cross-manager ID collision")
	}
	mustRelease(t, other)
}

func TestConfigSnapshotOwnershipAndZeroQueue(t *testing.T) {
	c := testConfig()
	m := mustManager(t, c)
	c.WorkerRAMBytes = 1
	c.Accelerator.Backend = domain.BackendCUDA
	l := mustAcquire(t, m, Request{RAMBytes: 5, CPU: .5})
	s := m.Snapshot()
	s.Config.WorkerRAMBytes = 1
	s.Active[0].Request.RAMBytes = 999
	s.Active[0].Reserved.CPUUnits = 99
	s.Available.RAMBytes = 0
	got := m.Snapshot()
	if got.Config.WorkerRAMBytes != 7_500_000_000 || got.Active[0].Request.RAMBytes != 5 || got.Active[0].Reserved.CPUUnits != 500000 || got.Available.RAMBytes != 7_499_999_995 {
		t.Fatalf("snapshot aliased: %+v", got)
	}
	copyConfig := m.Config()
	copyConfig.MaxParallel = 100
	if m.Config().MaxParallel != 2 {
		t.Fatal("config aliased")
	}
	ctx, cancel := context.WithCancel(context.Background())
	ch := asyncAcquire(m, ctx, Request{RAMBytes: 7_500_000_000})
	waitQueue(t, m, 1)
	s = m.Snapshot()
	s.Queue[0].Request.RAMBytes = 0
	s.Queue[0].Decision = "grant"
	if q := m.Snapshot().Queue[0]; q.Request.RAMBytes != 7_500_000_000 || q.Decision != "fifo_wait" {
		t.Fatal("queue snapshot aliased")
	}
	cancel()
	receive(t, ch)
	mustRelease(t, l)
	c = testConfig()
	c.QueueLimit = 0
	c.MaxParallel = 1
	m = mustManager(t, c)
	l = mustAcquire(t, m, Request{})
	if _, err := m.Acquire(context.Background(), Request{}); !errors.Is(err, ErrQueueFull) {
		t.Fatal("zero queue must not wait")
	}
	mustRelease(t, l)
	if !reflect.DeepEqual(m.Snapshot().Active, []Allocation{}) {
		t.Fatal("released active list nonempty")
	}
}
