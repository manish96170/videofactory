package changeset

import (
	"context"
	"reflect"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/review"
)

type Engine struct {
	provider IntentProvider
	mode     Mode
	timeout  time.Duration
}

// New requires an injected provider. Zero config defaults to review mode and a
// 5s cooperative timeout. A provider is called synchronously exactly once.
func New(provider IntentProvider, config Config) (*Engine, error) {
	if nilInterface(provider) {
		return nil, ErrInvalid
	}
	if config.Mode == "" {
		config.Mode = Review
	}
	if config.Mode != Review && config.Mode != Automatic {
		return nil, ErrInvalid
	}
	if config.Timeout == 0 {
		config.Timeout = 5 * time.Second
	}
	if config.Timeout < 0 || config.Timeout > time.Minute {
		return nil, ErrInvalid
	}
	return &Engine{provider: provider, mode: config.Mode, timeout: config.Timeout}, nil
}

// Propose routes both chat and feedback through the same intent provider and
// deterministic Validate gate. Error-accompanying and late results are ignored.
func (e *Engine) Propose(ctx context.Context, p domain.Project, req Request) (*Validated, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if e == nil || nilInterface(e.provider) {
		return nil, ErrInvalid
	}
	base, hash, err := snapshotProject(p)
	if err != nil {
		return nil, err
	}
	req = copyRequest(req)
	if err := validateRequest(base, req); err != nil {
		return nil, err
	}
	// The provider gets its own project AND request, not the engine's retained
	// authoritative snapshot/correlations. No callback holds a lifecycle lock.
	providerProject, _, err := snapshotProject(base)
	if err != nil {
		return nil, err
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	intent, callErr := e.provider.Propose(callCtx, IntentRequest{Request: copyRequest(req), Project: providerProject, BaseHash: hash})
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := callCtx.Err(); err != nil {
		return nil, err
	}
	if callErr != nil {
		return nil, ErrProvider
	}
	source := "director-chat"
	if req.Feedback != nil {
		source = "review-feedback"
	}
	return Validate(ctx, base, Proposal{BaseHash: hash, ChangeSet: domain.ChangeSet{
		ID: req.ID, ProjectID: base.ID, Source: source, Changes: intent.Changes,
		Status: domain.ChangeSetProposed, CreatedAt: req.CreatedAt,
	}})
}

// Execute is the mode-aware entry point. Automatic mode only applies the handle
// produced by Propose's successful validation. Review is the default.
func (e *Engine) Execute(ctx context.Context, p domain.Project, req Request) (Outcome, error) {
	v, err := e.Propose(ctx, p, req)
	if err != nil {
		return Outcome{}, err
	}
	if e.mode == Review {
		return Outcome{Validated: v}, nil
	}
	r, err := Apply(ctx, p, v)
	if err != nil {
		return Outcome{}, err
	}
	return Outcome{Validated: v, Applied: &r}, nil
}

func validateRequest(p domain.Project, req Request) error {
	if !handle(req.ID) || !handle(req.ProjectID) || req.ProjectID != p.ID || !validTime(req.CreatedAt) {
		return ErrInvalid
	}
	if req.Feedback != nil {
		// Approval records are review dispositions, never edit intent. Reject
		// before invoking a provider so automatic mode cannot turn one into edits.
		if req.Feedback.Type == domain.FeedbackTypeApproval {
			return ErrInvalid
		}
		if req.Text != "" || req.Target != (Target{}) || len(req.Feedback.Note) > MaxTextBytes || review.Validate(p, *req.Feedback) != nil {
			return ErrInvalid
		}
		return nil
	}
	if !text(req.Text, MaxTextBytes) {
		return ErrInvalid
	}
	t := req.Target
	if t.ShotID != "" && !handle(t.ShotID) || t.ElementID != "" && !handle(t.ElementID) || t.Duration != nil && t.Timestamp == nil {
		return ErrInvalid
	}
	if t.Timestamp != nil {
		return feedbackTarget(p, req, t)
	}
	if t.ElementID != "" {
		if p.Timeline == nil {
			return ErrInvalid
		}
		for _, element := range p.Timeline.Elements {
			if element.ID == t.ElementID && (t.ShotID == "" || t.ShotID == element.ShotID) {
				return nil
			}
		}
		return ErrInvalid
	}
	if t.ShotID != "" {
		if p.Plan != nil {
			for _, shot := range p.Plan.Shots {
				if shot.ID == t.ShotID {
					return nil
				}
			}
		}
		return ErrInvalid
	}
	return nil
}

func feedbackTarget(p domain.Project, req Request, t Target) error {
	f := domain.ReviewFeedback{ID: req.ID, ProjectID: req.ProjectID, ShotID: t.ShotID,
		ElementID: t.ElementID, Timestamp: *t.Timestamp, Duration: t.Duration,
		Type: domain.FeedbackTypeChangeRequest, Note: req.Text, CreatedAt: req.CreatedAt}
	if review.Validate(p, f) != nil {
		return ErrInvalid
	}
	return nil
}

func copyRequest(req Request) Request {
	req.CreatedAt = req.CreatedAt.UTC().Round(0)
	if req.Target.Timestamp != nil {
		t := *req.Target.Timestamp
		req.Target.Timestamp = &t
	}
	if req.Target.Duration != nil {
		t := *req.Target.Duration
		req.Target.Duration = &t
	}
	if req.Feedback != nil {
		f := *req.Feedback
		f.CreatedAt = f.CreatedAt.UTC().Round(0)
		if f.Duration != nil {
			t := *f.Duration
			f.Duration = &t
		}
		req.Feedback = &f
	}
	return req
}

func nilInterface(v any) bool {
	if v == nil {
		return true
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflect.ValueOf(v).IsNil()
	}
	return false
}
