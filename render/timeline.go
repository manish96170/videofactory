// Timeline composition: placing a VideoPlan's shots sequentially onto a
// Timeline, and concatenating their rendered clips into one final
// video (PHASES.md Phase 03 "shot composition" / "timeline
// composition").
package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/idgen"
	"github.com/manish96170/videofactory/project"
)

// BuildSequentialTimeline places plan's Shots back-to-back in Index
// order, starting at 0, with no gaps or overlaps: shot N's
// TimelineEntry.Start equals the sum of every earlier shot's Duration.
// This is a simple, deterministic default placement strategy — not the
// only possible one. Future phases (Director editing, cross-fades with
// deliberate overlap, ...) may place shots differently; this helper
// exists for the common case of "just play the shots in order," which
// is what Phase 03's milestone needs.
func BuildSequentialTimeline(projectID domain.ID, plan domain.VideoPlan) domain.Timeline {
	shots := append([]domain.Shot(nil), plan.Shots...)
	sort.Slice(shots, func(i, j int) bool { return shots[i].Index < shots[j].Index })

	entries := make([]domain.TimelineEntry, 0, len(shots))
	var offset time.Duration
	for _, s := range shots {
		entries = append(entries, domain.TimelineEntry{
			ShotID: s.ID,
			Start:  offset,
			End:    offset + s.Duration,
		})
		offset += s.Duration
	}

	return domain.Timeline{
		ID:        domain.ID(idgen.New()),
		ProjectID: projectID,
		Entries:   entries,
	}
}

// ComposeTimeline concatenates the already-rendered clips named in
// shotMedia, in timeline.Entries order (sorted by Start), into one
// final video at outputPath, using ffmpeg's concat demuxer with a
// stream copy (no re-encoding, since every clip shares the same
// Settings this package rendered them with).
//
// Phase 03 requires entries to be contiguous (entry[i].End ==
// entry[i+1].Start, and the first entry's Start == 0): gap-filling
// (e.g. inserting black filler for a deliberate gap) is deferred to a
// later phase. This is a documented limitation, not a silent one — a
// non-contiguous timeline returns a clear error.
func ComposeTimeline(ctx context.Context, timeline domain.Timeline, shotMedia map[domain.ID]string, outputPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(timeline.Entries) == 0 {
		return fmt.Errorf("render: timeline has no entries to compose")
	}

	entries := append([]domain.TimelineEntry(nil), timeline.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Start < entries[j].Start })

	for i, e := range entries {
		if e.End <= e.Start {
			return fmt.Errorf("render: timeline entry %d for shot %q has a non-positive interval [%v,%v)", i, e.ShotID, e.Start, e.End)
		}
	}

	if entries[0].Start != 0 {
		return fmt.Errorf("render: timeline composition requires the first entry to start at 0, got %v", entries[0].Start)
	}
	for i := 1; i < len(entries); i++ {
		if entries[i].Start != entries[i-1].End {
			return fmt.Errorf("render: timeline composition requires contiguous entries (gap-filling not yet supported): entry %d ends at %v but entry %d starts at %v",
				i-1, entries[i-1].End, i, entries[i].Start)
		}
	}

	listPath, cleanup, err := writeConcatList(entries, shotMedia)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return fmt.Errorf("render: create output directory: %w", err)
	}

	if _, err := runCommand(ctx, ffmpegBinary,
		"-y",
		"-f", "concat",
		"-safe", "0",
		"-i", listPath,
		"-c", "copy",
		outputPath,
	); err != nil {
		return err
	}

	return nil
}

// writeConcatList writes an ffmpeg concat-demuxer list file for
// entries (which must already be in the desired output order) and
// returns its path plus a cleanup function that removes it.
func writeConcatList(entries []domain.TimelineEntry, shotMedia map[domain.ID]string) (string, func(), error) {
	tmp, err := os.CreateTemp("", "videofactory-concat-*.txt")
	if err != nil {
		return "", nil, fmt.Errorf("render: create concat list: %w", err)
	}
	cleanup := func() { os.Remove(tmp.Name()) }

	for _, entry := range entries {
		path, ok := shotMedia[entry.ShotID]
		if !ok {
			cleanup()
			return "", nil, fmt.Errorf("render: no rendered media for shot %q", entry.ShotID)
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("render: resolve path for shot %q: %w", entry.ShotID, err)
		}
		// The concat demuxer's list format quotes paths with single
		// quotes and escapes embedded single quotes as '\''.
		line := fmt.Sprintf("file '%s'\n", escapeConcatPath(abs))
		if _, err := tmp.WriteString(line); err != nil {
			tmp.Close()
			cleanup()
			return "", nil, fmt.Errorf("render: write concat list: %w", err)
		}
	}

	if err := tmp.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("render: close concat list: %w", err)
	}

	return tmp.Name(), cleanup, nil
}

func escapeConcatPath(p string) string {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
			continue
		}
		out = append(out, p[i])
	}
	return string(out)
}

// ValidateSequentialTimeline is a convenience wrapper around
// project.ValidateTimeline for timelines built by
// BuildSequentialTimeline, reusing Phase 01's structural checks
// (unknown-shot references, interval well-formedness, placed-duration-
// matches-shot-duration, ...) rather than duplicating them here.
func ValidateSequentialTimeline(timeline domain.Timeline, shots []domain.Shot) error {
	return project.ValidateTimeline(timeline, shots)
}
