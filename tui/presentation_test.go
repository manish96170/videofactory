package tui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestRenderIsDeterministicEscapedAndComplete(t *testing.T) {
	snapshot := Snapshot{
		Tabs:    Tabs(),
		Focused: TabShots,
		Views: []TabSnapshot{{
			Tab:           TabShots,
			SelectedID:    "shot\x1b[31m",
			SelectedIndex: 0,
			Items: []Item{
				{ID: "shot\x1b[31m", Label: "line\nbreak \u202e"},
				{ID: "next", Label: "Next shot"},
			},
		}},
		Tasks: []Task{{
			ID: "task-1", Name: "render", Status: TaskRunning, Progress: 42, Message: "working\tplease wait",
		}},
		Errors: []Error{{Code: "warning", Message: "safe\rmessage"}},
		Status: Status{Code: "ready", Text: "agent idle"},
	}
	beforeTabs := append([]Tab(nil), snapshot.Tabs...)
	beforeItems := append([]Item(nil), snapshot.Views[0].Items...)

	got := Render(snapshot)
	if got != Render(snapshot) {
		t.Fatal("Render returned different output for the same snapshot")
	}
	for _, want := range []string{
		"Tabs: Chat Plan [Shots] Timeline Assets Models Logs",
		"Views:\n  Shots selected=",
		`> "line\nbreak \u202e" (id="shot\u001b[31m")`,
		`running progress=42% id="task-1" name="render" message="working\tplease wait"`,
		`code="warning" message="safe\rmessage"`,
		`Status: code="ready" text="agent idle"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render output missing %q:\n%s", want, got)
		}
	}
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\u202e') || !utf8.ValidString(got) {
		t.Fatalf("output contains unescaped terminal/control data: %q", got)
	}
	if len(snapshot.Tabs) != len(beforeTabs) || snapshot.Tabs[0] != beforeTabs[0] || snapshot.Views[0].Items[0] != beforeItems[0] {
		t.Fatal("Render modified the snapshot")
	}
}

func TestRenderBoundsOversizedSnapshot(t *testing.T) {
	snapshot := Snapshot{Tabs: Tabs(), Status: Status{Code: strings.Repeat("s", 300), Text: strings.Repeat("t", 300)}}
	for viewIndex := 0; viewIndex < maxPresentedViews; viewIndex++ {
		view := TabSnapshot{Tab: TabChat, SelectedID: strings.Repeat("i", 300), SelectedIndex: 0}
		for itemIndex := 0; itemIndex < maxPresentedItems; itemIndex++ {
			view.Items = append(view.Items, Item{ID: strings.Repeat("i", 300), Label: strings.Repeat("x", 300)})
		}
		snapshot.Views = append(snapshot.Views, view)
	}
	for i := 0; i < maxPresentedTasks; i++ {
		snapshot.Tasks = append(snapshot.Tasks, Task{ID: strings.Repeat("i", 300), Name: strings.Repeat("n", 300), Message: strings.Repeat("m", 300)})
	}
	for i := 0; i < maxPresentedErrors; i++ {
		snapshot.Errors = append(snapshot.Errors, Error{Code: strings.Repeat("c", 300), Message: strings.Repeat("e", 300)})
	}
	got := Render(snapshot)
	if len(got) > maxPresentationBytes {
		t.Fatalf("rendered %d bytes, limit is %d", len(got), maxPresentationBytes)
	}
	if !strings.HasSuffix(got, presentationTruncated) {
		t.Fatalf("oversized output lacks truncation marker: suffix %q", got[max(0, len(got)-40):])
	}
	if !utf8.ValidString(got) {
		t.Fatal("bounded output is not valid UTF-8")
	}
}

func TestRenderWindowsAroundSelectionAndPrioritizesRecentActivity(t *testing.T) {
	view := TabSnapshot{Tab: TabShots, SelectedIndex: 9, SelectedID: "item-9"}
	for i := 0; i < 12; i++ {
		view.Items = append(view.Items, Item{ID: fmt.Sprintf("item-%02d", i), Label: fmt.Sprintf("Item %02d", i)})
	}
	snapshot := Snapshot{Tabs: Tabs(), Focused: TabShots, Views: []TabSnapshot{view}}
	for i := 0; i < 12; i++ {
		status := TaskSucceeded
		if i == 1 || i == 11 {
			status = TaskRunning
		}
		snapshot.Tasks = append(snapshot.Tasks, Task{ID: fmt.Sprintf("task-%02d", i), Name: "task", Status: status})
		snapshot.Errors = append(snapshot.Errors, Error{Code: "error", Message: fmt.Sprintf("error-%02d", i)})
	}

	got := Render(snapshot)
	for _, want := range []string{
		`> "Item 09" (id="item-09")`, "...7 earlier items", "...+1 more items",
		`running progress=0% id="task-11"`, `running progress=0% id="task-01"`,
		`message="error-11"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Render output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "item-00") || strings.Contains(got, "task-00") || strings.Contains(got, "error-00") {
		t.Fatalf("presentation included oldest entries instead of selected/recent window:\n%s", got)
	}
	if strings.Index(got, `id="task-11"`) > strings.Index(got, `id="task-01"`) {
		t.Fatal("newest active task was not presented before older active task")
	}
	if strings.Index(got, `message="error-11"`) > strings.Index(got, `message="error-04"`) {
		t.Fatal("newest errors were not presented newest-first")
	}
}

func TestRenderEmptyAndUnknownValues(t *testing.T) {
	got := Render(Snapshot{Focused: Tab(99), Tabs: []Tab{Tab(99)}, Views: []TabSnapshot{{Tab: Tab(99)}}})
	for _, want := range []string{"[Unknown(99)]", "(no items)", "Tasks:\n  (none)", "Errors:\n  (none)", `Status: code="" text=""`} {
		if !strings.Contains(got, want) {
			t.Errorf("Render output missing %q:\n%s", want, got)
		}
	}
}

func TestMapTerminalKey(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Key
	}{
		{"tab next", "\t", KeyTabNext},
		{"shift tab previous", "\x1b[Z", KeyTabPrevious},
		{"up", "\x1b[A", KeyUp},
		{"down", "\x1b[B", KeyDown},
		{"home CSI", "\x1b[H", KeyHome},
		{"home tilde", "\x1b[1~", KeyHome},
		{"home alternate", "\x1b[7~", KeyHome},
		{"end CSI", "\x1b[F", KeyEnd},
		{"end tilde", "\x1b[4~", KeyEnd},
		{"end alternate", "\x1b[8~", KeyEnd},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := MapTerminalKey([]byte(test.input))
			if !ok || got != test.want {
				t.Fatalf("MapTerminalKey(%q) = (%v, %v), want (%v, true)", test.input, got, ok, test.want)
			}
		})
	}
	for _, input := range [][]byte{nil, {}, []byte("a"), []byte("\r"), []byte("\x1b"), []byte("\x1b[C"), []byte("\x1b[99~")} {
		if key, ok := MapTerminalKey(input); ok {
			t.Errorf("MapTerminalKey(%q) = (%v, true), want unmapped", input, key)
		}
	}
}

func TestMappedTerminalKeyUsesExistingNavigationAction(t *testing.T) {
	app := New()
	key, ok := MapTerminalKey([]byte("\x1b[B"))
	if !ok {
		t.Fatal("down arrow was not mapped")
	}
	if err := app.SetItems(TabChat, []Item{{ID: "one", Label: "One"}, {ID: "two", Label: "Two"}}); err != nil {
		t.Fatal(err)
	}
	if err := app.HandleKey(key); err != nil {
		t.Fatal(err)
	}
	if got := app.Snapshot().Views[TabChat].SelectedID; got != "two" {
		t.Fatalf("mapped key selected %q, want two", got)
	}
}

func TestPresentationConcurrentReads(t *testing.T) {
	snapshot := Snapshot{
		Tabs: Tabs(), Focused: TabLogs,
		Views: []TabSnapshot{{Tab: TabLogs, Items: []Item{{ID: "event-1", Label: "render complete"}}}},
		Tasks: []Task{{ID: "task-1", Name: "render", Status: TaskRunning, Progress: 10}},
	}
	want := Render(snapshot)
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 100; j++ {
				if got := Render(snapshot); got != want {
					t.Errorf("concurrent Render changed output")
					return
				}
				if key, ok := MapTerminalKey([]byte("\x1b[A")); !ok || key != KeyUp {
					t.Errorf("concurrent key mapping = (%v, %v)", key, ok)
					return
				}
			}
		}()
	}
	workers.Wait()
}
