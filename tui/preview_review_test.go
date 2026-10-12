package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/preview"
	reviewstore "github.com/manish96170/videofactory/review"
)

type reviewAppenderFunc func(context.Context, domain.Project, domain.ReviewFeedback) error

func (f reviewAppenderFunc) Append(ctx context.Context, project domain.Project, feedback domain.ReviewFeedback) error {
	return f(ctx, project, feedback)
}

func previewReviewFixture(t *testing.T) (preview.View, domain.Project) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("artifact store requires macOS or Linux")
	}
	root := t.TempDir()
	store, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := preview.NewReader(store)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	project := domain.Project{
		ID: "project-1", Name: "Preview test", CreatedAt: now, UpdatedAt: now,
		Plan: &domain.VideoPlan{ID: "plan-1", ProjectID: "project-1", FlowID: "flow", FramingID: "framing", CreatedAt: now,
			Shots: []domain.Shot{{ID: "shot-1", PlanID: "plan-1", Category: domain.ShotCategory("TEXT"), Duration: 4 * time.Second}}},
		Timeline: &domain.Timeline{ID: "timeline-1", ProjectID: "project-1",
			Entries: []domain.TimelineEntry{{ShotID: "shot-1", Start: 5 * time.Second, End: 9 * time.Second}},
			Elements: []domain.VisualElement{
				{ID: "text-1", ShotID: "shot-1", Type: "Text", Start: time.Second, End: 3 * time.Second},
				{ID: "overlay-1", ShotID: "shot-1", Type: "Image", Start: 2 * time.Second, End: 4 * time.Second},
			}},
	}
	if err := os.WriteFile(filepath.Join(root, "shot.mp4"), []byte("verified-preview-bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	key, err := artifact.BuildKey(artifact.KeyInput{
		Quality: domain.QualityDraft,
		Shot:    artifact.ShotInput{Duration: 4 * time.Second},
		Render:  artifact.RenderInput{Revision: "test", Width: 640, Height: 360, FPS: 24},
	})
	if err != nil {
		t.Fatal(err)
	}
	identity := artifact.Identity{Key: key, ProjectID: project.ID, ShotID: "shot-1", Quality: domain.QualityDraft}
	manifest := domain.RenderManifest{ID: "manifest-1", ShotID: "shot-1", MediaPath: "shot.mp4", CreatedAt: now, Elements: project.Timeline.Elements}
	_, err = store.Publish(context.Background(), artifact.PublishRequest{
		Identity: identity, MediaPath: "shot.mp4", Manifest: manifest,
		Validation: &domain.ValidationResult{ID: "validation-1", TargetID: "shot-1", Status: domain.ValidationPassed},
	})
	if err != nil {
		t.Fatal(err)
	}
	view, hit, err := reader.Resolve(context.Background(), project, identity)
	if err != nil || !hit {
		t.Fatalf("resolve preview: hit=%v err=%v", hit, err)
	}
	return view, project
}

func TestPreviewReviewLoadsAndProjectsTimestampSelection(t *testing.T) {
	view, _ := previewReviewFixture(t)
	adapter, err := NewPreviewReview(reviewAppenderFunc(func(context.Context, domain.Project, domain.ReviewFeedback) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Snapshot(); !errors.Is(err, ErrPreviewUnavailable) {
		t.Fatalf("empty snapshot error = %v", err)
	}
	if err := adapter.Load(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	state, err := adapter.Snapshot()
	if err != nil || state.ProjectID != "project-1" || state.ShotID != "shot-1" || state.Revision != view.Revision() || state.ShotTimestamp != 0 || state.ProjectTimestamp != 5*time.Second || len(state.Elements) != 0 {
		t.Fatalf("initial preview state = %+v, %v", state, err)
	}
	if err := adapter.SetShotTimestamp(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	state, _ = adapter.Snapshot()
	if state.ShotTimestamp != 2*time.Second || state.ProjectTimestamp != 7*time.Second || !reflect.DeepEqual(state.Elements, []PreviewElement{
		{ID: "text-1", Type: "Text", Start: time.Second, End: 3 * time.Second},
		{ID: "overlay-1", Type: "Image", Start: 2 * time.Second, End: 4 * time.Second},
	}) {
		t.Fatalf("shot-relative projection = %+v", state)
	}
	if err := adapter.SelectElement(context.Background(), "overlay-1"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetProjectTimestamp(context.Background(), 8*time.Second); err != nil {
		t.Fatal(err)
	}
	state, _ = adapter.Snapshot()
	if state.ProjectTimestamp != 8*time.Second || state.ShotTimestamp != 3*time.Second || len(state.Elements) != 1 || state.Elements[0].ID != "overlay-1" || state.SelectedElement != "overlay-1" {
		t.Fatalf("project-relative projection/selection = %+v", state)
	}
	if err := adapter.SelectElement(context.Background(), "text-1"); !errors.Is(err, ErrPreviewSelection) {
		t.Fatalf("inactive element selected: %v", err)
	}
	if err := adapter.SetShotTimestamp(context.Background(), 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	state, _ = adapter.Snapshot()
	if state.SelectedElement != "" || len(state.Elements) != 1 || state.Elements[0].ID != "text-1" {
		t.Fatalf("selection survived after its element became inactive: %+v", state)
	}
	before, _ := adapter.Snapshot()
	if err := adapter.SetProjectTimestamp(context.Background(), 9*time.Second); !errors.Is(err, ErrInvalidPreviewReview) {
		t.Fatalf("out-of-range timestamp = %v", err)
	}
	after, _ := adapter.Snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("invalid timestamp changed preview state")
	}
	before.Elements[0].Type = "caller mutation"
	after, _ = adapter.Snapshot()
	if after.Elements[0].Type != "Text" {
		t.Fatal("snapshot slice aliases adapter state")
	}
	if err := adapter.SelectElement(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := adapter.Snapshot(); got.SelectedElement != "" {
		t.Fatal("selection was not cleared")
	}
}

func TestPreviewReviewCaptureValidatesAndCopiesFeedback(t *testing.T) {
	view, project := previewReviewFixture(t)
	var appended domain.ReviewFeedback
	var appendedProject domain.Project
	calls := 0
	adapter, err := NewPreviewReview(reviewAppenderFunc(func(ctx context.Context, gotProject domain.Project, feedback domain.ReviewFeedback) error {
		calls++
		if ctx.Value("key") != "context-value" {
			t.Fatal("caller context was not forwarded")
		}
		appendedProject = gotProject
		appended = feedback
		if feedback.Duration != nil {
			*feedback.Duration = 99 * time.Second
		}
		gotProject.Timeline.Entries[0].Start = 0
		return nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Load(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetShotTimestamp(context.Background(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SelectElement(context.Background(), "overlay-1"); err != nil {
		t.Fatal(err)
	}
	duration := time.Second
	draft := FeedbackDraft{ID: "feedback-1", Duration: &duration, Type: domain.FeedbackTypeChangeRequest, Note: "Move this overlay", CreatedAt: time.Date(2026, 10, 12, 12, 0, 1, 0, time.FixedZone("test", 3600))}
	ctx := context.WithValue(context.Background(), "key", "context-value")
	feedback, err := adapter.Capture(ctx, project, draft)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || feedback.ProjectID != "project-1" || feedback.ShotID != "shot-1" || feedback.ElementID != "overlay-1" || feedback.Timestamp != 2*time.Second || feedback.Duration == nil || *feedback.Duration != time.Second || !feedback.CreatedAt.Equal(draft.CreatedAt) {
		t.Fatalf("captured feedback = %+v calls=%d", feedback, calls)
	}
	if appended.Duration == nil || *appended.Duration != 99*time.Second || appendedProject.Timeline.Entries[0].Start != 0 {
		t.Fatalf("appender test did not receive independent inputs: %+v %+v", appendedProject, appended)
	}
	if duration != time.Second || project.Timeline.Entries[0].Start != 5*time.Second || feedback.Duration == nil || *feedback.Duration != time.Second {
		t.Fatal("caller or returned feedback/project was aliased")
	}
	if err := reviewstore.Validate(project, feedback); err != nil {
		t.Fatalf("captured feedback is not valid against authority: %v", err)
	}

	invalid := []FeedbackDraft{
		{ID: "../bad", Type: domain.FeedbackTypeComment, Note: "note", CreatedAt: draft.CreatedAt},
		{ID: "feedback-2", Type: domain.FeedbackTypeComment, Note: " \n", CreatedAt: draft.CreatedAt},
		{ID: "feedback-3", Type: domain.FeedbackTypeApproval, Note: strings.Repeat("x", maxReviewNoteBytes+1), CreatedAt: draft.CreatedAt},
	}
	for _, bad := range invalid {
		if _, err := adapter.Capture(ctx, project, bad); !errors.Is(err, ErrInvalidPreviewReview) {
			t.Fatalf("invalid draft error = %v", err)
		}
	}
	wrongProject := project
	wrongProject.ID = "other-project"
	if _, err := adapter.Capture(ctx, wrongProject, draft); !errors.Is(err, ErrInvalidPreviewReview) {
		t.Fatalf("wrong project error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("invalid feedback reached append interface: calls=%d", calls)
	}
}

func TestPreviewReviewCaptureErrorsAreBoundedAndCancellationIsRespected(t *testing.T) {
	view, project := previewReviewFixture(t)
	cause := errors.New(strings.Repeat("secret", 100))
	calls := 0
	adapter, err := NewPreviewReview(reviewAppenderFunc(func(context.Context, domain.Project, domain.ReviewFeedback) error {
		calls++
		return cause
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Load(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	draft := FeedbackDraft{ID: "feedback-1", Type: domain.FeedbackTypeComment, Note: "Please adjust", CreatedAt: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)}
	err = func() error { _, err := adapter.Capture(context.Background(), project, draft); return err }()
	if !errors.Is(err, ErrPreviewOperation) || !errors.Is(err, cause) || strings.Contains(err.Error(), "secret") || len(err.Error()) > maxErrorText {
		t.Fatalf("unsafe append error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := adapter.Capture(ctx, project, draft); err != context.Canceled {
		t.Fatalf("canceled capture = %v", err)
	}
	if _, err := adapter.Capture(nil, project, draft); !errors.Is(err, ErrInvalidPreviewReview) {
		t.Fatalf("nil context capture = %v", err)
	}
	if calls != 1 {
		t.Fatalf("canceled capture invoked appender: calls=%d", calls)
	}
	if err := adapter.Load(ctx, view); err != context.Canceled {
		t.Fatalf("canceled load = %v", err)
	}
	if err := adapter.SetShotTimestamp(ctx, 0); err != context.Canceled {
		t.Fatalf("canceled timestamp = %v", err)
	}
}

func TestPreviewReviewCapturesThroughPhase10ReviewStore(t *testing.T) {
	view, project := previewReviewFixture(t)
	store, err := reviewstore.NewStore(reviewstore.StoreConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := NewPreviewReview(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Load(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SetProjectTimestamp(context.Background(), 7*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := adapter.SelectElement(context.Background(), "text-1"); err != nil {
		t.Fatal(err)
	}
	feedback, err := adapter.Capture(context.Background(), project, FeedbackDraft{
		ID: "feedback-store", Duration: ptrDuration(time.Second), Type: domain.FeedbackTypeComment,
		Note: "Review this text", CreatedAt: time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := store.List(context.Background(), project.ID)
	if err != nil || len(listed) != 1 || !reflect.DeepEqual(listed[0], feedback) {
		t.Fatalf("persisted feedback = %+v, err=%v; returned=%+v", listed, err, feedback)
	}
}

func TestPreviewReviewAppenderFailureDoesNotLeakRawError(t *testing.T) {
	view, project := previewReviewFixture(t)
	adapter, err := NewPreviewReview(reviewAppenderFunc(func(context.Context, domain.Project, domain.ReviewFeedback) error {
		return errors.New(strings.Repeat("x", 10_000))
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Load(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Capture(context.Background(), project, FeedbackDraft{ID: "feedback", Type: domain.FeedbackTypeComment, Note: "note", CreatedAt: time.Now().UTC()})
	if err == nil || len(err.Error()) > maxErrorText || strings.Contains(err.Error(), strings.Repeat("x", 100)) {
		t.Fatalf("unbounded error: len=%d err=%v", len(err.Error()), err)
	}
}

func TestPreviewReviewConcurrentProjectionAndSelection(t *testing.T) {
	view, _ := previewReviewFixture(t)
	adapter, err := NewPreviewReview(reviewAppenderFunc(func(context.Context, domain.Project, domain.ReviewFeedback) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.Load(context.Background(), view); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for n := 0; n < 100; n++ {
				if worker%2 == 0 {
					_ = adapter.SetShotTimestamp(context.Background(), time.Duration(n%4)*time.Second)
				} else {
					_, _ = adapter.Snapshot()
					_ = adapter.SelectElement(context.Background(), "overlay-1")
				}
			}
		}(i)
	}
	wg.Wait()
	state, err := adapter.Snapshot()
	if err != nil || state.ProjectID != "project-1" || state.ShotID != "shot-1" || state.Revision != view.Revision() {
		t.Fatalf("concurrent snapshot = %+v, %v", state, err)
	}
}

func TestPreviewReviewConstructionAndInvalidState(t *testing.T) {
	var typedNil *reviewstore.Store
	if _, err := NewPreviewReview(nil); !errors.Is(err, ErrInvalidPreviewReview) {
		t.Fatalf("nil appender = %v", err)
	}
	if _, err := NewPreviewReview(typedNil); !errors.Is(err, ErrInvalidPreviewReview) {
		t.Fatalf("typed-nil appender = %v", err)
	}
	var adapter *PreviewReview
	if err := adapter.Load(context.Background(), preview.View{}); !errors.Is(err, ErrInvalidPreviewReview) {
		t.Fatalf("nil adapter load = %v", err)
	}
	if _, err := adapter.Snapshot(); !errors.Is(err, ErrPreviewUnavailable) {
		t.Fatalf("nil adapter snapshot = %v", err)
	}
}

func ptrDuration(value time.Duration) *time.Duration { return &value }
