// Package event defines the Video Factory's execution-backbone events
// (PHASES.md section 10). Core project/timeline state remains the source
// of truth; events exist for observability (TUI, logs, future control
// plane), not as a sourcing mechanism.
//
// This package defines the event vocabulary, the Publisher/Subscriber
// contracts, and (as of Phase 02) a concrete in-process Bus implementing
// both with defined cancellation, backpressure, and ordering semantics.
// See Bus's doc comment for the exact guarantees.
package event

import (
	"context"
	"time"

	"github.com/manish96170/videofactory/domain"
)

// Type identifies a kind of event. It is the vocabulary from PHASES.md
// section 10.
type Type string

const (
	TypeProjectCreated  Type = "PROJECT_CREATED"
	TypePlanCreated     Type = "PLAN_CREATED"
	TypeShotCreated     Type = "SHOT_CREATED"
	TypeShotUpdated     Type = "SHOT_UPDATED"
	TypeShotInvalidated Type = "SHOT_INVALIDATED"

	TypeModelLoadStarted Type = "MODEL_LOAD_STARTED"
	TypeModelLoaded      Type = "MODEL_LOADED"
	TypeModelUnloaded    Type = "MODEL_UNLOADED"

	TypeGenerationStarted   Type = "GENERATION_STARTED"
	TypeGenerationProgress  Type = "GENERATION_PROGRESS"
	TypeGenerationCompleted Type = "GENERATION_COMPLETED"
	TypeGenerationFailed    Type = "GENERATION_FAILED"

	TypeValidationStarted   Type = "VALIDATION_STARTED"
	TypeValidationCompleted Type = "VALIDATION_COMPLETED"
	TypeValidationFailed    Type = "VALIDATION_FAILED"

	TypeTimelineUpdated Type = "TIMELINE_UPDATED"

	TypeRenderStarted   Type = "RENDER_STARTED"
	TypeRenderCompleted Type = "RENDER_COMPLETED"
)

// Event is a single observable occurrence in the system.
//
// Seq is assigned by Bus.Publish: a strictly increasing, bus-wide
// sequence number that establishes a total delivery order independent
// of wall-clock resolution (see Bus's doc comment). It is zero for an
// Event that hasn't been published through a Bus yet.
//
// Payload is shared (not deep-copied) with every subscriber that
// receives the event; see Bus's "Payload ownership" doc for the
// resulting immutability contract.
type Event struct {
	ID         domain.ID      `json:"id"`
	Type       Type           `json:"type"`
	ProjectID  domain.ID      `json:"projectId,omitempty"`
	ShotID     domain.ID      `json:"shotId,omitempty"`
	OccurredAt time.Time      `json:"occurredAt"`
	Seq        uint64         `json:"seq,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

// Publisher publishes events. Implementations (Phase 02) must honor ctx
// cancellation (PHASES.md section 4.8).
type Publisher interface {
	Publish(ctx context.Context, evt Event) error
}

// Subscriber receives events of the given types. The returned channel is
// closed when ctx is done or the subscription ends.
type Subscriber interface {
	Subscribe(ctx context.Context, types ...Type) (<-chan Event, error)
}
