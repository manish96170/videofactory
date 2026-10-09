package render

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestRenderShotBackgroundOnly(t *testing.T) {
	requireFFmpeg(t)

	r := NewFFmpegRenderer(DefaultSettings())
	shot := domain.Shot{
		ID:       "shot-bg",
		Duration: 500 * time.Millisecond,
		Primitives: []domain.Primitive{
			{ID: "bg1", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#224488"}},
		},
	}

	res, err := r.RenderShot(context.Background(), ShotRenderRequest{Shot: shot, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("RenderShot: %v", err)
	}
	if res.MediaPath == "" {
		t.Fatalf("expected a non-empty MediaPath")
	}
	if res.Manifest.ShotID != shot.ID {
		t.Fatalf("manifest ShotID = %q, want %q", res.Manifest.ShotID, shot.ID)
	}
	if len(res.Manifest.Elements) != 1 || res.Manifest.Elements[0].ID != "bg1" {
		t.Fatalf("expected one element with ID bg1, got %+v", res.Manifest.Elements)
	}

	probe, err := Probe(context.Background(), res.MediaPath)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if err := ValidateOutput(probe, r.settings, shot.Duration); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}

	// Verify the actual rendered pixel color, not just file metadata.
	px, err := SamplePixel(context.Background(), res.MediaPath, 100*time.Millisecond, r.settings.Width/2, r.settings.Height/2, r.settings.Width, r.settings.Height)
	if err != nil {
		t.Fatalf("SamplePixel: %v", err)
	}
	want := RGB{R: 0x22, G: 0x44, B: 0x88}
	if !px.CloseTo(want, 8) {
		t.Fatalf("center pixel = %+v, want close to %+v (background color #224488)", px, want)
	}
}

func TestRenderShotText(t *testing.T) {
	requireDrawtext(t)
	settings := DefaultSettings()
	settings.FontPath = requireFontPath(t)

	r := NewFFmpegRenderer(settings)
	shot := domain.Shot{
		ID:       "shot-text",
		Duration: 500 * time.Millisecond,
		Primitives: []domain.Primitive{
			{ID: "bg1", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#000000"}},
			{ID: "txt1", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "Video Factory: it's a test!"}},
		},
	}

	res, err := r.RenderShot(context.Background(), ShotRenderRequest{Shot: shot, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("RenderShot: %v", err)
	}

	probe, err := Probe(context.Background(), res.MediaPath)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if err := ValidateOutput(probe, settings, shot.Duration); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}

	if len(res.Manifest.Elements) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(res.Manifest.Elements))
	}

	// Verify text actually got drawn: sample several pixels across the
	// center row where centered text should appear, and require that
	// at least one differs meaningfully from the black background
	// (exact glyph pixel values depend on font rendering/anti-aliasing,
	// so this checks "something was drawn here," not an exact color).
	foundNonBackground := false
	for x := settings.Width/4 - 1; x < settings.Width*3/4; x += 30 {
		px, err := SamplePixel(context.Background(), res.MediaPath, 100*time.Millisecond, x, settings.Height/2, settings.Width, settings.Height)
		if err != nil {
			t.Fatalf("SamplePixel: %v", err)
		}
		if !px.CloseTo(RGB{}, 20) { // meaningfully different from black
			foundNonBackground = true
			break
		}
	}
	if !foundNonBackground {
		t.Fatalf("expected at least one non-background pixel across the text region; drawtext may not have rendered anything")
	}
}

func TestRenderShotImage(t *testing.T) {
	requireFFmpeg(t)
	fixture := writeFixturePNG(t)

	r := NewFFmpegRenderer(DefaultSettings())
	shot := domain.Shot{
		ID:       "shot-image",
		Duration: 500 * time.Millisecond,
		Primitives: []domain.Primitive{
			{ID: "bg1", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#000000"}},
			{
				ID:   "img1",
				Type: domain.PrimitiveTypeImage,
				Properties: map[string]any{
					"assetPath": fixture,
					"bounds":    domain.Bounds{X: 0.25, Y: 0.25, Width: 0.5, Height: 0.5},
				},
			},
		},
	}

	res, err := r.RenderShot(context.Background(), ShotRenderRequest{Shot: shot, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("RenderShot: %v", err)
	}

	probe, err := Probe(context.Background(), res.MediaPath)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if err := ValidateOutput(probe, r.settings, shot.Duration); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}

	// Inside the image's bounds (center of frame): should be close to
	// the fixture's fill color.
	inside, err := SamplePixel(context.Background(), res.MediaPath, 100*time.Millisecond, r.settings.Width/2, r.settings.Height/2, r.settings.Width, r.settings.Height)
	if err != nil {
		t.Fatalf("SamplePixel(inside): %v", err)
	}
	wantInside := RGB{R: 200, G: 100, B: 50}
	if !inside.CloseTo(wantInside, 10) {
		t.Fatalf("pixel inside image bounds = %+v, want close to %+v (fixture fill color)", inside, wantInside)
	}

	// Outside the image's bounds (corner): should still be the black
	// background, proving the image didn't bleed outside its box.
	outside, err := SamplePixel(context.Background(), res.MediaPath, 100*time.Millisecond, 5, 5, r.settings.Width, r.settings.Height)
	if err != nil {
		t.Fatalf("SamplePixel(outside): %v", err)
	}
	if !outside.CloseTo(RGB{}, 10) {
		t.Fatalf("pixel outside image bounds = %+v, want close to black background", outside)
	}
}

func TestRenderShotTransitionFade(t *testing.T) {
	requireFFmpeg(t)

	r := NewFFmpegRenderer(DefaultSettings())
	shot := domain.Shot{
		ID:       "shot-fade",
		Category: domain.ShotCategoryTransition,
		Duration: 300 * time.Millisecond,
		Primitives: []domain.Primitive{
			// A fade-in multiplies the composed frame's brightness by
			// the fade progress; fading a pure-black frame to pure
			// black is a no-op and wouldn't actually exercise
			// anything, so this shot needs real content to fade.
			{ID: "bg1", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#FFFFFF"}},
			{ID: "fade1", Type: domain.PrimitiveTypeTransition, Properties: map[string]any{"style": "fade-in"}},
		},
	}

	res, err := r.RenderShot(context.Background(), ShotRenderRequest{Shot: shot, OutputDir: t.TempDir()})
	if err != nil {
		t.Fatalf("RenderShot: %v", err)
	}

	probe, err := Probe(context.Background(), res.MediaPath)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if err := ValidateOutput(probe, r.settings, shot.Duration); err != nil {
		t.Fatalf("ValidateOutput: %v", err)
	}

	// A fade-in should start near black and end near full brightness:
	// sample near the start and near the end (but safely before the
	// clip's last frame — a 300ms/30fps clip has frames up to ~267ms,
	// so 290ms would land past the last frame and yield no sample) and
	// confirm the center pixel's brightness increased.
	early, err := SamplePixel(context.Background(), res.MediaPath, 10*time.Millisecond, r.settings.Width/2, r.settings.Height/2, r.settings.Width, r.settings.Height)
	if err != nil {
		t.Fatalf("SamplePixel(early): %v", err)
	}
	late, err := SamplePixel(context.Background(), res.MediaPath, shot.Duration*3/4, r.settings.Width/2, r.settings.Height/2, r.settings.Width, r.settings.Height)
	if err != nil {
		t.Fatalf("SamplePixel(late): %v", err)
	}
	brightness := func(c RGB) int { return int(c.R) + int(c.G) + int(c.B) }
	if brightness(late) <= brightness(early) {
		t.Fatalf("expected a fade-in to brighten over time: early=%+v (brightness %d), late=%+v (brightness %d)", early, brightness(early), late, brightness(late))
	}
}

func TestRenderShotRejectsEmptyShotID(t *testing.T) {
	r := NewFFmpegRenderer(DefaultSettings())
	_, err := r.RenderShot(context.Background(), ShotRenderRequest{
		Shot:      domain.Shot{Duration: time.Second},
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error for empty Shot.ID")
	}
}

func TestRenderShotRejectsNonPositiveDuration(t *testing.T) {
	r := NewFFmpegRenderer(DefaultSettings())
	_, err := r.RenderShot(context.Background(), ShotRenderRequest{
		Shot:      domain.Shot{ID: "shot-1", Duration: 0},
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error for non-positive Duration")
	}
}

func TestRenderShotRespectsCancellation(t *testing.T) {
	r := NewFFmpegRenderer(DefaultSettings())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := r.RenderShot(ctx, ShotRenderRequest{
		Shot:      domain.Shot{ID: "shot-1", Duration: time.Second},
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error for a cancelled context")
	}
}

func TestRenderShotRejectsPathTraversalShotID(t *testing.T) {
	r := NewFFmpegRenderer(DefaultSettings())
	_, err := r.RenderShot(context.Background(), ShotRenderRequest{
		Shot:      domain.Shot{ID: "../../escape", Duration: time.Second},
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error for a path-traversal-shaped shot ID")
	}
}

func TestRenderShotRejectsEmptyPrimitiveID(t *testing.T) {
	r := NewFFmpegRenderer(DefaultSettings())
	_, err := r.RenderShot(context.Background(), ShotRenderRequest{
		Shot: domain.Shot{
			ID:       "shot-1",
			Duration: time.Second,
			Primitives: []domain.Primitive{
				{Type: domain.PrimitiveTypeBackground}, // empty ID
			},
		},
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error for a primitive with an empty ID")
	}
}

func TestRenderShotRejectsDuplicatePrimitiveID(t *testing.T) {
	r := NewFFmpegRenderer(DefaultSettings())
	_, err := r.RenderShot(context.Background(), ShotRenderRequest{
		Shot: domain.Shot{
			ID:       "shot-1",
			Duration: time.Second,
			Primitives: []domain.Primitive{
				{ID: "dup", Type: domain.PrimitiveTypeBackground},
				{ID: "dup", Type: domain.PrimitiveTypeTransition, Properties: map[string]any{"style": "fade-in"}},
			},
		},
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error for duplicate primitive IDs")
	}
}

func TestRenderShotRejectsInvalidSettings(t *testing.T) {
	r := NewFFmpegRenderer(Settings{Width: 0, Height: 1080, FPS: 30})
	_, err := r.RenderShot(context.Background(), ShotRenderRequest{
		Shot:      domain.Shot{ID: "shot-1", Duration: time.Second},
		OutputDir: t.TempDir(),
	})
	if err == nil {
		t.Fatalf("expected error for Settings.Width == 0")
	}
}

// TestFontPathInjectionIsNeutralized is a regression test for a real
// bug found during Phase 03 review: an unescaped/unquoted FontPath
// containing ffmpeg filtergraph syntax characters (';', '[', ']')
// could inject additional filter stages. See DECISIONS.md ADR-016's
// amendment.
func TestFontPathInjectionIsNeutralized(t *testing.T) {
	requireDrawtext(t)
	realFont := requireFontPath(t)

	evilDir := t.TempDir()
	evilPath := evilDir + "/evil;format=yuv420p[v1].ttf"
	data, err := os.ReadFile(realFont)
	if err != nil {
		t.Fatalf("read real font: %v", err)
	}
	if err := os.WriteFile(evilPath, data, 0o644); err != nil {
		t.Fatalf("write evil-named font copy: %v", err)
	}

	settings := DefaultSettings()
	settings.FontPath = evilPath
	r := NewFFmpegRenderer(settings)

	shot := domain.Shot{
		ID:       "shot-text",
		Duration: 300 * time.Millisecond,
		Primitives: []domain.Primitive{
			{ID: "txt1", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "hi"}},
		},
	}

	// Before the fix, this either failed with a filtergraph parse
	// error or (worse) silently produced a mangled filter graph. After
	// the fix, it must render successfully, proving the injected
	// characters were treated as literal path bytes, not filtergraph
	// syntax.
	if _, err := r.RenderShot(context.Background(), ShotRenderRequest{Shot: shot, OutputDir: t.TempDir()}); err != nil {
		t.Fatalf("RenderShot with a filtergraph-special-character font path: %v", err)
	}
}
