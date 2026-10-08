package event

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestEventJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	evt := Event{
		ID:         domain.ID("evt-1"),
		Type:       TypeGenerationCompleted,
		ProjectID:  domain.ID("proj-1"),
		ShotID:     domain.ID("shot-17"),
		OccurredAt: now,
		Payload:    map[string]any{"durationMs": float64(1200)},
	}

	data, err := json.Marshal(evt)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Event
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Type != TypeGenerationCompleted || decoded.ShotID != domain.ID("shot-17") {
		t.Fatalf("round trip mismatch: %+v", decoded)
	}
	if !decoded.OccurredAt.Equal(now) {
		t.Fatalf("occurredAt mismatch: got %v want %v", decoded.OccurredAt, now)
	}
}

// fakePublisher/fakeSubscriber exist only to prove the interfaces are
// satisfiable and ergonomic to implement; the real event bus (including
// the channel-close-on-cancellation/subscription-end behavior documented
// on Subscriber) arrives in Phase 02. fakeSubscriber below is a
// compile-time shape check only — it does not itself close its channel
// on ctx cancellation, and must not be read as evidence of that
// lifecycle guarantee.
type fakePublisher struct {
	published []Event
}

func (f *fakePublisher) Publish(ctx context.Context, evt Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.published = append(f.published, evt)
	return nil
}

type fakeSubscriber struct {
	ch chan Event
}

func (f *fakeSubscriber) Subscribe(ctx context.Context, types ...Type) (<-chan Event, error) {
	return f.ch, nil
}

var (
	_ Publisher  = &fakePublisher{}
	_ Subscriber = &fakeSubscriber{}
)

func TestFakePublisherRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	pub := &fakePublisher{}
	if err := pub.Publish(ctx, Event{ID: domain.ID("evt-1"), Type: TypeShotCreated}); err == nil {
		t.Fatalf("expected cancelled context to produce an error")
	}
}
