package event

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/idgen"
)

// ErrBusClosed is returned by Publish/Subscribe once the Bus has been
// closed via Close.
var ErrBusClosed = errors.New("event: bus is closed")

// DefaultBufferSize is the per-subscription channel capacity used when
// NewBus is called without WithBufferSize.
const DefaultBufferSize = 32

// Option configures a Bus constructed with NewBus.
type Option func(*Bus)

// WithBufferSize sets the per-subscription channel capacity. Non-positive
// values are ignored (DefaultBufferSize is kept).
func WithBufferSize(n int) Option {
	return func(b *Bus) {
		if n > 0 {
			b.bufferSize = n
		}
	}
}

type subscription struct {
	ch    chan Event
	types map[Type]struct{} // nil/empty = all types
}

func (s *subscription) wants(t Type) bool {
	if len(s.types) == 0 {
		return true
	}
	_, ok := s.types[t]
	return ok
}

// Bus is a simple, pragmatic in-process event bus implementing both
// Publisher and Subscriber (PHASES.md section 10 / Phase 02). It is not
// an event-sourcing system: core project/timeline state remains the
// source of truth, and the Bus exists purely for observability (TUI,
// logs, future control plane).
//
// The zero value is not usable; construct a Bus with NewBus.
//
// # Ordering
//
// Publish assigns each event a bus-wide, strictly increasing Seq under
// a single mutex before dispatching it to matching subscribers. Every
// subscriber observes the events of the types it asked for in exactly
// the order they were published to this Bus (delivery order == Seq
// order); events of types a subscriber didn't request are skipped, not
// buffered or reordered for it. Two concurrent Publish calls are
// serialized by the same mutex, so Seq (not wall-clock OccurredAt,
// whose resolution/monotonicity isn't guaranteed) is the authoritative
// order.
//
// # Backpressure
//
// Dispatch to each subscriber is non-blocking. If a subscriber's
// channel is full, the new event is dropped for that subscriber
// (drop-newest: the queued older events are left in place, so delivery
// order for what *is* received is unaffected) and TotalDropped is
// incremented; Publish itself never blocks waiting for subscriber
// buffer space. This keeps a slow TUI/log subscriber from stalling the
// rest of the system (PHASES.md section 11, "the TUI must remain
// responsive while workers run"). Publish is not wait-free in the
// stronger sense: it still briefly waits for Bus's internal mutex and
// does O(number of subscriptions) work per call.
//
// # Cancellation
//
// A subscription's channel is closed when its ctx is done or the Bus is
// Closed, whichever happens first — asynchronously; Close does not wait
// for every watcher goroutine to observe the signal before returning,
// though each one will. A context check at the start of Publish/
// Subscribe rejects already-cancelled contexts before doing any work;
// cancellation that happens while waiting for the internal mutex (an
// uninterruptible operation) is not retroactively honored, matching the
// "best-effort, checked at the start of the operation" cancellation
// style used elsewhere in this module (e.g. project.FileStore).
//
// # Payload ownership
//
// Event.Payload (and any mutable values nested within it) is handed to
// every matching subscriber and is not copied per-subscriber. Callers
// must treat a Payload passed to Publish as immutable from that point
// on: do not mutate it afterward, and do not assume exclusive access to
// it from a Subscribe channel, since other subscribers may be reading
// the same map concurrently.
type Bus struct {
	mu           sync.Mutex
	closed       bool
	closedCh     chan struct{}
	nextSeq      uint64
	nextSubID    uint64
	subs         map[uint64]*subscription
	bufferSize   int
	totalDropped uint64
}

// NewBus creates a ready-to-use Bus.
func NewBus(opts ...Option) *Bus {
	b := &Bus{
		subs:       make(map[uint64]*subscription),
		bufferSize: DefaultBufferSize,
		closedCh:   make(chan struct{}),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// Publish implements Publisher.
//
// If evt.ID or evt.OccurredAt are zero, Publish fills them in (a fresh
// idgen-generated ID, and time.Now()) before dispatch, so callers
// aren't required to stamp every event themselves. Callers that need
// deterministic IDs/timestamps for testing may set them explicitly;
// Publish leaves non-zero values untouched. evt.Seq is always
// overwritten by Publish regardless of any value the caller set.
//
// See the Bus doc comment ("Cancellation") for exactly when a
// cancelled ctx is and isn't honored, and ("Payload ownership") for the
// contract on evt.Payload after this call.
func (b *Bus) Publish(ctx context.Context, evt Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if evt.Type == "" {
		return errors.New("event: Type must not be empty")
	}

	if evt.ID.IsZero() {
		evt.ID = domain.ID(idgen.New())
	}
	if evt.OccurredAt.IsZero() {
		evt.OccurredAt = time.Now()
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	// Re-check after acquiring the mutex: a caller whose ctx was
	// cancelled while waiting for the lock should not have its event
	// dispatched (and should not consume a Seq value) just because it
	// happened to win the lock race.
	if err := ctx.Err(); err != nil {
		return err
	}
	if b.closed {
		return ErrBusClosed
	}

	b.nextSeq++
	evt.Seq = b.nextSeq

	for _, sub := range b.subs {
		if !sub.wants(evt.Type) {
			continue
		}
		select {
		case sub.ch <- evt:
		default:
			b.totalDropped++
		}
	}

	return nil
}

// Subscribe implements Subscriber. An empty types list subscribes to
// every event type. The returned channel is closed when ctx is done or
// the Bus is Closed.
func (b *Bus) Subscribe(ctx context.Context, types ...Type) (<-chan Event, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	b.mu.Lock()
	// Re-check after acquiring the mutex, for the same reason as in
	// Publish: don't register a subscription for a ctx that was
	// cancelled while this call was waiting for the lock.
	if err := ctx.Err(); err != nil {
		b.mu.Unlock()
		return nil, err
	}
	if b.closed {
		b.mu.Unlock()
		return nil, ErrBusClosed
	}

	typeSet := make(map[Type]struct{}, len(types))
	for _, t := range types {
		typeSet[t] = struct{}{}
	}

	id := b.nextSubID
	b.nextSubID++
	sub := &subscription{
		ch:    make(chan Event, b.bufferSize),
		types: typeSet,
	}
	b.subs[id] = sub
	closedCh := b.closedCh
	b.mu.Unlock()

	// Watch both this subscription's ctx and the whole Bus's closedCh,
	// so a subscriber whose ctx never cancels (e.g. context.Background)
	// doesn't leak a goroutine blocked forever once the Bus is closed
	// (PHASES.md section 4.10, "no uncontrolled goroutines").
	go func() {
		select {
		case <-ctx.Done():
		case <-closedCh:
		}
		b.unsubscribe(id)
	}()

	return sub.ch, nil
}

func (b *Bus) unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sub, ok := b.subs[id]
	if !ok {
		return // already removed, e.g. by Close
	}
	delete(b.subs, id)
	close(sub.ch)
}

// TotalDropped returns the number of (subscriber, event) deliveries
// dropped so far because a subscriber's channel was full. It is a
// coarse, bus-wide diagnostic, not a per-subscription count: Phase 02
// intentionally keeps this simple (PHASES.md section 10, "a pragmatic
// event-driven architecture", not "a complicated event-sourcing
// framework").
func (b *Bus) TotalDropped() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.totalDropped
}

// Close closes every open subscription's channel and makes future
// Publish/Subscribe calls return ErrBusClosed. Already-buffered events
// in a subscription's channel remain readable after Close; only the
// close signal is new. Close is idempotent, safe to call concurrently
// with Publish/Subscribe, and does not wait for watcher goroutines
// spawned by Subscribe to finish (they will, promptly, but Close
// doesn't join them).
func (b *Bus) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	for id, sub := range b.subs {
		close(sub.ch)
		delete(b.subs, id)
	}
	b.mu.Unlock()

	close(b.closedCh) // wake every Subscribe watcher goroutine so none leak

	return nil
}

var (
	_ Publisher  = &Bus{}
	_ Subscriber = &Bus{}
)
