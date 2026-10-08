// Package event defines the Video Factory's execution-backbone events
// (PHASES.md section 10). Core project/timeline state remains the source
// of truth; events exist for observability (TUI, logs, future control
// plane), not as a sourcing mechanism.
//
// This package only defines the event vocabulary and the Publisher/
// Subscriber contracts. The pub/sub implementation (cancellation,
// backpressure, ordering guarantees) is built in Phase 02.
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
type Event struct {
	ID         domain.ID      `json:"id"`
	Type       Type           `json:"type"`
	ProjectID  domain.ID      `json:"projectId,omitempty"`
	ShotID     domain.ID      `json:"shotId,omitempty"`
	OccurredAt time.Time      `json:"occurredAt"`
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
