package tui

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
)

// ControlState is a read-only snapshot from core adapters. Models and Status are
// user-safe display projections (Status may include resource and agent status).
// The reader must return data that remains immutable until Refresh returns.
// Control retains only bounded display fields, never project maps or assets.
type ControlState struct {
	Project domain.Project
	Models  []Item
	Status  Status
}

// AIRequest carries explicit target context to a proposal-only callback. Time
// is shot-relative when ShotID is present, otherwise project-relative. A later
// core Director validates target existence; this client does not apply changes.
type AIRequest struct {
	ProjectID domain.ID
	ShotID    domain.ID
	ElementID domain.ID
	Timestamp time.Duration
	Note      string
}

// TaskFunc cooperatively runs in the caller's goroutine. report is concurrency
// safe and rejects updates after completion/cancellation. Callbacks must join
// any work they start before returning, and supply user-safe progress text.
type TaskFunc func(ctx context.Context, report func(progress int, message string) error) error

// ControlConfig contains optional capabilities. Missing callbacks disable the
// corresponding action. Dependencies must honor context cancellation and be
// concurrency safe; no callback is called while holding an App/Control lock.
type ControlConfig struct {
	ProjectID domain.ID
	ReadState func(context.Context, domain.ID) (ControlState, error)
	Events    event.Subscriber
	SendToAI  func(context.Context, AIRequest) error
}

var (
	ErrInvalidControl   = errors.New("tui: invalid control configuration or request")
	ErrControlDisabled  = errors.New("tui: control action unavailable")
	ErrControlBusy      = errors.New("tui: control action already running")
	ErrControlRead      = errors.New("tui: state refresh failed")
	ErrControlEvents    = errors.New("tui: event subscription failed")
	ErrControlOperation = errors.New("tui: command failed")
)

const maxEventLogs = 256
const maxAINoteBytes = 4096

// Control adapts injected core operations to App. Refresh explicitly reconciles
// authoritative views; Observe projects best-effort event notices only. Missing,
// duplicate or reordered events never create shots or complete owned tasks.
// Methods start no goroutines: the host owns invocation, cancellation and joining.
// Use NewControl; associate only one Control with an App.
type Control struct {
	app        *App
	cfg        ControlConfig
	mu         sync.Mutex
	refreshing bool
	observing  bool
	logs       []Item
	nextLog    uint64
}

func NewControl(app *App, cfg ControlConfig) (*Control, error) {
	if app == nil || !validText(string(cfg.ProjectID), maxIDBytes) || nilControlDependency(cfg.Events) {
		return nil, ErrInvalidControl
	}
	return &Control{app: app, cfg: cfg}, nil
}

// Refresh reads core state and atomically replaces Plan, Shots, Timeline, Assets,
// Models and Status, preserving stable selections. Chat/Logs are independent.
// Errors, invalid data, and late successes preserve the previous projection.
// Concurrent refreshes are rejected rather than publishing out-of-order reads.
func (c *Control) Refresh(ctx context.Context) error {
	if err := controlContext(ctx); err != nil {
		return err
	}
	if c.cfg.ReadState == nil {
		return ErrControlDisabled
	}
	c.mu.Lock()
	if c.refreshing {
		c.mu.Unlock()
		return ErrControlBusy
	}
	c.refreshing = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.refreshing = false; c.mu.Unlock() }()
	state, err := c.cfg.ReadState(ctx, c.cfg.ProjectID)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return c.failure("refresh_failed", ErrControlRead, err)
	}
	views, status, err := c.projectState(state)
	if err != nil {
		return c.failure("refresh_failed", ErrControlRead, err)
	}
	c.app.mu.Lock()
	defer c.app.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, tab := range []Tab{TabPlan, TabShots, TabTimeline, TabAssets, TabModels} {
		replaceControlItems(&c.app.tabs[tab], views[tab])
	}
	c.app.status = status
	return nil
}

func (c *Control) projectState(state ControlState) ([len(tabOrder)][]Item, Status, error) {
	var views [len(tabOrder)][]Item
	p := state.Project
	if p.ID != c.cfg.ProjectID {
		return views, Status{}, ErrInvalidControl
	}
	// Project identity is displayed alongside plan identity, including projects
	// without a plan. No raw asset URIs, properties or metadata are displayed.
	views[TabPlan] = []Item{{ID: "project", Label: "Project: " + p.Name}}
	if p.Plan != nil {
		if p.Plan.ProjectID != p.ID || !validText(string(p.Plan.ID), maxIDBytes) || len(p.Plan.Shots) > maxItemsPerTab {
			return views, Status{}, ErrInvalidControl
		}
		views[TabPlan] = append(views[TabPlan], Item{ID: "plan", Label: "Plan: " + string(p.Plan.ID)})
		for _, shot := range p.Plan.Shots {
			views[TabShots] = append(views[TabShots], Item{ID: string(shot.ID), Label: fmt.Sprintf("%d %s %s", shot.Index, shot.Category, shot.Duration)})
		}
	}
	if p.Timeline != nil {
		if p.Timeline.ProjectID != p.ID || len(p.Timeline.Entries) > maxItemsPerTab {
			return views, Status{}, ErrInvalidControl
		}
		for _, entry := range p.Timeline.Entries {
			views[TabTimeline] = append(views[TabTimeline], Item{ID: string(entry.ShotID), Label: fmt.Sprintf("%s–%s", entry.Start, entry.End)})
		}
	}
	if len(p.Assets) > maxItemsPerTab || len(state.Models) > maxItemsPerTab {
		return views, Status{}, ErrTooManyItems
	}
	for _, asset := range p.Assets {
		views[TabAssets] = append(views[TabAssets], Item{ID: string(asset.ID), Label: string(asset.Kind)})
	}
	views[TabModels] = append([]Item(nil), state.Models...)
	for tab := range views {
		seen := make(map[string]bool, len(views[tab]))
		for i := range views[tab] {
			item := &views[tab][i]
			if !validText(item.ID, maxIDBytes) || seen[item.ID] {
				return views, Status{}, ErrInvalidItem
			}
			seen[item.ID] = true
			item.Label = strings.TrimSpace(truncate(item.Label, maxLabelBytes))
			if item.Label == "" {
				item.Label = item.ID
			}
		}
	}
	status := state.Status
	if status == (Status{}) {
		status = Status{Code: "project_ready", Text: p.Name}
	}
	status.Code = truncate(status.Code, maxErrorCode)
	status.Text = truncate(status.Text, maxStatusBytes)
	return views, status, nil
}

// Observe subscribes until cancellation or channel closure. It cancels the
// subscription on every exit, rejects nil streams and permits one active stream.
// Global/unrelated events are ignored: each Control observes one exact project.
// Event payloads are deliberately never read/retained (the vocabulary has no
// standardized progress schema). Use RunTask's typed report for owned progress.
func (c *Control) Observe(ctx context.Context) error {
	if err := controlContext(ctx); err != nil {
		return err
	}
	if c.cfg.Events == nil {
		return ErrControlDisabled
	}
	c.mu.Lock()
	if c.observing {
		c.mu.Unlock()
		return ErrControlBusy
	}
	c.observing = true
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.observing = false; c.mu.Unlock() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := c.cfg.Events.Subscribe(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return c.failure("events_failed", ErrControlEvents, err)
	}
	if stream == nil {
		return c.failure("events_failed", ErrControlEvents, ErrInvalidControl)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case evt, ok := <-stream:
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if !ok {
				return nil
			}
			if evt.ProjectID != c.cfg.ProjectID {
				continue
			}
			c.projectEvent(evt)
		}
	}
}

func (c *Control) projectEvent(evt event.Event) {
	// Use a closed, core-owned display vocabulary; arbitrary types/payloads may
	// contain secrets. IDs are caller-declared nonsecret correlation handles.
	switch evt.Type {
	case event.TypeProjectCreated, event.TypePlanCreated, event.TypeShotCreated,
		event.TypeShotUpdated, event.TypeShotInvalidated, event.TypeModelLoadStarted,
		event.TypeModelLoaded, event.TypeModelUnloaded, event.TypeGenerationStarted,
		event.TypeGenerationProgress, event.TypeGenerationCompleted, event.TypeGenerationFailed,
		event.TypeDecisionStarted, event.TypeDecisionAttemptCompleted, event.TypeDecisionCompleted,
		event.TypeDecisionFailed, event.TypeValidationStarted, event.TypeValidationCompleted,
		event.TypeValidationFailed, event.TypeTimelineUpdated, event.TypeRenderStarted, event.TypeRenderCompleted:
	default:
		return
	}
	label := string(evt.Type)
	if validText(string(evt.ShotID), maxIDBytes) {
		label += " " + string(evt.ShotID)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextLog++
	entry := Item{ID: "event-" + strconv.FormatUint(c.nextLog, 10), Label: truncate(label, maxLabelBytes)}
	if len(c.logs) == maxEventLogs {
		copy(c.logs, c.logs[1:])
		c.logs[len(c.logs)-1] = entry
	} else {
		c.logs = append(c.logs, entry)
	}
	c.app.mu.Lock()
	defer c.app.mu.Unlock()
	replaceControlItems(&c.app.tabs[TabLogs], append([]Item(nil), c.logs...))
	c.app.status = Status{Code: "event_observed", Text: truncate(label, maxStatusBytes)}
}

// RunTask executes a context-aware command with App-owned cancellation and
// bounded progress. Dependency error text is never copied to UI diagnostics.
func (c *Control) RunTask(ctx context.Context, id, name string, run TaskFunc) error {
	if err := controlContext(ctx); err != nil {
		return err
	}
	if run == nil {
		return ErrControlDisabled
	}
	taskCtx, err := c.app.StartTask(ctx, id, name)
	if err != nil {
		return err
	}
	var reportMu sync.Mutex
	finished := false
	report := func(progress int, message string) error {
		reportMu.Lock()
		defer reportMu.Unlock()
		if finished {
			return ErrTaskFinished
		}
		if err := taskCtx.Err(); err != nil {
			return err
		}
		return c.app.UpdateTask(id, progress, message)
	}
	if err = taskCtx.Err(); err == nil {
		err = run(taskCtx, report)
	}
	if taskCtx.Err() != nil {
		err = taskCtx.Err()
	}
	reportMu.Lock()
	finished = true
	reportMu.Unlock()
	var displayErr error
	if err != nil {
		displayErr = ErrControlOperation
	}
	completion, finishErr := c.app.FinishTask(id, displayErr)
	if finishErr != nil {
		return finishErr
	}
	if completion == TaskCanceled {
		if errors.Is(err, context.DeadlineExceeded) {
			return context.DeadlineExceeded
		}
		return context.Canceled
	}
	if err != nil {
		return &controlFailure{ErrControlOperation, err}
	}
	return nil
}

// SendToAI sends a proposal request as a cancellable App task. It has no result
// mutation/application path. Task IDs are supplied by the host and must be unique
// within App's retained task history. The callback receives scalar-only data.
func (c *Control) SendToAI(ctx context.Context, taskID string, req AIRequest) error {
	if err := controlContext(ctx); err != nil {
		return err
	}
	if c.cfg.SendToAI == nil {
		return ErrControlDisabled
	}
	if req.ProjectID != c.cfg.ProjectID || req.Timestamp < 0 ||
		(req.ShotID != "" && !validText(string(req.ShotID), maxIDBytes)) ||
		(req.ElementID != "" && (!validText(string(req.ElementID), maxIDBytes) || req.ShotID == "")) ||
		len(req.Note) > maxAINoteBytes || !utf8.ValidString(req.Note) || strings.TrimSpace(req.Note) == "" {
		return ErrInvalidControl
	}
	return c.RunTask(ctx, taskID, "Send to AI", func(ctx context.Context, _ func(int, string) error) error {
		return c.cfg.SendToAI(ctx, req)
	})
}

// Cancel requests cooperative cancellation through App; it never waits for work.
func (c *Control) Cancel(ctx context.Context, taskID string) error {
	if err := controlContext(ctx); err != nil {
		return err
	}
	return c.app.CancelTask(taskID)
}

func controlContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidControl
	}
	return ctx.Err()
}

func nilControlDependency(value any) bool {
	if value == nil {
		return false
	} // optional dependency
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func replaceControlItems(state *tabState, items []Item) {
	selectedID := ""
	if state.selected >= 0 && state.selected < len(state.items) {
		selectedID = state.items[state.selected].ID
	}
	selected := -1
	if len(items) > 0 {
		selected = 0
	}
	for i, item := range items {
		if item.ID == selectedID {
			selected = i
			break
		}
	}
	*state = tabState{items: items, selected: selected}
}

// Preserve errors.Is/As for callers without exposing raw adapter diagnostics in
// Error() (which terminal clients commonly display directly).
type controlFailure struct{ kind, cause error }

func (e *controlFailure) Error() string   { return e.kind.Error() }
func (e *controlFailure) Unwrap() []error { return []error{e.kind, e.cause} }

func (c *Control) failure(code string, kind, cause error) error {
	c.app.AddError(code, kind.Error())
	return &controlFailure{kind, cause}
}
