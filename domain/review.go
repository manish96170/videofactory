package domain

import "time"

// FeedbackType classifies a piece of ReviewFeedback.
type FeedbackType string

const (
	FeedbackTypeComment       FeedbackType = "comment"
	FeedbackTypeChangeRequest FeedbackType = "change-request"
	FeedbackTypeApproval      FeedbackType = "approval"
)

// ReviewFeedback is user feedback captured from a preview surface,
// anchored to a timestamp/element when available (PHASES.md section
// 4.6.2). The visual player is an inspection surface; it must not become
// a second source of truth, so this struct always carries enough context
// (project/shot/element/timestamp) for the Director to resolve the target
// without guessing from pixels.
//
// Timestamp is shot-relative (0 = start of the shot) when ShotID is set,
// and project-timeline-relative when ShotID is empty. It serializes as
// integer nanoseconds, like other time.Duration fields in this package.
// See DECISIONS.md ADR-007.
type ReviewFeedback struct {
	ID        ID             `json:"id"`
	ProjectID ID             `json:"projectId"`
	ShotID    ID             `json:"shotId,omitempty"`
	ElementID ID             `json:"elementId,omitempty"`
	Timestamp time.Duration  `json:"timestamp"`
	Duration  *time.Duration `json:"duration,omitempty"`
	Type      FeedbackType   `json:"type"`
	Note      string         `json:"note"`
	CreatedAt time.Time      `json:"createdAt"`
}
