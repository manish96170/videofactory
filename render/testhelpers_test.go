package render

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// writeFixturePNG writes a tiny, deterministically generated solid-color
// PNG to a temp file and returns its path. Generating it in code (rather
// than committing a binary fixture) keeps the repository free of
// checked-in binary test assets (PHASES.md section 17/18 public-
// repository hygiene is about generated media/large artifacts, but
// generating this at test time sidesteps the question entirely).
func writeFixturePNG(t *testing.T) string {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 200, G: 100, B: 50, A: 255})
		}
	}

	path := filepath.Join(t.TempDir(), "fixture.png")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create fixture PNG: %v", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode fixture PNG: %v", err)
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		t.Fatalf("write fixture PNG: %v", err)
	}
	return path
}

// requireFFmpeg skips the test if ffmpeg/ffprobe aren't on PATH, so the
// render package's test suite degrades gracefully on a machine without
// them instead of failing (PHASES.md's native-tools architecture means
// ffmpeg is a system dependency, not a Go module dependency — it won't
// always be present).
func requireFFmpeg(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not found on PATH; skipping")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not found on PATH; skipping")
	}
}

// requireDrawtext additionally skips the test if the available ffmpeg
// wasn't built with the drawtext filter (common for a minimal `ffmpeg`
// formula without libfreetype) — a Text-primitive test needs it, a
// Background/Image-only test doesn't.
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

// requireFontPath skips the test if DefaultSettings() couldn't find a
// usable font (so drawtext-dependent assertions don't fail on a
// machine with ffmpeg+drawtext but no font at the hardcoded candidate
// paths).
func requireFontPath(t *testing.T) string {
	t.Helper()
	path := defaultFontPath()
	if path == "" {
		t.Skip("no default font found on this machine; skipping")
	}
	return path
}
