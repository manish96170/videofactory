// Package render implements the deterministic renderer
// (PHASES.md "PHASE 03 — DETERMINISTIC RENDERER"): it turns a Shot's
// Primitives into a real video file, and a Timeline's placed Shots into
// one final video, using no AI model.
//
// # FFmpeg integration strategy
//
// Core logic stays in Go: this package builds a deterministic FFmpeg
// filter graph and command line from domain types, then shells out to
// the external `ffmpeg`/`ffprobe` binaries via os/exec (not cgo/libav
// bindings), per PHASES.md section 2.1 ("Native tools: Use native
// executables where appropriate: FFmpeg, FFprobe"). This keeps the Go
// module dependency-free for rendering, avoids the build complexity and
// portability risk of linking against libav, and matches the general
// "Go core starts worker process, sends structured request, monitors
// output, cancels process when needed" pattern PHASES.md section 2.1
// also describes for Python workers. See DECISIONS.md ADR-015.
//
// ffmpeg/ffprobe must support the `drawtext` filter (requires FFmpeg
// built with libfreetype) for shots containing a Text primitive; a
// plain `ffmpeg` build without drawtext still works for Background/
// Image-only shots. See README.md for installation notes.
package render

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/manish96170/videofactory/domain"
)

// Settings are the deterministic encode parameters shared by every shot
// rendered together, so concatenating them (ComposeTimeline) can use a
// cheap stream copy instead of re-encoding.
type Settings struct {
	Width    int    // pixels
	Height   int    // pixels
	FPS      int    // frames per second
	FontPath string // path to a TrueType/OpenType font file, used by Text primitives
}

// validateSettings rejects a Settings value that can't produce a sane
// render (zero/negative dimensions or frame rate), rather than letting
// such a value reach ffmpeg as a confusing invocation failure.
func validateSettings(s Settings) error {
	if s.Width <= 0 || s.Height <= 0 {
		return fmt.Errorf("render: Settings.Width/Height must be positive, got %dx%d", s.Width, s.Height)
	}
	if s.FPS <= 0 {
		return fmt.Errorf("render: Settings.FPS must be positive, got %d", s.FPS)
	}
	return nil
}

// DefaultSettings returns 1080p30 settings. FontPath is best-effort
// auto-detected from a short list of common system font locations
// (see defaultFontPath); it is empty if none were found, which is fine
// for shots with no Text primitive and an explicit error for shots that
// have one.
func DefaultSettings() Settings {
	return Settings{
		Width:    1920,
		Height:   1080,
		FPS:      30,
		FontPath: defaultFontPath(),
	}
}

// defaultFontPath makes a best-effort guess at a usable font file on
// the current platform. It is deliberately a short, documented list,
// not a general font-discovery mechanism: callers that need a specific
// font (for determinism across machines, or because none of these
// exist) must set Settings.FontPath explicitly. This mirrors the
// pattern Go's own standard library uses for discovering platform
// resources (e.g. crypto/x509's default certificate locations): a
// documented, overridable best-effort default, not a silent, opaque
// one (PHASES.md section 4.10 asks us to avoid relying on
// environment-specific paths; this default is explicit, inspectable,
// and bypassed entirely by setting FontPath directly).
func defaultFontPath() string {
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/System/Library/Fonts/Supplemental/Arial.ttf",
			"/Library/Fonts/Arial.ttf",
		}
	default:
		candidates = []string{
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/truetype/liberation/LiberationSans-Regular.ttf",
		}
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// Renderer renders a single Shot's Primitives into a video clip
// (PHASES.md Phase 03). Concrete deterministic backends implement this
// without any AI/model dependency; AI-capable renderers are introduced
// behind the same shape in later phases where appropriate.
type Renderer interface {
	RenderShot(ctx context.Context, req ShotRenderRequest) (ShotRenderResult, error)
}

// ShotRenderRequest asks a Renderer to render one Shot.
type ShotRenderRequest struct {
	Shot domain.Shot
	// OutputDir is where the shot's MP4 and sidecar manifest JSON are
	// written (PHASES.md section 4.6.1: shot-017.mp4 / shot-017.render.json).
	OutputDir string
}

// ShotRenderResult is what a successful RenderShot produced.
type ShotRenderResult struct {
	MediaPath string // absolute path to the rendered MP4
	Manifest  domain.RenderManifest
}

// FFmpegRenderer is the deterministic renderer backend: it builds an
// ffmpeg filter graph from a Shot's Primitives and shells out to the
// `ffmpeg`/`ffprobe` binaries (see this package's doc comment for the
// integration strategy).
type FFmpegRenderer struct {
	settings Settings
	now      func() time.Time // overridable for deterministic tests
}

// NewFFmpegRenderer creates an FFmpegRenderer with the given Settings.
func NewFFmpegRenderer(settings Settings) *FFmpegRenderer {
	return &FFmpegRenderer{settings: settings, now: time.Now}
}

// safeIDPattern restricts shot IDs used as filenames to a conservative
// portable character set, mirroring project.FileStore's path-traversal
// guard (DECISIONS.md ADR-009) — a domain.Shot.ID is, in principle,
// caller-supplied data.
var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func safeShotFilename(id domain.ID) (string, error) {
	raw := string(id)
	if raw == "" || !safeIDPattern.MatchString(raw) {
		return "", fmt.Errorf("render: invalid shot ID for output filename: %q", raw)
	}
	return raw, nil
}

// RenderShot implements Renderer. It validates req.Shot, builds and
// runs the ffmpeg filter graph, validates the result with ffprobe
// (PHASES.md Phase 03 "FFprobe validation"), writes the render
// manifest sidecar, and returns both.
func (r *FFmpegRenderer) RenderShot(ctx context.Context, req ShotRenderRequest) (ShotRenderResult, error) {
	if err := ctx.Err(); err != nil {
		return ShotRenderResult{}, err
	}
	if req.Shot.ID.IsZero() {
		return ShotRenderResult{}, fmt.Errorf("render: Shot.ID must not be empty")
	}
	if req.Shot.Duration <= 0 {
		return ShotRenderResult{}, fmt.Errorf("render: shot %q: Duration must be positive, got %v", req.Shot.ID, req.Shot.Duration)
	}
	if req.OutputDir == "" {
		return ShotRenderResult{}, fmt.Errorf("render: OutputDir must not be empty")
	}

	filename, err := safeShotFilename(req.Shot.ID)
	if err != nil {
		return ShotRenderResult{}, err
	}

	if err := os.MkdirAll(req.OutputDir, 0o755); err != nil {
		return ShotRenderResult{}, fmt.Errorf("render: create output directory: %w", err)
	}

	plan, err := buildFilterGraph(req.Shot, r.settings)
	if err != nil {
		return ShotRenderResult{}, err
	}
	defer plan.runCleanup()

	mediaPath := filepath.Join(req.OutputDir, filename+".mp4")

	args := []string{"-y"} // overwrite: callers that want incremental-rebuild caching decide whether to call us at all (Phase 10)
	for _, in := range plan.inputs {
		args = append(args, in.args...)
	}
	args = append(args,
		"-filter_complex", plan.filterComplex,
		"-map", "["+plan.outputLabel+"]",
		"-r", formatInt(r.settings.FPS),
		"-pix_fmt", "yuv420p",
		"-c:v", "libx264",
		"-an",
		mediaPath,
	)

	if _, err := runCommand(ctx, ffmpegBinary, args...); err != nil {
		return ShotRenderResult{}, err
	}

	probe, err := Probe(ctx, mediaPath)
	if err != nil {
		return ShotRenderResult{}, fmt.Errorf("render: probing rendered shot %q: %w", req.Shot.ID, err)
	}
	if err := ValidateOutput(probe, r.settings, req.Shot.Duration); err != nil {
		return ShotRenderResult{}, fmt.Errorf("render: shot %q: %w", req.Shot.ID, err)
	}

	manifest := buildManifest(req.Shot, mediaPath, r.now())
	manifestData, err := MarshalManifest(manifest)
	if err != nil {
		return ShotRenderResult{}, err
	}
	manifestPath := filepath.Join(req.OutputDir, filename+".render.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o644); err != nil {
		return ShotRenderResult{}, fmt.Errorf("render: write manifest: %w", err)
	}

	return ShotRenderResult{MediaPath: mediaPath, Manifest: manifest}, nil
}

func formatInt(n int) string {
	return fmt.Sprintf("%d", n)
}

var _ Renderer = &FFmpegRenderer{}
