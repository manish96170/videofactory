// Package review validates and persists timestamped feedback against authoritative
// project state. Feedback is an observation, never a project mutation.
package review

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
)

// Validate checks feedback against the supplied authoritative project snapshot.
// The project must have a valid plan and timeline. Shot targets must be placed;
// element targets must belong to that shot (or resolve globally if ShotID is
// omitted). Points use [start,end); a positive optional duration must fit wholly
// inside the targeted element, shot, or project extent. Project-only feedback
// may address gaps in [0,last timeline end). No wall clock is consulted.
// Callers must not mutate the project or feedback during this call.
func Validate(p domain.Project, f domain.ReviewFeedback) error {
	if validateFields(f) != nil || f.ProjectID != p.ID || p.Plan == nil || p.Timeline == nil || project.Validate(p) != nil {
		return ErrInvalid
	}
	var start, end time.Duration
	for _, entry := range p.Timeline.Entries {
		if entry.End > end {
			end = entry.End
		}
	}
	if f.ShotID != "" {
		entry, ok := project.EntryForShot(*p.Timeline, f.ShotID)
		if !ok {
			return ErrInvalid
		}
		end = entry.End - entry.Start
	}
	if f.ElementID != "" {
		found := false
		for _, element := range p.Timeline.Elements {
			if element.ID != f.ElementID {
				continue
			}
			if f.ShotID != "" && element.ShotID != f.ShotID {
				return ErrInvalid
			}
			start, end = element.Start, element.End
			if f.ShotID == "" {
				entry, ok := project.EntryForShot(*p.Timeline, element.ShotID)
				if !ok {
					return ErrInvalid
				}
				// Validated elements fit in the entry, so additions cannot overflow.
				start, _ = project.AbsoluteStart(entry, element)
				end, _ = project.AbsoluteEnd(entry, element)
			}
			found = true
			break
		}
		if !found {
			return ErrInvalid
		}
	}
	if f.Timestamp < start || f.Timestamp >= end || f.Duration != nil && *f.Duration > end-f.Timestamp {
		return ErrInvalid
	}
	return nil
}

func validateFields(f domain.ReviewFeedback) error {
	if !handle(f.ID) || !handle(f.ProjectID) || f.ShotID != "" && !handle(f.ShotID) || f.ElementID != "" && !handle(f.ElementID) || f.CreatedAt.IsZero() || f.CreatedAt.Year() < 1 || f.CreatedAt.Year() > 9999 || f.Timestamp < 0 || f.Duration != nil && *f.Duration <= 0 || !utf8.ValidString(f.Note) || strings.ContainsRune(f.Note, 0) {
		return ErrInvalid
	}
	switch f.Type {
	case domain.FeedbackTypeApproval:
	case domain.FeedbackTypeComment, domain.FeedbackTypeChangeRequest:
		if strings.TrimSpace(f.Note) == "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func handle(id domain.ID) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func clone(f domain.ReviewFeedback) domain.ReviewFeedback {
	if f.Duration != nil {
		d := *f.Duration
		f.Duration = &d
	}
	// Strip monotonic/process-local information and canonicalize the location.
	f.CreatedAt = f.CreatedAt.UTC().Round(0)
	return f
}
