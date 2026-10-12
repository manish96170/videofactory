package tui

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/preview"
	reviewstore "github.com/manish96170/videofactory/review"
)

var (
	ErrInvalidPreviewReview = errors.New("tui: invalid preview or review request")
	ErrPreviewUnavailable   = errors.New("tui: preview is unavailable")
	ErrPreviewOperation     = errors.New("tui: preview/review operation failed")
	ErrPreviewSelection     = errors.New("tui: visual element is not active at the selected timestamp")
	ErrPreviewChanged       = errors.New("tui: preview changed during operation")
)

const (
	maxPreviewElements = maxItemsPerTab
	maxReviewNoteBytes = 4096
	maxProjectJSONSize = 16 << 20
)

// ReviewAppender is the append-only Phase 10 feedback boundary. Implementations
// must honor cancellation cooperatively, be safe for concurrent callers, and not
// retain or mutate inputs after returning. review.Store satisfies this interface.
type ReviewAppender interface {
	Append(context.Context, domain.Project, domain.ReviewFeedback) error
}

// PreviewElement is the bounded identity/timing projection needed for stable UI
// selection. Editable properties and renderer metadata are deliberately omitted.
type PreviewElement struct {
	ID    domain.ID
	Type  string
	Start time.Duration // shot-relative, end-exclusive interval [Start, End)
	End   time.Duration
}

// PreviewSnapshot is a detached control-plane projection of a Phase 10 View.
// Timestamps are both reported; feedback uses the shot-relative coordinate.
type PreviewSnapshot struct {
	ProjectID        domain.ID
	ShotID           domain.ID
	Revision         string
	ProjectTimestamp time.Duration
	ShotTimestamp    time.Duration
	Elements         []PreviewElement
	SelectedElement  domain.ID
}

// FeedbackDraft contains user-entered feedback metadata. Project/shot/element
// identity and timestamp are always derived from the current preview selection.
type FeedbackDraft struct {
	ID        domain.ID
	Duration  *time.Duration
	Type      domain.FeedbackType
	Note      string
	CreatedAt time.Time
}

// PreviewReview adapts immutable Phase 10 preview views and append-only review
// storage to a presentation-neutral, copied TUI preview state. It performs no
// player/web/terminal I/O and does not mutate the project or produce ChangeSets.
type PreviewReview struct {
	mu       sync.RWMutex
	appender ReviewAppender
	view     preview.View
	state    PreviewSnapshot
	loaded   bool
}

func NewPreviewReview(appender ReviewAppender) (*PreviewReview, error) {
	if nilReviewAppender(appender) {
		return nil, ErrInvalidPreviewReview
	}
	return &PreviewReview{appender: appender}, nil
}

// Load replaces the preview state with a detached Phase 10 view. It starts at
// the shot's first frame and clears an old element selection. Invalid or late
// loads leave the prior state unchanged.
func (p *PreviewReview) Load(ctx context.Context, view preview.View) error {
	if p == nil {
		return ErrInvalidPreviewReview
	}
	if err := previewReviewContext(ctx); err != nil {
		return err
	}
	identity := view.Identity()
	manifest := view.Manifest()
	if !validText(string(identity.ProjectID), maxIDBytes) || !validText(string(identity.ShotID), maxIDBytes) ||
		view.Revision() == "" || manifest.ShotID != identity.ShotID || len(manifest.Elements) > maxPreviewElements {
		return ErrInvalidPreviewReview
	}
	position, err := view.AtShot(0)
	if err != nil || position.ProjectID != identity.ProjectID || position.ShotID != identity.ShotID {
		return previewReviewFailure(ErrInvalidPreviewReview, err)
	}
	state, err := previewState(view, position)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	if err := ctx.Err(); err != nil {
		p.mu.Unlock()
		return err
	}
	p.view, p.state, p.loaded = view, state, true
	p.mu.Unlock()
	return nil
}

// SetShotTimestamp projects a shot-relative timestamp through the verified View.
// Existing selection is retained only if its stable element remains active.
func (p *PreviewReview) SetShotTimestamp(ctx context.Context, timestamp time.Duration) error {
	return p.setTimestamp(ctx, timestamp, false)
}

// SetProjectTimestamp projects a project-relative timestamp through the verified
// View and reports both project- and shot-relative coordinates.
func (p *PreviewReview) SetProjectTimestamp(ctx context.Context, timestamp time.Duration) error {
	return p.setTimestamp(ctx, timestamp, true)
}

func (p *PreviewReview) setTimestamp(ctx context.Context, timestamp time.Duration, projectRelative bool) error {
	if p == nil {
		return ErrPreviewUnavailable
	}
	if err := previewReviewContext(ctx); err != nil {
		return err
	}
	p.mu.RLock()
	if !p.loaded {
		p.mu.RUnlock()
		return ErrPreviewUnavailable
	}
	view, revision := p.view, p.state.Revision
	p.mu.RUnlock()

	var position preview.Position
	var err error
	if projectRelative {
		position, err = view.AtProject(timestamp)
	} else {
		position, err = view.AtShot(timestamp)
	}
	if err != nil {
		return previewReviewFailure(ErrInvalidPreviewReview, err)
	}
	state, err := previewState(view, position)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !p.loaded || p.state.Revision != revision {
		return ErrPreviewChanged
	}
	if containsPreviewElement(state.Elements, p.state.SelectedElement) {
		state.SelectedElement = p.state.SelectedElement
	}
	p.state = state
	return nil
}

// SelectElement selects an active element by its stable renderer-supplied ID.
// An empty ID clears the selection and is valid at any in-shot timestamp.
func (p *PreviewReview) SelectElement(ctx context.Context, id domain.ID) error {
	if p == nil {
		return ErrPreviewUnavailable
	}
	if err := previewReviewContext(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.loaded {
		return ErrPreviewUnavailable
	}
	if id != "" && !containsPreviewElement(p.state.Elements, id) {
		return ErrPreviewSelection
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.state.SelectedElement = id
	return nil
}

// Snapshot returns an owned copy suitable for a TUI view. Mutating its slice
// cannot alter the adapter's selection state.
func (p *PreviewReview) Snapshot() (PreviewSnapshot, error) {
	if p == nil {
		return PreviewSnapshot{}, ErrPreviewUnavailable
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.loaded {
		return PreviewSnapshot{}, ErrPreviewUnavailable
	}
	return clonePreviewSnapshot(p.state), nil
}

// Capture validates and appends feedback using the currently selected project,
// shot, timestamp and optional visual element. The supplied project is cloned,
// validated against the existing review contract, and independently cloned
// again before crossing the injected append-only boundary.
func (p *PreviewReview) Capture(ctx context.Context, authoritative domain.Project, draft FeedbackDraft) (domain.ReviewFeedback, error) {
	if err := previewReviewContext(ctx); err != nil {
		return domain.ReviewFeedback{}, err
	}
	if p == nil || len(draft.Note) > maxReviewNoteBytes || !utf8.ValidString(draft.Note) {
		return domain.ReviewFeedback{}, ErrInvalidPreviewReview
	}
	p.mu.RLock()
	if !p.loaded {
		p.mu.RUnlock()
		return domain.ReviewFeedback{}, ErrPreviewUnavailable
	}
	state := clonePreviewSnapshot(p.state)
	appender := p.appender
	p.mu.RUnlock()
	if authoritative.ID != state.ProjectID {
		return domain.ReviewFeedback{}, ErrInvalidPreviewReview
	}

	projectCopy, err := cloneProject(authoritative)
	if err != nil {
		return domain.ReviewFeedback{}, previewReviewFailure(ErrInvalidPreviewReview, err)
	}
	feedback := domain.ReviewFeedback{
		ID: draft.ID, ProjectID: state.ProjectID, ShotID: state.ShotID,
		ElementID: state.SelectedElement, Timestamp: state.ShotTimestamp,
		Duration: cloneDuration(draft.Duration), Type: draft.Type, Note: draft.Note,
		CreatedAt: draft.CreatedAt,
	}
	if err := ctx.Err(); err != nil {
		return domain.ReviewFeedback{}, err
	}
	if err := reviewstore.Validate(projectCopy, feedback); err != nil {
		return domain.ReviewFeedback{}, previewReviewFailure(ErrInvalidPreviewReview, err)
	}
	if err := ctx.Err(); err != nil {
		return domain.ReviewFeedback{}, err
	}
	appendProject, err := cloneProject(projectCopy)
	if err != nil {
		return domain.ReviewFeedback{}, previewReviewFailure(ErrInvalidPreviewReview, err)
	}
	if err := ctx.Err(); err != nil {
		return domain.ReviewFeedback{}, err
	}
	if err := appender.Append(ctx, appendProject, cloneFeedback(feedback)); err != nil {
		return domain.ReviewFeedback{}, previewReviewFailure(ErrPreviewOperation, err)
	}
	// Append implementations own their commit-point cancellation semantics. In
	// particular, a successful append after its commit point must remain success.
	return cloneFeedback(feedback), nil
}

func previewState(view preview.View, position preview.Position) (PreviewSnapshot, error) {
	manifest := view.Manifest()
	if len(manifest.Elements) > maxPreviewElements {
		return PreviewSnapshot{}, ErrInvalidPreviewReview
	}
	items := make([]PreviewElement, 0, len(position.Elements))
	seen := make(map[domain.ID]struct{}, len(manifest.Elements))
	for _, element := range manifest.Elements {
		if !validText(string(element.ID), maxIDBytes) || element.ShotID != position.ShotID || !validText(strings.TrimSpace(element.Type), maxLabelBytes) {
			return PreviewSnapshot{}, ErrInvalidPreviewReview
		}
		if _, exists := seen[element.ID]; exists {
			return PreviewSnapshot{}, ErrInvalidPreviewReview
		}
		seen[element.ID] = struct{}{}
	}
	for _, element := range position.Elements {
		items = append(items, PreviewElement{ID: element.ID, Type: truncate(strings.TrimSpace(element.Type), maxLabelBytes), Start: element.Start, End: element.End})
	}
	return PreviewSnapshot{
		ProjectID: position.ProjectID, ShotID: position.ShotID, Revision: view.Revision(),
		ProjectTimestamp: position.ProjectTimestamp, ShotTimestamp: position.ShotTimestamp,
		Elements: items,
	}, nil
}

func containsPreviewElement(elements []PreviewElement, id domain.ID) bool {
	if id == "" {
		return false
	}
	for _, element := range elements {
		if element.ID == id {
			return true
		}
	}
	return false
}

func clonePreviewSnapshot(snapshot PreviewSnapshot) PreviewSnapshot {
	snapshot.Elements = append([]PreviewElement(nil), snapshot.Elements...)
	return snapshot
}

func cloneFeedback(feedback domain.ReviewFeedback) domain.ReviewFeedback {
	feedback.Duration = cloneDuration(feedback.Duration)
	feedback.CreatedAt = feedback.CreatedAt.UTC().Round(0)
	return feedback
}

func cloneDuration(duration *time.Duration) *time.Duration {
	if duration == nil {
		return nil
	}
	copy := *duration
	return &copy
}

func cloneProject(project domain.Project) (domain.Project, error) {
	data, err := json.Marshal(project)
	if err != nil || len(data) > maxProjectJSONSize {
		return domain.Project{}, ErrInvalidPreviewReview
	}
	var copy domain.Project
	if err := json.Unmarshal(data, &copy); err != nil {
		return domain.Project{}, ErrInvalidPreviewReview
	}
	return copy, nil
}

func previewReviewContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidPreviewReview
	}
	return ctx.Err()
}

func nilReviewAppender(appender ReviewAppender) bool {
	if appender == nil {
		return true
	}
	value := reflect.ValueOf(appender)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func previewReviewFailure(kind, cause error) error {
	if cause == nil {
		return kind
	}
	return &previewReviewError{kind: kind, cause: cause}
}

// Error text is always the bounded core-owned sentinel; Unwrap preserves
// errors.Is/As for callers without exposing adapter-controlled diagnostics.
type previewReviewError struct{ kind, cause error }

func (e *previewReviewError) Error() string   { return e.kind.Error() }
func (e *previewReviewError) Unwrap() []error { return []error{e.kind, e.cause} }
