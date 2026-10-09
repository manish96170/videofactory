package project

import (
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestAbsoluteStartAndEndCompose(t *testing.T) {
	entry := domain.TimelineEntry{ShotID: "shot-17", Start: 30 * time.Second, End: 32 * time.Second}
	elem := domain.VisualElement{ID: "elem-1", ShotID: "shot-17", Start: 1200 * time.Millisecond, End: 1800 * time.Millisecond}

	start, err := AbsoluteStart(entry, elem)
	if err != nil {
		t.Fatalf("AbsoluteStart: %v", err)
	}
	if want := 31200 * time.Millisecond; start != want {
		t.Fatalf("AbsoluteStart = %v, want %v", start, want)
	}

	end, err := AbsoluteEnd(entry, elem)
	if err != nil {
		t.Fatalf("AbsoluteEnd: %v", err)
	}
	if want := 31800 * time.Millisecond; end != want {
		t.Fatalf("AbsoluteEnd = %v, want %v", end, want)
	}
}

func TestAbsoluteStartRejectsMismatchedShot(t *testing.T) {
	entry := domain.TimelineEntry{ShotID: "shot-17"}
	elem := domain.VisualElement{ID: "elem-1", ShotID: "shot-18"}

	if _, err := AbsoluteStart(entry, elem); err == nil {
		t.Fatalf("expected error for mismatched shot IDs")
	}
}

func TestElementsForShotAndEntryForShot(t *testing.T) {
	timeline := domain.Timeline{
		Entries: []domain.TimelineEntry{
			{ShotID: "shot-1", Start: 0, End: time.Second},
			{ShotID: "shot-2", Start: time.Second, End: 2 * time.Second},
		},
		Elements: []domain.VisualElement{
			{ID: "e1", ShotID: "shot-1", Type: "subtitle"},
			{ID: "e2", ShotID: "shot-2", Type: "subtitle"},
			{ID: "e3", ShotID: "shot-1", Type: "cursor"},
		},
	}

	got := ElementsForShot(timeline, "shot-1")
	if len(got) != 2 || got[0].ID != "e1" || got[1].ID != "e3" {
		t.Fatalf("ElementsForShot(shot-1) = %+v, want [e1 e3]", got)
	}

	entry, ok := EntryForShot(timeline, "shot-2")
	if !ok || entry.Start != time.Second {
		t.Fatalf("EntryForShot(shot-2) = %+v, %v, want start=1s true", entry, ok)
	}

	if _, ok := EntryForShot(timeline, "missing"); ok {
		t.Fatalf("expected EntryForShot(missing) to return ok=false")
	}
}
