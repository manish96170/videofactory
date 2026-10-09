package event

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"testing"
	"time"
)

// drain receives exactly n events from ch, or returns an error on
// timeout or if ch closes early. It never calls any testing.T method,
// so it is safe to call from a goroutine other than the one running the
// test (unlike t.Fatalf, which the testing package requires to be
// called only from the test's own goroutine).
func drain(ch <-chan Event, n int, timeout time.Duration) ([]Event, error) {
	var got []Event
	deadline := time.After(timeout)
	for len(got) < n {
		select {
		case evt, ok := <-ch:
			if !ok {
				return got, fmt.Errorf("channel closed after receiving %d/%d events", len(got), n)
			}
			got = append(got, evt)
		case <-deadline:
			return got, fmt.Errorf("timed out waiting for events: got %d/%d", len(got), n)
		}
	}
	return got, nil
}

// mustDrain is drain plus an immediate t.Fatalf on error. Only call
// this from the goroutine running the test itself.
func mustDrain(t *testing.T, ch <-chan Event, n int, timeout time.Duration) []Event {
	t.Helper()
	got, err := drain(ch, n, timeout)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return got
}

func TestBusPublishSubscribeBasic(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	ctx := context.Background()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish(ctx, Event{Type: TypeShotCreated, ShotID: "shot-1"}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	got := mustDrain(t, ch, 1, time.Second)
	if got[0].Type != TypeShotCreated || got[0].ShotID != "shot-1" {
		t.Fatalf("unexpected event: %+v", got[0])
	}
}

func TestBusPublishStampsIDAndOccurredAtWhenZero(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	ctx := context.Background()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	before := time.Now()
	if err := bus.Publish(ctx, Event{Type: TypeShotCreated}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := mustDrain(t, ch, 1, time.Second)[0]

	if got.ID.IsZero() {
		t.Fatalf("expected Publish to stamp a non-zero ID")
	}
	if got.OccurredAt.Before(before) || got.OccurredAt.After(time.Now()) {
		t.Fatalf("expected OccurredAt to be stamped to roughly now, got %v", got.OccurredAt)
	}
}

func TestBusPublishPreservesCallerSuppliedIDAndOccurredAt(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	ctx := context.Background()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	fixedTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := bus.Publish(ctx, Event{ID: "fixed-id", Type: TypeShotCreated, OccurredAt: fixedTime}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	got := mustDrain(t, ch, 1, time.Second)[0]

	if got.ID != "fixed-id" {
		t.Fatalf("expected caller-supplied ID to be preserved, got %q", got.ID)
	}
	if !got.OccurredAt.Equal(fixedTime) {
		t.Fatalf("expected caller-supplied OccurredAt to be preserved, got %v", got.OccurredAt)
	}
}

func TestBusPublishRejectsEmptyType(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	if err := bus.Publish(context.Background(), Event{}); err == nil {
		t.Fatalf("expected error for empty Type")
	}
}

func TestBusPublishRespectsCancellation(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := bus.Publish(ctx, Event{Type: TypeShotCreated}); err == nil {
		t.Fatalf("expected error for cancelled context")
	}
}

func TestBusSubscribeRespectsCancellation(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := bus.Subscribe(ctx); err == nil {
		t.Fatalf("expected error for cancelled context")
	}
}

func TestBusSubscribeTypeFiltering(t *testing.T) {
	bus := NewBus()
	defer bus.Close()
	ctx := context.Background()

	shotsOnly, err := bus.Subscribe(ctx, TypeShotCreated)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	everything, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Publish(ctx, Event{Type: TypeShotCreated}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if err := bus.Publish(ctx, Event{Type: TypeRenderStarted}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	gotEverything := mustDrain(t, everything, 2, time.Second)
	if gotEverything[0].Type != TypeShotCreated || gotEverything[1].Type != TypeRenderStarted {
		t.Fatalf("unexpected events on the all-types subscription: %+v", gotEverything)
	}

	gotShotsOnly := mustDrain(t, shotsOnly, 1, time.Second)
	if gotShotsOnly[0].Type != TypeShotCreated {
		t.Fatalf("unexpected event on the filtered subscription: %+v", gotShotsOnly[0])
	}
	select {
	case extra := <-shotsOnly:
		t.Fatalf("expected no RENDER_STARTED event on the filtered subscription, got %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestBusOrderingGuarantee documents and verifies the ordering semantics
// from Bus's doc comment: a subscriber sees the events it's interested
// in in exactly published (Seq) order, with no gaps, when nothing is
// dropped.
func TestBusOrderingGuarantee(t *testing.T) {
	bus := NewBus(WithBufferSize(256))
	defer bus.Close()
	ctx := context.Background()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	const n = 200
	for i := 0; i < n; i++ {
		if err := bus.Publish(ctx, Event{Type: TypeShotCreated, Payload: map[string]any{"i": i}}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	got := mustDrain(t, ch, n, 2*time.Second)
	for i, evt := range got {
		if evt.Seq != uint64(i+1) {
			t.Fatalf("event %d: Seq = %d, want %d (exact publish order, no gaps)", i, evt.Seq, i+1)
		}
		if payloadI, ok := evt.Payload["i"].(int); !ok || payloadI != i {
			t.Fatalf("event %d: Payload[\"i\"] = %v, want %d (delivery order should match publish order)", i, evt.Payload["i"], i)
		}
	}
}

// TestBusConcurrentPublishersPreserveTotalOrder publishes from many
// goroutines concurrently, released together via a start barrier to
// maximize contention, and verifies the single subscriber still sees
// the exact Seq sequence 1..total with no duplicates, gaps, or
// reordering - i.e. the mutex genuinely serializes Seq assignment *and*
// dispatch under concurrency.
func TestBusConcurrentPublishersPreserveTotalOrder(t *testing.T) {
	bus := NewBus(WithBufferSize(2000))
	defer bus.Close()
	ctx := context.Background()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	const publishers = 20
	const perPublisher = 50
	const total = publishers * perPublisher

	start := make(chan struct{})
	errs := make(chan error, publishers)
	var wg sync.WaitGroup
	for p := 0; p < publishers; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start // released together, to maximize lock contention
			for i := 0; i < perPublisher; i++ {
				if err := bus.Publish(ctx, Event{Type: TypeShotCreated}); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Publish: %v", err)
	}

	got := mustDrain(t, ch, total, 5*time.Second)
	for i, evt := range got {
		if evt.Seq != uint64(i+1) {
			t.Fatalf("event %d: Seq = %d, want %d (expected exact 1..%d sequence with no duplicates/gaps/reordering)", i, evt.Seq, i+1, total)
		}
	}
}

// TestBusConcurrentSubscribers exercises many concurrent Subscribe
// calls alongside concurrent Publish calls, under -race. Each
// subscriber goroutine reports failures back to the test goroutine via
// a channel rather than calling any t.Fatal*/t.FailNow method itself,
// since the testing package requires those to be called only from the
// goroutine running the test.
func TestBusConcurrentSubscribers(t *testing.T) {
	bus := NewBus(WithBufferSize(100))
	defer bus.Close()
	ctx := context.Background()

	const subscribers = 30
	const events = 40

	ready := make(chan struct{}, subscribers)
	errs := make(chan error, subscribers)
	var wg sync.WaitGroup
	for s := 0; s < subscribers; s++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, err := bus.Subscribe(ctx)
			if err != nil {
				errs <- fmt.Errorf("Subscribe: %w", err)
				return
			}
			ready <- struct{}{}
			got, err := drain(ch, events, 5*time.Second)
			if err != nil {
				errs <- err
				return
			}
			for i, evt := range got {
				if evt.Seq != uint64(i+1) {
					errs <- fmt.Errorf("event %d: Seq = %d, want %d", i, evt.Seq, i+1)
					return
				}
			}
		}()
	}

	// Wait for every subscriber to have actually registered before
	// publishing, instead of guessing with a fixed sleep.
	for i := 0; i < subscribers; i++ {
		<-ready
	}

	for i := 0; i < events; i++ {
		if err := bus.Publish(ctx, Event{Type: TypeShotCreated}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("subscriber failed: %v", err)
	}
}

func TestBusBackpressureDropsNewestWithoutBlocking(t *testing.T) {
	bus := NewBus(WithBufferSize(1))
	ctx := context.Background()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Publish 3 events into a buffer of size 1 without ever reading:
	// the first fills the buffer, the next two must be dropped, and
	// Publish must never block. Intentionally not deferring bus.Close()
	// here: if Publish ever regressed to blocking while holding its
	// internal mutex, Close() would also block acquiring that mutex,
	// turning a clean, fast test failure into a hang. Leaking this
	// bus's goroutines for the remainder of the test binary's run is an
	// acceptable tradeoff for a fast, unambiguous failure signal.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 3; i++ {
			if err := bus.Publish(ctx, Event{Type: TypeShotCreated, Payload: map[string]any{"i": i}}); err != nil {
				t.Errorf("Publish: %v", err)
			}
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("Publish blocked on a full subscriber channel")
	}

	if dropped := bus.TotalDropped(); dropped != 2 {
		t.Fatalf("TotalDropped() = %d, want 2", dropped)
	}

	got := mustDrain(t, ch, 1, time.Second)
	if i, ok := got[0].Payload["i"].(int); !ok || i != 0 {
		t.Fatalf("expected the retained event to be the first (oldest) one, got payload %+v", got[0].Payload)
	}

	_ = bus.Close()
}

func TestBusCloseClosesSubscriptionsAndRejectsFurtherUse(t *testing.T) {
	bus := NewBus()
	ctx := context.Background()

	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	if err := bus.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("expected subscription channel to be closed after Bus.Close")
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for subscription channel to close")
	}

	if err := bus.Publish(ctx, Event{Type: TypeShotCreated}); err != ErrBusClosed {
		t.Fatalf("Publish after Close() = %v, want ErrBusClosed", err)
	}
	if _, err := bus.Subscribe(ctx); err != ErrBusClosed {
		t.Fatalf("Subscribe after Close() = %v, want ErrBusClosed", err)
	}
}

func TestBusCloseIsIdempotent(t *testing.T) {
	bus := NewBus()
	if err := bus.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := bus.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestBusSubscriptionClosesOnContextCancel verifies the per-subscription
// cancellation path (as opposed to whole-Bus Close).
func TestBusSubscriptionClosesOnContextCancel(t *testing.T) {
	bus := NewBus()
	defer bus.Close()

	ctx, cancel := context.WithCancel(context.Background())
	ch, err := bus.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("expected channel to be closed after ctx cancellation")
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for channel to close after ctx cancellation")
	}
}

// TestBusCloseDoesNotLeakBackgroundContextWatchers proves - by actually
// counting goroutines, not merely observing that a channel closed -
// that Subscribe's watcher goroutines for context.Background()
// subscriptions terminate once the Bus is Closed, instead of blocking
// forever. A channel closing on its own (Close's direct loop over subs)
// would not, by itself, demonstrate this: the watcher goroutine could
// still be leaked even though the channel is closed.
func TestBusCloseDoesNotLeakBackgroundContextWatchers(t *testing.T) {
	before := runtime.NumGoroutine()

	bus := NewBus()
	const n = 200
	for i := 0; i < n; i++ {
		if _, err := bus.Subscribe(context.Background()); err != nil {
			t.Fatalf("Subscribe %d: %v", i, err)
		}
	}

	// Goroutines should have grown by roughly n (one watcher per
	// subscription); tolerate scheduling noise with a loose lower bound
	// rather than asserting an exact count.
	afterSubscribe := runtime.NumGoroutine()
	if afterSubscribe < before+n/2 {
		t.Fatalf("expected goroutine count to grow by roughly %d after subscribing, got %d -> %d", n, before, afterSubscribe)
	}

	if err := bus.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Watcher goroutines exit promptly after Close (they just need to
	// wake from a select and call unsubscribe), but not necessarily
	// instantaneously, so poll with a generous timeout instead of
	// sleeping a fixed amount. The post-close tolerance is a small,
	// fixed noise allowance (unrelated runtime/GC goroutines), not a
	// fraction of n: allowing up to n/2 leaked watchers to still pass
	// would defeat the point of this test.
	const leakTolerance = 10
	deadline := time.Now().Add(2 * time.Second)
	for {
		runtime.GC() // encourage goroutine/stack bookkeeping to settle
		current := runtime.NumGoroutine()
		if current <= before+leakTolerance {
			return // back down near the pre-subscribe baseline: no leak
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine count did not return to baseline after Close: before=%d, after=%d (leaked watcher goroutines)", before, current)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
