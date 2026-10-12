package tui

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
)

type controlSubscriber func(context.Context, ...event.Type) (<-chan event.Event, error)

func (f controlSubscriber) Subscribe(ctx context.Context, types ...event.Type) (<-chan event.Event, error) {
	return f(ctx, types...)
}

func newTestControl(t *testing.T, app *App, cfg ControlConfig) *Control {
	t.Helper()
	cfg.ProjectID = "project-1"
	c, err := NewControl(app, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func controlFixture() ControlState {
	return ControlState{
		Project: domain.Project{ID: "project-1", Name: "Demo", Plan: &domain.VideoPlan{
			ID: "plan-1", ProjectID: "project-1", Shots: []domain.Shot{
				{ID: "shot-1", Index: 0, Category: domain.ShotCategoryCode, Duration: 2 * time.Second},
				{ID: "shot-2", Index: 1, Category: domain.ShotCategoryUI, Duration: 3 * time.Second},
			},
		}, Timeline: &domain.Timeline{ID: "timeline-1", ProjectID: "project-1", Entries: []domain.TimelineEntry{
			{ShotID: "shot-1", Start: 0, End: 2 * time.Second},
			{ShotID: "shot-2", Start: 2 * time.Second, End: 5 * time.Second},
		}}, Assets: []domain.Asset{{ID: "asset-1", Kind: domain.AssetKindImage, URI: "private-source"}}},
		Models: []Item{{ID: "model-1", Label: "Loaded"}},
		Status: Status{Code: "ready", Text: "Resources available; agent idle"},
	}
}

func TestControlRefreshProjectionOwnershipAndReconciliation(t *testing.T) {
	app := New()
	state := controlFixture()
	c := newTestControl(t, app, ControlConfig{ReadState: func(ctx context.Context, id domain.ID) (ControlState, error) {
		if ctx == nil || id != "project-1" {
			t.Fatal("reader context/identity missing")
		}
		return state, nil
	}})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := app.Snapshot()
	if snapshot.Views[TabPlan].Items[0].Label != "Project: Demo" || snapshot.Views[TabPlan].Items[1].Label != "Plan: plan-1" ||
		snapshot.Views[TabShots].Items[1].ID != "shot-2" || snapshot.Views[TabTimeline].Items[1].Label != "2s–5s" ||
		snapshot.Views[TabAssets].Items[0].Label != "image" || snapshot.Views[TabModels].Items[0].Label != "Loaded" || snapshot.Status != state.Status {
		t.Fatalf("projection = %+v", snapshot)
	}
	state.Models[0].Label = "Unloaded"
	state.Project.Plan.Shots[0].ID = "new-shot"
	if !reflect.DeepEqual(app.Snapshot(), snapshot) {
		t.Fatal("retained caller-owned state")
	}
	if err := app.Select(TabShots, "shot-2"); err != nil {
		t.Fatal(err)
	}
	state.Project.Plan.Shots[0], state.Project.Plan.Shots[1] = state.Project.Plan.Shots[1], state.Project.Plan.Shots[0]
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := app.Snapshot().Views[TabShots]; got.SelectedID != "shot-2" || got.SelectedIndex != 0 {
		t.Fatalf("selection = %+v", got)
	}
	state.Project.Plan, state.Project.Timeline, state.Project.Assets, state.Models = nil, nil, nil, nil
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, tab := range []Tab{TabShots, TabTimeline, TabAssets, TabModels} {
		if got := app.Snapshot().Views[tab]; len(got.Items) != 0 || got.SelectedIndex != -1 {
			t.Fatalf("stale view: %+v", got)
		}
	}
}

func TestControlRefreshRejectsInvalidErrorAndLateResults(t *testing.T) {
	for _, mode := range []string{"identity", "duplicate", "capacity", "adapter-error", "late-success"} {
		t.Run(mode, func(t *testing.T) {
			app := New()
			state := controlFixture()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			bad := false
			cause := errors.New("secret adapter details")
			c := newTestControl(t, app, ControlConfig{ReadState: func(context.Context, domain.ID) (ControlState, error) {
				if !bad {
					return state, nil
				}
				state.Project.Name = "must not appear"
				switch mode {
				case "identity":
					state.Project.ID = "another-project"
				case "duplicate":
					state.Models = append(state.Models, state.Models[0])
				case "capacity":
					state.Models = make([]Item, maxItemsPerTab+1)
				case "adapter-error":
					return state, cause
				case "late-success":
					cancel()
				}
				return state, nil
			}})
			if err := c.Refresh(ctx); err != nil {
				t.Fatal(err)
			}
			before := app.Snapshot()
			bad = true
			err := c.Refresh(ctx)
			if err == nil {
				t.Fatal("invalid/late result accepted")
			}
			if mode == "late-success" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			if mode == "adapter-error" && (!errors.Is(err, cause) || strings.Contains(err.Error(), "secret")) {
				t.Fatal(err)
			}
			after := app.Snapshot()
			if !reflect.DeepEqual(before.Views, after.Views) || before.Status != after.Status {
				t.Fatal("partial refresh published")
			}
			for _, entry := range after.Errors {
				if strings.Contains(entry.Message, "secret") {
					t.Fatal("raw error leaked")
				}
			}
		})
	}
}

func TestControlRefreshKeepsNavigationResponsive(t *testing.T) {
	app := New()
	entered := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newTestControl(t, app, ControlConfig{ReadState: func(ctx context.Context, _ domain.ID) (ControlState, error) {
		close(entered)
		<-ctx.Done()
		return controlFixture(), nil
	}})
	done := make(chan error, 1)
	go func() { done <- c.Refresh(ctx) }()
	waitControlSignal(t, entered)
	if err := c.Refresh(context.Background()); err != ErrControlBusy {
		t.Fatalf("concurrent refresh = %v", err)
	}
	if err := app.HandleKey(KeyTabNext); err != nil {
		t.Fatal(err)
	}
	if app.Snapshot().Focused != TabPlan {
		t.Fatal("navigation blocked")
	}
	cancel()
	if err := waitControlResult(t, done); err != context.Canceled {
		t.Fatal(err)
	}
}

func TestControlEventsAreFilteredBoundedAndNeverAuthoritative(t *testing.T) {
	app := New()
	state := controlFixture()
	stream := make(chan event.Event, maxEventLogs+8)
	var subscriptionCtx context.Context
	c := newTestControl(t, app, ControlConfig{
		ReadState: func(context.Context, domain.ID) (ControlState, error) { return state, nil },
		Events: controlSubscriber(func(ctx context.Context, _ ...event.Type) (<-chan event.Event, error) {
			subscriptionCtx = ctx
			return stream, nil
		}),
	})
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartTask(context.Background(), "owned-task", "work"); err != nil {
		t.Fatal(err)
	}
	defer app.FinishTask("owned-task", nil)
	payload := map[string]any{"taskId": "owned-task", "progress": 100, "secret": make(chan int)}
	for i := 0; i < maxEventLogs+3; i++ {
		stream <- event.Event{Type: event.TypeGenerationCompleted, ProjectID: "project-1", ShotID: "phantom-shot", Seq: 1, Payload: payload}
	}
	stream <- event.Event{Type: event.TypeShotCreated, ProjectID: "other-project"}
	stream <- event.Event{Type: event.TypeShotCreated}
	stream <- event.Event{Type: "secret-unknown-type", ProjectID: "project-1"}
	close(stream)
	if err := c.Observe(context.Background()); err != nil {
		t.Fatal(err)
	}
	if subscriptionCtx.Err() != context.Canceled {
		t.Fatal("subscription not cleaned up")
	}
	payload["progress"] = 0
	snapshot := app.Snapshot()
	logs := snapshot.Views[TabLogs].Items
	if len(logs) != maxEventLogs || logs[0].ID != "event-4" || logs[len(logs)-1].Label != "GENERATION_COMPLETED phantom-shot" {
		t.Fatalf("logs = %+v", logs)
	}
	if snapshot.Status.Code != "event_observed" || len(snapshot.Views[TabShots].Items) != 2 || snapshot.Tasks[0].Status != TaskRunning {
		t.Fatalf("events became authoritative: %+v", snapshot)
	}
	logs[0].Label = "caller mutation"
	if app.Snapshot().Views[TabLogs].Items[0].Label == "caller mutation" {
		t.Fatal("logs alias snapshot")
	}
}

func TestControlObserveCancellationAndErrors(t *testing.T) {
	for _, mode := range []string{"error", "nil-stream", "late-success", "waiting"} {
		t.Run(mode, func(t *testing.T) {
			app := New()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{})
			var child context.Context
			cause := errors.New("secret subscription error")
			c := newTestControl(t, app, ControlConfig{Events: controlSubscriber(func(got context.Context, _ ...event.Type) (<-chan event.Event, error) {
				child = got
				close(entered)
				switch mode {
				case "error":
					return make(chan event.Event), cause
				case "nil-stream":
					return nil, nil
				case "late-success":
					cancel()
				}
				return make(chan event.Event), nil
			})})
			done := make(chan error, 1)
			go func() { done <- c.Observe(ctx) }()
			waitControlSignal(t, entered)
			if mode == "waiting" {
				if err := c.Observe(context.Background()); err != ErrControlBusy {
					t.Fatal(err)
				}
				cancel()
			}
			err := waitControlResult(t, done)
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("observe error = %v", err)
			}
			if mode == "error" && !errors.Is(err, cause) {
				t.Fatal("lost error cause")
			}
			if child.Err() != context.Canceled {
				t.Fatal("subscription leaked")
			}
		})
	}
}

func TestControlTaskProgressCancellationAndErrorSafety(t *testing.T) {
	app := New()
	c := newTestControl(t, app, ControlConfig{})
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	var retainedReport func(int, string) error
	done := make(chan error, 1)
	go func() {
		done <- c.RunTask(context.Background(), "task", "work", func(ctx context.Context, report func(int, string) error) error {
			retainedReport = report
			if err := report(35, "working"); err != nil {
				return err
			}
			close(entered)
			select {
			case <-ctx.Done():
			case <-release:
			}
			return nil // late success must be rejected
		})
	}()
	waitControlSignal(t, entered)
	if app.Snapshot().Tasks[0].Progress != 35 {
		t.Fatal("progress missing")
	}
	if err := c.Cancel(context.Background(), "task"); err != nil {
		t.Fatal(err)
	}
	if err := waitControlResult(t, done); err != context.Canceled {
		t.Fatalf("late result = %v", err)
	}
	if app.Snapshot().Tasks[0].Status != TaskCanceled {
		t.Fatal("cancellation missing")
	}
	if err := retainedReport(99, "late"); err != ErrTaskFinished {
		t.Fatalf("late report = %v", err)
	}
	cause := errors.New("secret command details")
	err := c.RunTask(context.Background(), "failed", "work", func(context.Context, func(int, string) error) error { return cause })
	if !errors.Is(err, cause) || !errors.Is(err, ErrControlOperation) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("failure = %v", err)
	}
	snapshot := app.Snapshot()
	if snapshot.Tasks[1].Status != TaskFailed || strings.Contains(snapshot.Tasks[1].Message, "secret") || strings.Contains(snapshot.Errors[0].Message, "secret") {
		t.Fatalf("unsafe failure = %+v", snapshot)
	}
}

func TestControlSendToAIForwardsOnlyRequestAndContext(t *testing.T) {
	app := New()
	state := controlFixture()
	request := AIRequest{ProjectID: "project-1", ShotID: "shot-2", ElementID: "element-1", Timestamp: time.Second, Note: "Move the subtitle"}
	readCalls, aiCalls := 0, 0
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "value")
	c := newTestControl(t, app, ControlConfig{
		ReadState: func(context.Context, domain.ID) (ControlState, error) { readCalls++; return state, nil },
		SendToAI: func(got context.Context, req AIRequest) error {
			aiCalls++
			if got.Value(contextKey{}) != "value" || req != request {
				t.Fatal("lost context or target")
			}
			req.Note = "local callback edit"
			return nil
		},
	})
	if err := c.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	before := app.Snapshot()
	if err := c.SendToAI(ctx, "request-1", request); err != nil {
		t.Fatal(err)
	}
	after := app.Snapshot()
	if readCalls != 1 || aiCalls != 1 || request.Note != "Move the subtitle" || !reflect.DeepEqual(before.Views, after.Views) || !reflect.DeepEqual(state, controlFixture()) || after.Tasks[0].Status != TaskSucceeded {
		t.Fatal("AI action changed authoritative/view state")
	}
	for _, invalid := range []AIRequest{
		{ProjectID: "other", Note: "note"}, {ProjectID: "project-1", Note: " "},
		{ProjectID: "project-1", Note: strings.Repeat("x", maxAINoteBytes+1)},
		{ProjectID: "project-1", Note: "note", Timestamp: -1},
		{ProjectID: "project-1", Note: "note", ElementID: "element"},
	} {
		if err := c.SendToAI(ctx, "invalid", invalid); err != ErrInvalidControl {
			t.Fatalf("invalid request = %v", err)
		}
	}
	if aiCalls != 1 || len(app.Snapshot().Tasks) != 1 {
		t.Fatal("invalid request invoked callback")
	}
}

func TestControlInvalidDisabledAndCanceledInputs(t *testing.T) {
	var typedNil controlSubscriber
	if _, err := NewControl(New(), ControlConfig{ProjectID: "p", Events: typedNil}); err != ErrInvalidControl {
		t.Fatal(err)
	}
	if _, err := NewControl(nil, ControlConfig{ProjectID: "p"}); err != ErrInvalidControl {
		t.Fatal(err)
	}
	app := New()
	c := newTestControl(t, app, ControlConfig{})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	operations := []func(context.Context) error{
		c.Refresh, c.Observe,
		func(ctx context.Context) error { return c.SendToAI(ctx, "task", AIRequest{}) },
		func(ctx context.Context) error { return c.RunTask(ctx, "task", "work", nil) },
		func(ctx context.Context) error { return c.Cancel(ctx, "task") },
	}
	for _, operation := range operations {
		if err := operation(nil); err != ErrInvalidControl {
			t.Fatal(err)
		}
		if err := operation(canceled); err != context.Canceled {
			t.Fatal(err)
		}
	}
	for _, operation := range operations[:4] {
		if err := operation(context.Background()); err != ErrControlDisabled {
			t.Fatal(err)
		}
	}
	if len(app.Snapshot().Tasks) != 0 {
		t.Fatal("invalid calls started tasks")
	}
}

func TestControlConcurrentProgressAndNavigation(t *testing.T) {
	app := New()
	c := newTestControl(t, app, ControlConfig{})
	err := c.RunTask(context.Background(), "task", "work", func(ctx context.Context, report func(int, string) error) error {
		var wg sync.WaitGroup
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					if err := report(j, "working"); err != nil {
						t.Error(err)
					}
					_ = app.HandleKey(KeyTabNext)
					_ = app.Snapshot()
				}
			}()
		}
		wg.Wait()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := app.Snapshot().Tasks[0]; got.Status != TaskSucceeded || got.Progress != 100 {
		t.Fatalf("task = %+v", got)
	}
}

func waitControlSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not start")
	}
}

func waitControlResult(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("operation did not finish")
		return nil
	}
}
