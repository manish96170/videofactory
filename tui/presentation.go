package tui

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	maxPresentationBytes = 16 << 10
	maxPresentedTabs     = 16
	maxPresentedTasks    = 8
	maxPresentedErrors   = 8
	maxPresentedViews    = 16
	maxPresentedItems    = 4
	maxQuotedFieldBytes  = 96
)

const presentationTruncated = "\n[output truncated]\n"

// Render returns a deterministic, terminal-safe text projection of snapshot.
// The output has a fixed byte limit, and caller-controlled text is quoted with
// non-ASCII and control characters escaped. Render performs no I/O and does
// not modify snapshot.
func Render(snapshot Snapshot) string {
	w := presentationWriter{limit: maxPresentationBytes - len(presentationTruncated)}
	w.text("Tabs:")
	if len(snapshot.Tabs) == 0 {
		w.text(" (none)")
	} else {
		count := min(len(snapshot.Tabs), maxPresentedTabs)
		for i := 0; i < count && !w.full; i++ {
			w.text(" ")
			w.tab(snapshot.Tabs[i], snapshot.Focused)
		}
		if len(snapshot.Tabs) > count {
			w.text(" ...+")
			w.integer(len(snapshot.Tabs) - count)
		}
	}
	w.text("\nStatus: code=")
	w.quoted(snapshot.Status.Code)
	w.text(" text=")
	w.quoted(snapshot.Status.Text)
	w.text("\n")

	w.text("Tasks:\n")
	if len(snapshot.Tasks) == 0 {
		w.text("  (none)\n")
	} else {
		tasks := presentationTaskWindow(snapshot.Tasks)
		for _, task := range tasks {
			if w.full {
				break
			}
			w.text("  ")
			w.text(taskStatusName(task.Status))
			w.text(" progress=")
			w.integer(task.Progress)
			w.text("% id=")
			w.quoted(task.ID)
			w.text(" name=")
			w.quoted(task.Name)
			w.text(" message=")
			w.quoted(task.Message)
			w.text("\n")
		}
		if len(snapshot.Tasks) > len(tasks) && !w.full {
			w.text("  ...+")
			w.integer(len(snapshot.Tasks) - len(tasks))
			w.text(" more tasks\n")
		}
	}

	w.text("Errors:\n")
	if len(snapshot.Errors) == 0 {
		w.text("  (none)\n")
	} else {
		count := min(len(snapshot.Errors), maxPresentedErrors)
		for i := 0; i < count && !w.full; i++ {
			entry := snapshot.Errors[len(snapshot.Errors)-1-i]
			w.text("  code=")
			w.quoted(entry.Code)
			w.text(" message=")
			w.quoted(entry.Message)
			w.text("\n")
		}
		if len(snapshot.Errors) > count && !w.full {
			w.text("  ...+")
			w.integer(len(snapshot.Errors) - count)
			w.text(" more errors\n")
		}
	}

	w.text("Views:\n")
	if len(snapshot.Views) == 0 {
		w.text("  (none)\n")
	} else {
		count := min(len(snapshot.Views), maxPresentedViews)
		for i := 0; i < count && !w.full; i++ {
			view := snapshot.Views[i]
			w.text("  ")
			w.tabName(view.Tab)
			w.text(" selected=")
			w.quoted(view.SelectedID)
			w.text(" index=")
			w.integer(view.SelectedIndex)
			w.text("\n")
			if len(view.Items) == 0 {
				w.text("    (no items)\n")
			}
			start, end, selected := presentationItemWindow(view)
			if start > 0 && !w.full {
				w.text("    ...")
				w.integer(start)
				w.text(" earlier items\n")
			}
			for j := start; j < end && !w.full; j++ {
				item := view.Items[j]
				if j == selected {
					w.text("    > ")
				} else {
					w.text("      ")
				}
				w.quoted(item.Label)
				w.text(" (id=")
				w.quoted(item.ID)
				w.text(")\n")
			}
			if len(view.Items) > end && !w.full {
				w.text("    ...+")
				w.integer(len(view.Items) - end)
				w.text(" more items\n")
			}
		}
		if len(snapshot.Views) > count && !w.full {
			w.text("  ...+")
			w.integer(len(snapshot.Views) - count)
			w.text(" more views\n")
		}
	}
	if w.full {
		w.builder.WriteString(presentationTruncated)
	}
	return w.builder.String()
}

func presentationItemWindow(view TabSnapshot) (start, end, selected int) {
	selected = view.SelectedIndex
	if selected < 0 || selected >= len(view.Items) {
		selected = -1
		if view.SelectedID != "" {
			for i := range view.Items {
				if view.Items[i].ID == view.SelectedID {
					selected = i
					break
				}
			}
		}
	}
	end = min(len(view.Items), maxPresentedItems)
	if len(view.Items) <= maxPresentedItems || selected < 0 || selected < end {
		return 0, end, selected
	}
	start = selected - maxPresentedItems/2
	if start+maxPresentedItems > len(view.Items) {
		start = len(view.Items) - maxPresentedItems
	}
	end = start + maxPresentedItems
	return start, end, selected
}

// presentationTaskWindow prioritizes the newest active tasks, then fills any
// remaining slots with the newest tasks of any status.
func presentationTaskWindow(tasks []Task) []Task {
	window := make([]Task, 0, min(len(tasks), maxPresentedTasks))
	selected := make(map[int]struct{}, maxPresentedTasks)
	for i := len(tasks) - 1; i >= 0 && len(window) < maxPresentedTasks; i-- {
		if tasks[i].Status == TaskRunning || tasks[i].Status == TaskCancelRequested {
			window = append(window, tasks[i])
			selected[i] = struct{}{}
		}
	}
	for i := len(tasks) - 1; i >= 0 && len(window) < maxPresentedTasks; i-- {
		if _, exists := selected[i]; exists {
			continue
		}
		window = append(window, tasks[i])
	}
	return window
}

// MapTerminalKey maps one complete raw terminal key sequence to an existing
// semantic navigation Key. Unknown or partial sequences return false. It is a
// pure mapping; the caller decides whether to pass the key to App.HandleKey.
func MapTerminalKey(input []byte) (Key, bool) {
	switch {
	case bytes.Equal(input, []byte("\t")):
		return KeyTabNext, true
	case bytes.Equal(input, []byte("\x1b[Z")):
		return KeyTabPrevious, true
	case bytes.Equal(input, []byte("\x1b[A")):
		return KeyUp, true
	case bytes.Equal(input, []byte("\x1b[B")):
		return KeyDown, true
	case bytes.Equal(input, []byte("\x1b[H")), bytes.Equal(input, []byte("\x1b[1~")), bytes.Equal(input, []byte("\x1b[7~")):
		return KeyHome, true
	case bytes.Equal(input, []byte("\x1b[F")), bytes.Equal(input, []byte("\x1b[4~")), bytes.Equal(input, []byte("\x1b[8~")):
		return KeyEnd, true
	default:
		return 0, false
	}
}

type presentationWriter struct {
	builder strings.Builder
	limit   int
	full    bool
}

func (w *presentationWriter) text(value string) {
	if w.full {
		return
	}
	remaining := w.limit - w.builder.Len()
	if len(value) > remaining {
		if remaining > 0 {
			w.builder.WriteString(value[:remaining])
		}
		w.full = true
		return
	}
	w.builder.WriteString(value)
}

func (w *presentationWriter) integer(value int) { w.text(strconv.Itoa(value)) }

func (w *presentationWriter) tab(tab, focused Tab) {
	if tab == focused {
		w.text("[")
		w.tabName(tab)
		w.text("]")
		return
	}
	w.tabName(tab)
}

func (w *presentationWriter) tabName(tab Tab) {
	if name := tab.String(); name != "" {
		w.text(name)
		return
	}
	w.text("Unknown(")
	w.integer(int(tab))
	w.text(")")
}

func (w *presentationWriter) quoted(value string) {
	w.text("\"")
	fieldStart := w.builder.Len()
	clipped := false
	for len(value) > 0 && !w.full {
		r, size := utf8.DecodeRuneInString(value)
		if size == 0 {
			break
		}
		value = value[size:]
		token := presentationQuoteRune(r)
		if w.builder.Len()-fieldStart+len(token) > maxQuotedFieldBytes-3 {
			clipped = true
			break
		}
		w.text(token)
	}
	if clipped && !w.full {
		w.text("...")
	}
	w.text("\"")
}

func presentationQuoteRune(r rune) string {
	switch r {
	case '"':
		return `\"`
	case '\\':
		return `\\`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\t':
		return `\t`
	case '\b':
		return `\b`
	case '\f':
		return `\f`
	}
	if r >= 0x20 && r <= 0x7e {
		return string(r)
	}

	const digits = "0123456789abcdef"
	var escaped [10]byte
	width := 4
	escaped[0], escaped[1] = '\\', 'u'
	if r <= 0xffff {
		for i := 0; i < width; i++ {
			escaped[2+i] = digits[(r>>(12-4*i))&0xf]
		}
		return string(escaped[:6])
	}
	width = 8
	escaped[1] = 'U'
	for i := 0; i < width; i++ {
		escaped[2+i] = digits[(r>>(28-4*i))&0xf]
	}
	return string(escaped[:10])
}

func taskStatusName(status TaskStatus) string {
	switch status {
	case TaskRunning:
		return "running"
	case TaskCancelRequested:
		return "cancel_requested"
	case TaskSucceeded:
		return "succeeded"
	case TaskFailed:
		return "failed"
	case TaskCanceled:
		return "canceled"
	default:
		return "unknown"
	}
}
