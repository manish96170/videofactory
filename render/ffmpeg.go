package render

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// maxCapturedStderr bounds how much of a failed command's stderr we
// include in an error message, so a pathological or very verbose
// ffmpeg failure can't produce an unreadable (or memory-hungry) error.
const maxCapturedStderr = 4096

// ffmpegBinary/ffprobeBinary name (or path to) the executables this
// package shells out to. They are package-level variables, not
// constants, solely so tests can point them at a stub for unit tests
// that don't need a real ffmpeg; production code must not reassign
// them.
var (
	ffmpegBinary  = "ffmpeg"
	ffprobeBinary = "ffprobe"
)

// runCommand runs name with args, honoring ctx cancellation (killing
// the process if ctx is done — PHASES.md section 4.8/2.1), and returns
// its stdout. On a non-zero exit it returns an error that includes a
// bounded tail of stderr, so failures are diagnosable without needing
// to re-run the command manually.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("render: %s not found on PATH: %w", name, err)
	}

	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("render: %s failed: %w\n%s", name, err, truncateTail(stderr.String(), maxCapturedStderr))
	}

	return stdout.Bytes(), nil
}

// truncateTail returns the last max bytes of s (on a line boundary
// where convenient), prefixed with a note if truncation happened.
func truncateTail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	tail := s[len(s)-max:]
	if idx := strings.IndexByte(tail, '\n'); idx >= 0 && idx < max/4 {
		tail = tail[idx+1:]
	}
	return fmt.Sprintf("[... truncated %d bytes ...]\n%s", len(s)-len(tail), tail)
}
