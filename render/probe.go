package render

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// durationTolerance is how far a probed output's duration may differ
// from the requested duration and still be considered valid. Container/
// encoder rounding (keyframe alignment, GOP structure) means an exact
// match isn't realistic even for a correct render.
const durationTolerance = 150 * time.Millisecond

// ProbeResult is the subset of ffprobe's output this package checks.
type ProbeResult struct {
	Duration  time.Duration
	Width     int
	Height    int
	CodecName string
	FPS       float64
}

type ffprobeOutput struct {
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
	} `json:"streams"`
}

// Probe runs ffprobe on path and extracts duration, resolution, codec,
// and frame rate from its first video stream (PHASES.md Phase 03
// "FFprobe validation").
func Probe(ctx context.Context, path string) (ProbeResult, error) {
	out, err := runCommand(ctx, ffprobeBinary, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
	if err != nil {
		return ProbeResult{}, err
	}

	var raw ffprobeOutput
	if err := json.Unmarshal(out, &raw); err != nil {
		return ProbeResult{}, fmt.Errorf("render: parse ffprobe output for %s: %w", path, err)
	}

	durSeconds, err := strconv.ParseFloat(raw.Format.Duration, 64)
	if err != nil {
		return ProbeResult{}, fmt.Errorf("render: parse ffprobe duration %q for %s: %w", raw.Format.Duration, path, err)
	}

	for _, s := range raw.Streams {
		if s.CodecType != "video" {
			continue
		}
		fps, err := parseFrameRate(s.RFrameRate)
		if err != nil {
			return ProbeResult{}, fmt.Errorf("render: parse ffprobe frame rate %q for %s: %w", s.RFrameRate, path, err)
		}
		return ProbeResult{
			Duration:  time.Duration(durSeconds * float64(time.Second)),
			Width:     s.Width,
			Height:    s.Height,
			CodecName: s.CodecName,
			FPS:       fps,
		}, nil
	}

	return ProbeResult{}, fmt.Errorf("render: %s has no video stream", path)
}

func parseFrameRate(s string) (float64, error) {
	num, den, ok := strings.Cut(s, "/")
	if !ok {
		return strconv.ParseFloat(s, 64)
	}
	n, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, err
	}
	d, err := strconv.ParseFloat(den, 64)
	if err != nil {
		return 0, err
	}
	if d == 0 {
		return 0, fmt.Errorf("frame rate denominator is zero in %q", s)
	}
	return n / d, nil
}

// ValidateOutput checks a ProbeResult against the Settings and expected
// duration a render was supposed to produce, returning a descriptive
// error if they disagree (PHASES.md Phase 03 "FFprobe validation" —
// catching an ffmpeg invocation that exited successfully but produced
// something other than what was requested).
func ValidateOutput(probe ProbeResult, settings Settings, wantDuration time.Duration) error {
	var issues []string

	if probe.Width != settings.Width || probe.Height != settings.Height {
		issues = append(issues, fmt.Sprintf("resolution %dx%d, want %dx%d", probe.Width, probe.Height, settings.Width, settings.Height))
	}
	if diff := probe.Duration - wantDuration; diff > durationTolerance || diff < -durationTolerance {
		issues = append(issues, fmt.Sprintf("duration %v, want %v (±%v)", probe.Duration, wantDuration, durationTolerance))
	}
	if wantFPS := float64(settings.FPS); probe.FPS < wantFPS-0.01 || probe.FPS > wantFPS+0.01 {
		issues = append(issues, fmt.Sprintf("frame rate %v, want %v", probe.FPS, wantFPS))
	}

	if len(issues) > 0 {
		return fmt.Errorf("render: output validation failed: %s", strings.Join(issues, "; "))
	}
	return nil
}

// RGB is a sampled pixel's red/green/blue values (0-255 each).
type RGB struct{ R, G, B uint8 }

// CloseTo reports whether c is within tolerance (per channel) of want.
// Useful because lossy H.264 encoding introduces a few LSBs of drift
// even for a flat solid color, so exact equality isn't a realistic
// assertion against rendered (as opposed to source) pixel data.
func (c RGB) CloseTo(want RGB, tolerance int) bool {
	diff := func(a, b uint8) int {
		d := int(a) - int(b)
		if d < 0 {
			return -d
		}
		return d
	}
	return diff(c.R, want.R) <= tolerance && diff(c.G, want.G) <= tolerance && diff(c.B, want.B) <= tolerance
}

// SamplePixel extracts the pixel at (x,y) from the frame at timestamp
// at in the width x height video at path. This lets tests verify
// actually-rendered content (background color, image placement, text
// presence) rather than only file-level metadata: ffprobe can confirm
// a file is a plausible video of the right size/duration, but not that
// the right colors ended up in the right places.
//
// Seeking is done by output (ffmpeg -i path ... -ss at), not input
// (-ss at -i path): the former is frame-accurate but slower (ffmpeg
// decodes from the start), the latter seeks to the nearest keyframe
// and can return zero frames for a timestamp very close to the end of
// a short clip. Accuracy matters more than speed for these short test
// fixtures.
func SamplePixel(ctx context.Context, path string, at time.Duration, x, y, width, height int) (RGB, error) {
	if x < 0 || x >= width || y < 0 || y >= height {
		return RGB{}, fmt.Errorf("render: pixel (%d,%d) out of bounds for a %dx%d frame", x, y, width, height)
	}

	out, err := runCommand(ctx, ffmpegBinary,
		"-i", path,
		"-ss", formatSeconds(at.Seconds()),
		"-frames:v", "1",
		"-f", "rawvideo",
		"-pix_fmt", "rgb24",
		"-s", fmt.Sprintf("%dx%d", width, height),
		"-",
	)
	if err != nil {
		return RGB{}, err
	}

	want := width * height * 3
	if len(out) < want {
		return RGB{}, fmt.Errorf("render: expected %d bytes of rawvideo output from %s at %v, got %d", want, path, at, len(out))
	}

	idx := (y*width + x) * 3
	return RGB{R: out[idx], G: out[idx+1], B: out[idx+2]}, nil
}
