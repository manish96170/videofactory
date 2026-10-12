package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
	"unsafe"
)

func TestTabsAndNavigation(t *testing.T) {
	app := New()
	if got, want := Tabs(), []Tab{TabChat, TabPlan, TabShots, TabTimeline, TabAssets, TabModels, TabLogs}; !equalTabs(got, want) {
		t.Fatalf("Tabs() = %v, want %v", got, want)
	}
	if got := app.Snapshot(); got.Focused != TabChat || got.Views[0].SelectedIndex != -1 {
		t.Fatalf("initial snapshot = %+v", got)
	}
	if err := app.SetItems(TabChat, []Item{{ID: "one", Label: "One"}, {ID: "two", Label: "Two"}, {ID: "three", Label: "Three"}}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []Key{KeyDown, KeyEnd, KeyUp, KeyHome} {
		if err := app.HandleKey(key); err != nil {
			t.Fatal(err)
		}
	}
	if got := app.Snapshot().Views[0]; got.SelectedID != "one" || got.SelectedIndex != 0 {
		t.Fatalf("home selection = %+v", got)
	}
	if err := app.Select(TabChat, "two"); err != nil {
		t.Fatal(err)
	}
	if err := app.SetItems(TabChat, []Item{{ID: "three", Label: "Three"}, {ID: "two", Label: "Two updated"}}); err != nil {
		t.Fatal(err)
	}
	if got := app.Snapshot().Views[0]; got.SelectedID != "two" || got.SelectedIndex != 1 {
		t.Fatalf("stable selection was not preserved: %+v", got)
	}
	if err := app.HandleKey(KeyTabPrevious); err != nil {
		t.Fatal(err)
	}
	if got := app.Snapshot().Focused; got != TabLogs {
		t.Fatalf("previous tab wrapped to %v, want Logs", got)
	}
	if err := app.HandleKey(KeyTabNext); err != nil {
		t.Fatal(err)
	}
	if got := app.Snapshot().Focused; got != TabChat {
		t.Fatalf("next tab wrapped to %v, want Chat", got)
	}
	if err := app.HandleKey(Key(255)); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("invalid key error = %v", err)
	}
	if err := app.FocusTab(Tab(255)); !errors.Is(err, ErrInvalidTab) {
		t.Fatalf("invalid tab error = %v", err)
	}
}

func TestSnapshotAndInputOwnership(t *testing.T) {
	app := New()
	items := []Item{{ID: "a", Label: "Alpha"}, {ID: "b", Label: "Beta"}}
	if err := app.SetItems(TabShots, items); err != nil {
		t.Fatal(err)
	}
	items[0].Label = "caller mutation"
	if err := app.Select(TabShots, "b"); err != nil {
		t.Fatal(err)
	}
	app.SetStatus("ready", "waiting")
	app.AddError("warning", "first")
	first := app.Snapshot()
	first.Tabs[0] = TabLogs
	first.Views[2].Items[0].Label = "snapshot mutation"
	first.Errors[0].Message = "snapshot mutation"
	first.Views[2].Items = nil
	second := app.Snapshot()
	if second.Tabs[0] != TabChat || second.Views[2].Items[0].Label != "Alpha" || second.Errors[0].Message != "first" {
		t.Fatalf("snapshot mutation escaped ownership: %+v", second)
	}
	if second.Status != (Status{Code: "ready", Text: "waiting"}) {
		t.Fatalf("status = %+v", second.Status)
	}
}

func TestBoundedErrorsAndStatus(t *testing.T) {
	app := New()
	for i := 0; i < maxErrors+3; i++ {
		app.AddError("code", fmt.Sprintf("error-%02d", i))
	}
	app.SetStatus(strings.Repeat("x", maxErrorCode+10), strings.Repeat("界", maxStatusBytes))
	snapshot := app.Snapshot()
	if len(snapshot.Errors) != maxErrors || snapshot.Errors[0].Message != "error-03" || snapshot.Errors[maxErrors-1].Message != "error-18" {
		t.Fatalf("bounded errors = %+v", snapshot.Errors)
	}
	if len(snapshot.Status.Code) > maxErrorCode || len(snapshot.Status.Text) > maxStatusBytes || !utf8.ValidString(snapshot.Status.Text) {
		t.Fatalf("unbounded/invalid status: %+v", snapshot.Status)
	}
	app.AddError(strings.Repeat("c", maxErrorCode+2), strings.Repeat("é", maxErrorText))
	snapshot = app.Snapshot()
	last := snapshot.Errors[len(snapshot.Errors)-1]
	if len(last.Code) > maxErrorCode || len(last.Message) > maxErrorText || !utf8.ValidString(last.Message) {
		t.Fatalf("unbounded/invalid error: %+v", last)
	}
}

func TestCancellableTaskLifecycle(t *testing.T) {
	app := New()
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, err := app.StartTask(parent, "task-1", "render previews")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartTask(parent, "task-1", "duplicate"); !errors.Is(err, ErrTaskExists) {
		t.Fatalf("duplicate task error = %v", err)
	}
	if err := app.UpdateTask("task-1", 35, "working"); err != nil {
		t.Fatal(err)
	}
	if err := app.UpdateTask("task-1", 101, "bad"); !errors.Is(err, ErrInvalidProgress) {
		t.Fatalf("invalid progress error = %v", err)
	}
	if err := app.CancelTask("task-1"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("task context error = %v, want canceled", ctx.Err())
	}
	if got := app.Snapshot().Tasks[0]; got.Status != TaskCancelRequested || got.Progress != 35 {
		t.Fatalf("cancel requested state = %+v", got)
	}
	if status, err := app.FinishTask("task-1", errors.New("ignored after cancellation")); err != nil || status != TaskCanceled {
		t.Fatal(err)
	}
	if got := app.Snapshot().Tasks[0]; got.Status != TaskCanceled {
		t.Fatalf("finished cancellation = %+v", got)
	}
	if err := app.UpdateTask("task-1", 50, "late"); !errors.Is(err, ErrTaskFinished) {
		t.Fatalf("late update error = %v", err)
	}

	_, err = app.StartTask(context.Background(), "task-2", "index")
	if err != nil {
		t.Fatal(err)
	}
	if status, err := app.FinishTask("task-2", nil); err != nil || status != TaskSucceeded {
		t.Fatal(err)
	}
	if got := app.Snapshot().Tasks[1]; got.Status != TaskSucceeded || got.Progress != 100 {
		t.Fatalf("successful task = %+v", got)
	}
	if _, err := app.FinishTask("task-2", nil); !errors.Is(err, ErrTaskFinished) {
		t.Fatalf("second finish error = %v", err)
	}
	cancelParent()
}

func TestParentCancellationAndTaskFailure(t *testing.T) {
	app := New()
	parent, cancel := context.WithCancel(context.Background())
	ctx, err := app.StartTask(parent, "parent-task", "operation")
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatalf("derived context error = %v", ctx.Err())
	}
	if got := app.Snapshot().Tasks[0].Status; got != TaskCancelRequested {
		t.Fatalf("parent cancellation was not projected: %v", got)
	}
	if status, err := app.FinishTask("parent-task", errors.New("late failure")); err != nil || status != TaskCanceled {
		t.Fatal(err)
	}
	if got := app.Snapshot().Tasks[0].Status; got != TaskCanceled {
		t.Fatalf("parent-canceled task status = %v", got)
	}

	if _, err := app.StartTask(context.Background(), "failed", "operation"); err != nil {
		t.Fatal(err)
	}
	if err := app.UpdateTask("failed", 40, strings.Repeat("m", maxTaskMessage*2)); err != nil {
		t.Fatal(err)
	}
	if status, err := app.FinishTask("failed", errors.New(strings.Repeat("e", maxTaskMessage*2))); err != nil || status != TaskFailed {
		t.Fatal(err)
	}
	snapshot := app.Snapshot()
	if got := snapshot.Tasks[1]; got.Status != TaskFailed || got.Message != ErrTaskOperation.Error() || len(got.Message) > maxTaskMessage {
		t.Fatalf("failed task projection = %+v", got)
	}
	if len(snapshot.Errors) != 1 || snapshot.Errors[0].Message != ErrTaskOperation.Error() || len(snapshot.Errors[0].Message) > maxErrorText {
		t.Fatalf("failure error was not bounded: %+v", snapshot.Errors)
	}
}

func TestTaskRetentionIsBounded(t *testing.T) {
	app := New()
	for i := 0; i < maxTasks; i++ {
		id := fmt.Sprintf("task-%02d", i)
		if _, err := app.StartTask(context.Background(), id, "task"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.StartTask(context.Background(), "overflow", "task"); !errors.Is(err, ErrTaskLimit) {
		t.Fatalf("all-running capacity error = %v", err)
	}
	if _, err := app.FinishTask("task-00", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := app.StartTask(context.Background(), "replacement", "task"); err != nil {
		t.Fatalf("terminal task should be evictable: %v", err)
	}
	if got := app.Snapshot().Tasks; len(got) != maxTasks || got[0].ID != "task-01" || got[len(got)-1].ID != "replacement" {
		t.Fatalf("task retention order/limit = %+v", got)
	}
}

func TestFinishTaskOutcomeSurvivesTaskEviction(t *testing.T) {
	app := New()
	if _, err := app.StartTask(context.Background(), "finished", "finished"); err != nil {
		t.Fatal(err)
	}
	status, err := app.FinishTask("finished", nil)
	if err != nil || status != TaskSucceeded {
		t.Fatalf("completion = (%v, %v), want succeeded", status, err)
	}
	for i := 0; i < maxTasks-1; i++ {
		if _, err := app.StartTask(context.Background(), fmt.Sprintf("active-%02d", i), "active"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := app.StartTask(context.Background(), "replacement", "replacement"); err != nil {
		t.Fatal(err)
	}
	for _, task := range app.Snapshot().Tasks {
		if task.ID == "finished" {
			t.Fatal("finished task was not evicted by bounded history")
		}
	}
	if status != TaskSucceeded {
		t.Fatalf("eviction changed completion outcome to %v", status)
	}
}

type reentrantTaskError struct {
	app    *App
	called bool
}

func (e *reentrantTaskError) Error() string {
	e.called = true
	e.app.SetStatus("reentered", "safe")
	return "arbitrary private error"
}

func TestFinishTaskDoesNotFormatArbitraryErrorUnderLockOrRetainIt(t *testing.T) {
	app := New()
	if _, err := app.StartTask(context.Background(), "private", "operation"); err != nil {
		t.Fatal(err)
	}
	cause := &reentrantTaskError{app: app}
	status, err := app.FinishTask("private", cause)
	if err != nil || status != TaskFailed {
		t.Fatalf("completion = (%v, %v)", status, err)
	}
	if cause.called {
		t.Fatal("FinishTask called arbitrary Error() while completing task")
	}
	snapshot := app.Snapshot()
	if snapshot.Tasks[0].Message != ErrTaskOperation.Error() || snapshot.Errors[0].Message != ErrTaskOperation.Error() {
		t.Fatalf("arbitrary diagnostic was retained: %+v", snapshot)
	}
}

func TestTruncateReturnsOwnedBoundedStrings(t *testing.T) {
	for _, input := range []string{strings.Repeat("x", 1<<20), "short", strings.Repeat("\xff", 1<<20)} {
		got := truncate(input, 32)
		if len(got) > 32 || !utf8.ValidString(got) {
			t.Fatalf("truncate returned invalid/big string: length=%d", len(got))
		}
		if len(got) > 0 && unsafe.StringData(got) == unsafe.StringData(input) {
			t.Fatal("truncate result aliases caller's backing storage")
		}
	}
	input := strings.Repeat("界", 20)
	got := truncate(input, 32)
	if len(got) > 32 || !utf8.ValidString(got) || unsafe.StringData(got) == unsafe.StringData(input) {
		t.Fatalf("UTF-8 truncation did not return an owned bounded string: %q", got)
	}
}

func TestConcurrentStateAccess(t *testing.T) {
	app := New()
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				app.SetStatus("working", fmt.Sprintf("worker-%d iteration-%d", worker, i))
				app.AddError("notice", "bounded")
				_ = app.HandleKey(KeyTabNext)
				_ = app.SetItems(TabShots, []Item{{ID: "shot-1", Label: "Shot 1"}, {ID: "shot-2", Label: "Shot 2"}})
				_ = app.Select(TabShots, "shot-2")
				_ = app.Snapshot()
			}
		}(worker)
	}
	wg.Wait()
	snapshot := app.Snapshot()
	if len(snapshot.Errors) != maxErrors || len(snapshot.Views[2].Items) != 2 || snapshot.Views[2].SelectedID != "shot-2" {
		t.Fatalf("unexpected final state: %+v", snapshot)
	}
}

func equalTabs(a, b []Tab) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
