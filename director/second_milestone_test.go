package director

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/idgen"
	"github.com/manish96170/videofactory/registry"
	"github.com/manish96170/videofactory/render"
)

// requireFFmpeg skips the test if ffmpeg/ffprobe aren't on PATH,
// mirroring render/testhelpers_test.go's helper (not reusable across
// packages, since it's unexported test-only code) so this integration
// test degrades gracefully on a machine without ffmpeg instead of
// failing (PHASES.md's native-tools architecture: ffmpeg is a system
// dependency, not a Go module dependency).
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found on PATH; skipping")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found on PATH; skipping")
	}
}

// requireDrawtext mirrors render/testhelpers_test.go: only text renders
// need this feature, not tests that reject unsupported primitives.
func requireDrawtext(t *testing.T) {
	t.Helper()
	requireFFmpeg(t)
	out, err := exec.CommandContext(context.Background(), "ffmpeg", "-hide_banner", "-filters").CombinedOutput()
	if err != nil {
		t.Skipf("could not query ffmpeg -filters: %v", err)
	}
	if !bytes.Contains(out, []byte("drawtext")) {
		t.Skip("ffmpeg build does not include the drawtext filter (needs libfreetype); skipping")
	}
}

// requireFontPath uses the renderer's exported default discovery instead
// of duplicating its platform-specific font candidate paths.
func requireFontPath(t *testing.T) string {
	t.Helper()
	path := render.DefaultSettings().FontPath
	if path == "" {
		t.Skip("no default font found on this machine; skipping")
	}
	return path
}

// TestSecondMilestonePromptToRenderedMP4 is PHASES.md section 29's
// Second Milestone, literally: "Prompt -> Director -> VideoPlan ->
// Deterministic renderer -> MP4." It deliberately avoids avatar
// keywords, so FixtureDirector's output is 100% deterministic
// (Background+Text primitives only) and therefore fully renderable by
// Phase 03's render package today — a plan containing a
// DeveloperAvatar shot would correctly fail to render, since no AI
// provider exists yet (Phase 14/15), which is out of scope for this
// milestone.
func TestSecondMilestonePromptToRenderedMP4(t *testing.T) {
	requireDrawtext(t)

	settings := render.DefaultSettings()
	settings.FontPath = requireFontPath(t)
	settings.Width, settings.Height = 320, 240 // small, to keep the test fast

	d := NewFixtureDirector(registry.NewFlowRegistry(), registry.NewFramingRegistry(), registry.NewPrimitiveRegistry())

	projectID := domain.ID(idgen.New())
	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: projectID,
		Prompt:    "First we open the code editor. Then we write a small function. Finally we run it in the terminal.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(res.Plan.Shots) != 3 {
		t.Fatalf("expected 3 shots, got %d", len(res.Plan.Shots))
	}
	for _, shot := range res.Plan.Shots {
		if !shot.Deterministic {
			t.Fatalf("expected an all-deterministic plan for this prompt, got non-deterministic shot %q", shot.ID)
		}
	}

	timeline := render.BuildSequentialTimeline(projectID, res.Plan)
	if err := render.ValidateSequentialTimeline(timeline, res.Plan.Shots); err != nil {
		t.Fatalf("ValidateSequentialTimeline: %v", err)
	}

	renderer := render.NewFFmpegRenderer(settings)
	outDir := t.TempDir()
	shotMedia := make(map[domain.ID]string, len(res.Plan.Shots))

	for _, shot := range res.Plan.Shots {
		shotRes, err := renderer.RenderShot(context.Background(), render.ShotRenderRequest{Shot: shot, OutputDir: outDir})
		if err != nil {
			t.Fatalf("RenderShot(%s): %v", shot.ID, err)
		}
		shotMedia[shot.ID] = shotRes.MediaPath
	}

	finalPath := outDir + "/final.mp4"
	if err := render.ComposeTimeline(context.Background(), timeline, shotMedia, finalPath); err != nil {
		t.Fatalf("ComposeTimeline: %v", err)
	}

	probe, err := render.Probe(context.Background(), finalPath)
	if err != nil {
		t.Fatalf("Probe(final): %v", err)
	}

	var wantDuration time.Duration
	for _, shot := range res.Plan.Shots {
		wantDuration += shot.Duration
	}
	if diff := probe.Duration - wantDuration; diff > 150*time.Millisecond || diff < -150*time.Millisecond {
		t.Fatalf("final duration = %v, want %v (±150ms)", probe.Duration, wantDuration)
	}
	if probe.Width != settings.Width || probe.Height != settings.Height {
		t.Fatalf("final resolution = %dx%d, want %dx%d", probe.Width, probe.Height, settings.Width, settings.Height)
	}
}

// TestSecondMilestoneRejectsNonDeterministicPlanAtRenderTime confirms
// the Director/renderer boundary PHASES.md requires: the Director
// plans an avatar shot (it decides AI *should* be used), but nothing
// in this module can render it yet, and the renderer must fail loudly
// rather than silently skip or fake the content.
func TestSecondMilestoneRejectsNonDeterministicPlanAtRenderTime(t *testing.T) {
	d := NewFixtureDirector(registry.NewFlowRegistry(), registry.NewFramingRegistry(), registry.NewPrimitiveRegistry())
	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "Let me show my avatar explaining this.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.Plan.Shots[0].Deterministic {
		t.Fatalf("expected a non-deterministic avatar shot for this prompt")
	}

	// No tools or fonts are needed: unsupported primitives fail while
	// building the filter graph, before a native process is launched.
	renderer := render.NewFFmpegRenderer(render.Settings{Width: 320, Height: 240, FPS: 30})
	_, err = renderer.RenderShot(context.Background(), render.ShotRenderRequest{Shot: res.Plan.Shots[0], OutputDir: t.TempDir()})
	want := fmt.Sprintf("PrimitiveType %q is not supported by the deterministic renderer", domain.PrimitiveTypeDeveloperAvatar)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("RenderShot error = %v, want unsupported DeveloperAvatar error (%s)", err, want)
	}
}

func TestSecondMilestoneAvatarRejectionNeedsNoNativeTools(t *testing.T) {
	// Unsupported primitives are rejected before any native process is
	// launched, so this boundary must be testable even without FFmpeg.
	t.Setenv("PATH", t.TempDir())
	executed := false
	t.Run("without native tools", func(t *testing.T) {
		TestSecondMilestoneRejectsNonDeterministicPlanAtRenderTime(t)
		executed = true
	})
	if !executed {
		t.Fatal("unsupported-avatar assertion was skipped despite needing no native tools")
	}
}
