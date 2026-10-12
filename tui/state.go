package tui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unicode/utf8"
)

// Tab identifies a named control-plane view.
type Tab uint8

const (
	TabChat Tab = iota
	TabPlan
	TabShots
	TabTimeline
	TabAssets
	TabModels
	TabLogs
)

var tabOrder = [...]Tab{TabChat, TabPlan, TabShots, TabTimeline, TabAssets, TabModels, TabLogs}

// Tabs returns the stable display order of the control-plane tabs.
func Tabs() []Tab { return append([]Tab(nil), tabOrder[:]...) }

func (t Tab) String() string {
	switch t {
	case TabChat:
		return "Chat"
	case TabPlan:
		return "Plan"
	case TabShots:
		return "Shots"
	case TabTimeline:
		return "Timeline"
	case TabAssets:
		return "Assets"
	case TabModels:
		return "Models"
	case TabLogs:
		return "Logs"
	default:
		return ""
	}
}

// Key is a semantic navigation command, independent of any terminal key
// encoding. A future presentation layer can map its own key events to these.
type Key uint8

const (
	KeyTabNext Key = iota + 1
	KeyTabPrevious
	KeyUp
	KeyDown
	KeyHome
	KeyEnd
)

// Item is a stable selection target supplied by a client adapter.
type Item struct {
	ID    string
	Label string
}

// Status is a bounded, presentation-neutral status line.
type Status struct {
	Code string
	Text string
}

// Error is a bounded UI diagnostic. Callers should pass user-safe messages;
// truncation limits memory use but is not a substitute for redaction.
type Error struct {
	Code    string
	Message string
}

type TaskStatus uint8

const (
	TaskRunning TaskStatus = iota + 1
	TaskCancelRequested
	TaskSucceeded
	TaskFailed
	TaskCanceled
)

// Task is a serializable projection of command progress. Contexts and cancel
// functions are intentionally kept private to App.
type Task struct {
	ID       string
	Name     string
	Status   TaskStatus
	Progress int // integer percentage in [0, 100]
	Message  string
}

// TabSnapshot and Snapshot are independent value projections. Every slice is
// copied when produced, so callers may freely mutate a snapshot.
type TabSnapshot struct {
	Tab           Tab
	Items         []Item
	SelectedID    string
	SelectedIndex int // -1 when no item is selected
}

type Snapshot struct {
	Tabs    []Tab
	Focused Tab
	Views   []TabSnapshot
	Status  Status
	Errors  []Error
	Tasks   []Task
}

const (
	maxItemsPerTab = 1000
	maxErrors      = 16
	maxTasks       = 32
	maxIDBytes     = 128
	maxLabelBytes  = 128
	maxStatusBytes = 256
	maxErrorCode   = 64
	maxErrorText   = 256
	maxTaskName    = 96
	maxTaskMessage = 256
)

var (
	ErrInvalidTab      = errors.New("tui: invalid tab")
	ErrInvalidKey      = errors.New("tui: invalid key")
	ErrInvalidItem     = errors.New("tui: invalid item")
	ErrTooManyItems    = errors.New("tui: too many items")
	ErrSelection       = errors.New("tui: selection not found")
	ErrInvalidTask     = errors.New("tui: invalid task")
	ErrTaskExists      = errors.New("tui: task already exists")
	ErrTaskNotFound    = errors.New("tui: task not found")
	ErrTaskLimit       = errors.New("tui: task limit reached")
	ErrInvalidProgress = errors.New("tui: progress must be between 0 and 100")
	ErrTaskFinished    = errors.New("tui: task is not running")
	ErrTaskOperation   = errors.New("tui: task operation failed")
)

type tabState struct {
	items    []Item
	selected int
}

type taskControl struct {
	Task
	ctx    context.Context
	cancel context.CancelFunc
}

// App is a concurrency-safe control-plane state model. Its zero value is
// usable. It performs no I/O and starts no goroutines.
type App struct {
	mu      sync.Mutex
	focused Tab
	tabs    [len(tabOrder)]tabState
	status  Status
	errors  []Error
	tasks   []taskControl
}

func New() *App { return &App{} }

// SetItems replaces a tab's selection targets with an owned copy, preserving
// the selected stable ID when possible.
func (a *App) SetItems(tab Tab, items []Item) error {
	index, ok := tabIndex(tab)
	if !ok {
		return ErrInvalidTab
	}
	if len(items) > maxItemsPerTab {
		return ErrTooManyItems
	}
	owned := make([]Item, len(items))
	seen := make(map[string]struct{}, len(items))
	for i, item := range items {
		if !validText(item.ID, maxIDBytes) || !validText(item.Label, maxLabelBytes) {
			return ErrInvalidItem
		}
		if _, exists := seen[item.ID]; exists {
			return ErrInvalidItem
		}
		seen[item.ID] = struct{}{}
		owned[i] = item
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	previousID := ""
	if current := a.tabs[index]; current.selected >= 0 && current.selected < len(current.items) {
		previousID = current.items[current.selected].ID
	}
	selected := -1
	if len(owned) > 0 {
		selected = 0
		for i := range owned {
			if owned[i].ID == previousID && previousID != "" {
				selected = i
				break
			}
		}
	}
	a.tabs[index] = tabState{items: owned, selected: selected}
	return nil
}

// FocusTab selects the active view without changing any tab's item selection.
func (a *App) FocusTab(tab Tab) error {
	if _, ok := tabIndex(tab); !ok {
		return ErrInvalidTab
	}
	a.mu.Lock()
	a.focused = tab
	a.mu.Unlock()
	return nil
}

// Select focuses an item by stable ID within a tab.
func (a *App) Select(tab Tab, id string) error {
	index, ok := tabIndex(tab)
	if !ok {
		return ErrInvalidTab
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.tabs[index].items {
		if a.tabs[index].items[i].ID == id {
			a.tabs[index].selected = i
			return nil
		}
	}
	return ErrSelection
}

// HandleKey applies a semantic navigation command to the focused tab.
func (a *App) HandleKey(key Key) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch key {
	case KeyTabNext, KeyTabPrevious:
		index, _ := tabIndex(a.focused)
		delta := 1
		if key == KeyTabPrevious {
			delta = -1
		}
		a.focused = tabOrder[(index+delta+len(tabOrder))%len(tabOrder)]
	case KeyUp, KeyDown, KeyHome, KeyEnd:
		index, _ := tabIndex(a.focused)
		state := &a.tabs[index]
		if len(state.items) == 0 {
			return nil
		}
		switch key {
		case KeyUp:
			if state.selected > 0 {
				state.selected--
			}
		case KeyDown:
			if state.selected < len(state.items)-1 {
				state.selected++
			}
		case KeyHome:
			state.selected = 0
		case KeyEnd:
			state.selected = len(state.items) - 1
		}
	default:
		return ErrInvalidKey
	}
	return nil
}

// SetStatus stores the latest bounded status projection. Oversized fields are
// UTF-8-safe truncated; an empty code and text clear the status.
func (a *App) SetStatus(code, text string) {
	a.mu.Lock()
	a.status = Status{Code: truncate(code, maxErrorCode), Text: truncate(text, maxStatusBytes)}
	a.mu.Unlock()
}

// AddError appends a bounded diagnostic, retaining only the newest maxErrors.
func (a *App) AddError(code, message string) {
	entry := Error{Code: truncate(code, maxErrorCode), Message: truncate(message, maxErrorText)}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.errors) == maxErrors {
		copy(a.errors, a.errors[1:])
		a.errors[len(a.errors)-1] = entry
		return
	}
	a.errors = append(a.errors, entry)
}

// StartTask registers a cancellable command and returns its derived context to
// the caller. The caller remains responsible for running and finishing it.
func (a *App) StartTask(parent context.Context, id, name string) (context.Context, error) {
	if parent == nil || !validText(id, maxIDBytes) || !validText(name, maxTaskName) {
		return nil, ErrInvalidTask
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.tasks {
		if a.tasks[i].ID == id {
			return nil, ErrTaskExists
		}
	}
	if len(a.tasks) == maxTasks {
		oldest := -1
		for i := range a.tasks {
			if terminal(a.tasks[i].Status) {
				oldest = i
				break
			}
		}
		if oldest < 0 {
			return nil, ErrTaskLimit
		}
		copy(a.tasks[oldest:], a.tasks[oldest+1:])
		a.tasks = a.tasks[:len(a.tasks)-1]
	}
	ctx, cancel := context.WithCancel(parent)
	a.tasks = append(a.tasks, taskControl{Task: Task{ID: id, Name: name, Status: TaskRunning}, ctx: ctx, cancel: cancel})
	return ctx, nil
}

// UpdateTask advances a running task's bounded progress projection.
func (a *App) UpdateTask(id string, progress int, message string) error {
	if progress < 0 || progress > 100 {
		return ErrInvalidProgress
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	i := a.taskIndex(id)
	if i < 0 {
		return ErrTaskNotFound
	}
	if a.tasks[i].Status != TaskRunning {
		return ErrTaskFinished
	}
	a.tasks[i].Progress = progress
	a.tasks[i].Message = truncate(message, maxTaskMessage)
	return nil
}

// CancelTask requests cancellation without waiting for the operation to stop.
// Cancellation is cooperative; FinishTask records its eventual outcome.
func (a *App) CancelTask(id string) error {
	a.mu.Lock()
	i := a.taskIndex(id)
	if i < 0 {
		a.mu.Unlock()
		return ErrTaskNotFound
	}
	if terminal(a.tasks[i].Status) {
		a.mu.Unlock()
		return ErrTaskFinished
	}
	a.tasks[i].Status = TaskCancelRequested
	cancel := a.tasks[i].cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	return nil
}

// FinishTask marks a command terminal and returns its completion status. A
// cancellation signal takes precedence over a supplied operation error. The
// arbitrary operation error is intentionally not rendered or retained.
func (a *App) FinishTask(id string, operationErr error) (TaskStatus, error) {
	a.mu.Lock()
	i := a.taskIndex(id)
	if i < 0 {
		a.mu.Unlock()
		return 0, ErrTaskNotFound
	}
	task := &a.tasks[i]
	if terminal(task.Status) {
		a.mu.Unlock()
		return 0, ErrTaskFinished
	}
	if task.Status == TaskCancelRequested || (task.ctx != nil && task.ctx.Err() != nil) {
		task.Status = TaskCanceled
	} else if operationErr != nil {
		task.Status = TaskFailed
		task.Message = ErrTaskOperation.Error()
	} else {
		task.Status = TaskSucceeded
		task.Progress = 100
	}
	task.ctx = nil
	cancel := task.cancel
	task.cancel = nil
	result := task.Task
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if result.Status == TaskFailed {
		a.AddError("task_failed", ErrTaskOperation.Error())
	}
	return result.Status, nil
}

// Snapshot returns an immutable-by-ownership copy of current client state.
func (a *App) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	result := Snapshot{
		Tabs:    Tabs(),
		Focused: a.focused,
		Views:   make([]TabSnapshot, len(tabOrder)),
		Status:  a.status,
		Errors:  append([]Error(nil), a.errors...),
		Tasks:   make([]Task, len(a.tasks)),
	}
	for i, tab := range tabOrder {
		state := a.tabs[i]
		view := TabSnapshot{Tab: tab, Items: append([]Item(nil), state.items...), SelectedIndex: -1}
		if state.selected >= 0 && state.selected < len(state.items) {
			view.SelectedIndex = state.selected
			view.SelectedID = state.items[state.selected].ID
		}
		result.Views[i] = view
	}
	for i := range a.tasks {
		task := a.tasks[i].Task
		if task.Status == TaskRunning && a.tasks[i].ctx != nil && a.tasks[i].ctx.Err() != nil {
			task.Status = TaskCancelRequested
		}
		result.Tasks[i] = task
	}
	return result
}

func (a *App) taskIndex(id string) int {
	for i := range a.tasks {
		if a.tasks[i].ID == id {
			return i
		}
	}
	return -1
}

func tabIndex(tab Tab) (int, bool) {
	if int(tab) >= len(tabOrder) {
		return 0, false
	}
	return int(tab), true
}

func terminal(status TaskStatus) bool {
	return status == TaskSucceeded || status == TaskFailed || status == TaskCanceled
}

func validText(value string, maxBytes int) bool {
	return value != "" && utf8.ValidString(value) && len(value) <= maxBytes && strings.TrimSpace(value) == value
}

func truncate(value string, maxBytes int) string {
	if maxBytes <= 0 || value == "" {
		return ""
	}
	var result strings.Builder
	result.Grow(min(maxBytes, len(value)))
	for len(value) > 0 {
		r, size := utf8.DecodeRuneInString(value)
		token := value[:size]
		if r == utf8.RuneError && size == 1 {
			token = "�"
		}
		if len(token) > maxBytes-result.Len() {
			break
		}
		result.WriteString(token)
		value = value[size:]
	}
	return result.String()
}
