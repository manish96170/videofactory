package decision

import (
	"context"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
	"github.com/manish96170/videofactory/idgen"
)

// AuditFailures returns the atomic count of failed/timed-out publications, not
// dropped deliveries. event.Bus is lossy and non-durable: a full subscriber
// buffer drops records even when Publish succeeds. Caller owns publisher Close
// and lifecycle; the engine adds no queue, persistence, or detached goroutines.
// Request/project/shot/provider handles must be nonsecret. Attempt ordinals are
// global within a Decide call (including failed resolution), starting at 1;
// Started uses 0, and terminal records use the final attempted ordinal.
func (e *Engine) AuditFailures() uint64 { return e.auditFailures.Load() }

func (e *Engine) audit(ctx context.Context, typ event.Type, req domain.DecisionRequest, provider string, ordinal int, outcome Outcome, action string, result *domain.DecisionResult) {
	if nilInterface(e.cfg.Publisher) {
		return
	}
	// Each record owns a fresh scalar-only map that the engine never mutates
	// after publication. Subscribers/publishers must also treat it as immutable.
	payload := map[string]any{
		"requestId": string(req.ID), "provider": provider, "attempt": ordinal,
		"outcome": string(outcome), "action": action,
		"yesThreshold": e.cfg.Policy.YesThreshold, "noThreshold": e.cfg.Policy.NoThreshold,
	}
	if result != nil {
		payload["score"] = result.Score
		payload["verdict"] = string(result.Decision)
	}
	child, cancel := context.WithTimeout(ctx, e.cfg.AuditTimeout)
	err := e.cfg.Publisher.Publish(child, event.Event{
		ID: domain.ID(idgen.New()), Type: typ, ProjectID: req.ProjectID, ShotID: req.ShotID,
		OccurredAt: time.Now(), Payload: payload,
	})
	childErr := child.Err()
	cancel()
	if err != nil || childErr != nil {
		e.auditFailures.Add(1)
	}
}
