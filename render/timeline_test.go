package render

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestBuildSequentialTimelinePlacesShotsBackToBack(t *testing.T) {
	plan := domain.VideoPlan{
		ID:        "plan-1",
		ProjectID: "proj-1",
		Shots: []domain.Shot{
			{ID: "shot-b", Index: 1, Duration: 3 * time.Second},
			{ID: "shot-a", Index: 0, Duration: 2 * time.Second}, // deliberately out of slice order
		},
	}

	timeline := BuildSequentialTimeline("proj-1", plan)

	if timeline.ProjectID != "proj-1" {
		t.Fatalf("ProjectID = %q", timeline.ProjectID)
	}
	if len(timeline.Entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(timeline.Entries))
	}
	// Index order (shot-a=0 then shot-b=1), not slice order.
	if timeline.Entries[0].ShotID != "shot-a" || timeline.Entries[0].Start != 0 || timeline.Entries[0].End != 2*time.Second {
		t.Fatalf("entry 0 = %+v", timeline.Entries[0])
	}
	if timeline.Entries[1].ShotID != "shot-b" || timeline.Entries[1].Start != 2*time.Second || timeline.Entries[1].End != 5*time.Second {
		t.Fatalf("entry 1 = %+v", timeline.Entries[1])
	}
}

func TestBuildSequentialTimelineValidatesAgainstProjectPackage(t *testing.T) {
	plan := domain.VideoPlan{
		ID:        "plan-1",
		ProjectID: "proj-1",
		Shots: []domain.Shot{
			{ID: "shot-a", Index: 0, Duration: 2 * time.Second},
			{ID: "shot-b", Index: 1, Duration: 3 * time.Second},
		},
	}
	timeline := BuildSequentialTimeline("proj-1", plan)
	if err := ValidateSequentialTimeline(timeline, plan.Shots); err != nil {
		t.Fatalf("ValidateSequentialTimeline: %v", err)
	}
}

func TestComposeTimelineRejectsNonContiguousEntries(t *testing.T) {
	timeline := domain.Timeline{
		Entries: []domain.TimelineEntry{
			{ShotID: "a", Start: 0, End: time.Second},
			{ShotID: "b", Start: 2 * time.Second, End: 3 * time.Second}, // gap
		},
	}
	err := ComposeTimeline(context.Background(), timeline, map[domain.ID]string{"a": "x.mp4", "b": "y.mp4"}, "/tmp/out.mp4")
	if err == nil {
		t.Fatalf("expected error for a non-contiguous timeline")
	}
}

func TestComposeTimelineRejectsNonZeroFirstStart(t *testing.T) {
	timeline := domain.Timeline{
		Entries: []domain.TimelineEntry{
			{ShotID: "a", Start: time.Second, End: 2 * time.Second},
		},
	}
	err := ComposeTimeline(context.Background(), timeline, map[domain.ID]string{"a": "x.mp4"}, "/tmp/out.mp4")
	if err == nil {
		t.Fatalf("expected error when the first entry doesn't start at 0")
	}
}

func TestComposeTimelineRejectsEmptyTimeline(t *testing.T) {
	err := ComposeTimeline(context.Background(), domain.Timeline{}, map[domain.ID]string{}, "/tmp/out.mp4")
	if err == nil {
		t.Fatalf("expected error for an empty timeline")
	}
}

func TestComposeTimelineRejectsMissingShotMedia(t *testing.T) {
	timeline := domain.Timeline{
		Entries: []domain.TimelineEntry{{ShotID: "a", Start: 0, End: time.Second}},
	}
	err := ComposeTimeline(context.Background(), timeline, map[domain.ID]string{}, "/tmp/out.mp4")
	if err == nil {
		t.Fatalf("expected error when a shot has no rendered media")
	}
}

func TestComposeTimelineRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	timeline := domain.Timeline{Entries: []domain.TimelineEntry{{ShotID: "a", Start: 0, End: time.Second}}}
	err := ComposeTimeline(ctx, timeline, map[domain.ID]string{"a": "x.mp4"}, "/tmp/out.mp4")
	if err == nil {
		t.Fatalf("expected error for a cancelled context")
	}
}

// TestEndToEndThreeShotMilestone is the Phase 03 milestone test
// (PHASES.md section 29, First Milestone): a manual VideoPlan with 3
// deterministic shots, rendered and composed into one playable MP4,
// without any AI model. It deliberately exercises Background, Text,
// Image, and Transition primitives together.
func TestEndToEndThreeShotMilestone(t *testing.T) {
	requireDrawtext(t)
	settings := DefaultSettings()
	settings.FontPath = requireFontPath(t)
	settings.Width, settings.Height = 320, 240 // small, to keep the test fast

	fixture := writeFixturePNG(t)

	plan := domain.VideoPlan{
		ID:        "plan-1",
		ProjectID: "proj-1",
		FlowID:    "flow-demo",
		FramingID: "framing-fullscreen",
		CreatedAt: time.Now(),
		Shots: []domain.Shot{
			{
				ID:       "shot-title",
				Index:    0,
				Category: domain.ShotCategoryUI,
				Duration: 400 * time.Millisecond,
				Primitives: []domain.Primitive{
					{ID: "title-bg", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#101020"}},
					{ID: "title-text", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "Video Factory", "color": "#FFFFFF"}},
				},
			},
			{
				ID:       "shot-image",
				Index:    1,
				Category: domain.ShotCategoryUI,
				Duration: 400 * time.Millisecond,
				Primitives: []domain.Primitive{
					{ID: "image-bg", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#000000"}},
					{ID: "image-1", Type: domain.PrimitiveTypeImage, Properties: map[string]any{"assetPath": fixture}},
				},
			},
			{
				ID:       "shot-fade",
				Index:    2,
				Category: domain.ShotCategoryTransition,
				Duration: 200 * time.Millisecond,
				Primitives: []domain.Primitive{
					{ID: "fade-out", Type: domain.PrimitiveTypeTransition, Properties: map[string]any{"style": "fade-out"}},
				},
			},
		},
	}

	timeline := BuildSequentialTimeline(plan.ProjectID, plan)
	if err := ValidateSequentialTimeline(timeline, plan.Shots); err != nil {
		t.Fatalf("ValidateSequentialTimeline: %v", err)
	}

	renderer := NewFFmpegRenderer(settings)
	outDir := t.TempDir()
	shotMedia := make(map[domain.ID]string, len(plan.Shots))

	for _, shot := range plan.Shots {
		res, err := renderer.RenderShot(context.Background(), ShotRenderRequest{Shot: shot, OutputDir: outDir})
		if err != nil {
			t.Fatalf("RenderShot(%s): %v", shot.ID, err)
		}
		shotMedia[shot.ID] = res.MediaPath

		if _, err := UnmarshalManifest(mustReadFile(t, outDir+"/"+string(shot.ID)+".render.json")); err != nil {
			t.Fatalf("UnmarshalManifest(%s): %v", shot.ID, err)
		}
	}

	finalPath := outDir + "/final.mp4"
	if err := ComposeTimeline(context.Background(), timeline, shotMedia, finalPath); err != nil {
		t.Fatalf("ComposeTimeline: %v", err)
	}

	probe, err := Probe(context.Background(), finalPath)
	if err != nil {
		t.Fatalf("Probe(final): %v", err)
	}

	wantDuration := 400*time.Millisecond + 400*time.Millisecond + 200*time.Millisecond
	if diff := probe.Duration - wantDuration; diff > durationTolerance || diff < -durationTolerance {
		t.Fatalf("final duration = %v, want %v (±%v)", probe.Duration, wantDuration, durationTolerance)
	}
	if probe.Width != settings.Width || probe.Height != settings.Height {
		t.Fatalf("final resolution = %dx%d, want %dx%d", probe.Width, probe.Height, settings.Width, settings.Height)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
