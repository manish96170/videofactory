package project

import (
	"fmt"
	"time"

	"github.com/manish96170/videofactory/domain"
)

// AbsoluteStart resolves a VisualElement's shot-relative Start into a
// project-timeline-relative duration, by adding the matching
// TimelineEntry's Start (PHASES.md section 4.6.1; DECISIONS.md ADR-007).
//
// It returns an error if elem.ShotID does not match entry.ShotID, so
// callers cannot accidentally combine an element with the wrong shot's
// timeline placement.
func AbsoluteStart(entry domain.TimelineEntry, elem domain.VisualElement) (time.Duration, error) {
	if entry.ShotID != elem.ShotID {
		return 0, fmt.Errorf("project: timeline entry is for shot %q but element %q belongs to shot %q", entry.ShotID, elem.ID, elem.ShotID)
	}
	return entry.Start + elem.Start, nil
}

// AbsoluteEnd is the End-side counterpart to AbsoluteStart.
func AbsoluteEnd(entry domain.TimelineEntry, elem domain.VisualElement) (time.Duration, error) {
	if entry.ShotID != elem.ShotID {
		return 0, fmt.Errorf("project: timeline entry is for shot %q but element %q belongs to shot %q", entry.ShotID, elem.ID, elem.ShotID)
	}
	return entry.Start + elem.End, nil
}

// ElementsForShot returns the VisualElements in timeline that belong to
// shotID, in their original order. This is the "Element-to-shot/
// timeline relationship" lookup PHASES.md Phase 01 asks for: a preview
// surface resolving "what's at this timestamp" starts from a shot, then
// narrows to its elements, rather than guessing from pixels
// (PHASES.md section 4.6.1/4.6.2).
func ElementsForShot(timeline domain.Timeline, shotID domain.ID) []domain.VisualElement {
	var out []domain.VisualElement
	for _, elem := range timeline.Elements {
		if elem.ShotID == shotID {
			out = append(out, elem)
		}
	}
	return out
}

// EntryForShot returns the TimelineEntry placing shotID on the project
// timeline, and whether one was found.
func EntryForShot(timeline domain.Timeline, shotID domain.ID) (domain.TimelineEntry, bool) {
	for _, entry := range timeline.Entries {
		if entry.ShotID == shotID {
			return entry, true
		}
	}
	return domain.TimelineEntry{}, false
}
