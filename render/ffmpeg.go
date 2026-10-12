package render

import (
	"context"
	"fmt"
	"io"
	"os/exec"
)

// maxCapturedStderr bounds how much of a failed command's stderr we
// include in an error message, so a pathological or very verbose
// ffmpeg failure can't produce an unreadable (or memory-hungry) error.
const maxCapturedStderr = 4096

// maxCapturedProbeStdout bounds ffprobe's JSON response. Probe output is
// needed by the caller, unlike normal FFmpeg command stdout.
const maxCapturedProbeStdout = 1 << 20

// ffmpegBinary/ffprobeBinary name (or path to) the executables this
// package shells out to. They are package-level variables, not
// constants, solely so tests can point them at a stub for unit tests
// that don't need a real ffmpeg; production code must not reassign
// them.
var (
	ffmpegBinary  = "ffmpeg"
	ffprobeBinary = "ffprobe"
)

// runCommand runs name with args, discarding stdout and honoring ctx
// cancellation. On a non-zero exit it returns an error that includes a
// fixed-size tail of stderr.
func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	return runCommandInternal(ctx, name, 0, false, args...)
}

// runCommandOutput runs name with args and captures at most maxBytes of
// stdout. If the child exceeds that bound, it is canceled and reaped
// before this function returns an error.
func runCommandOutput(ctx context.Context, name string, maxBytes int, args ...string) ([]byte, error) {
	if maxBytes < 0 {
		return nil, fmt.Errorf("render: stdout limit must not be negative")
	}
	return runCommandInternal(ctx, name, maxBytes, true, args...)
}

func runCommandInternal(ctx context.Context, name string, stdoutLimit int, captureStdout bool, args ...string) ([]byte, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, fmt.Errorf("render: %s not found on PATH: %w", name, err)
	}

	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, path, args...)
	stderr := &tailBuffer{buf: make([]byte, 0, maxCapturedStderr), limit: maxCapturedStderr}
	cmd.Stderr = stderr
	var stdout *limitedBuffer
	if captureStdout {
		stdout = &limitedBuffer{buf: make([]byte, 0, stdoutLimit), limit: stdoutLimit, cancel: cancel}
		cmd.Stdout = stdout
	} else {
		cmd.Stdout = io.Discard
	}

	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		if stdout != nil && stdout.overflow {
			return nil, fmt.Errorf("render: %s stdout exceeded %d-byte limit", name, stdoutLimit)
		}
		return nil, fmt.Errorf("render: %s failed: %w\n%s", name, err, stderr.String())
	}

	if stdout != nil {
		if stdout.overflow {
			return nil, fmt.Errorf("render: %s stdout exceeded %d-byte limit", name, stdoutLimit)
		}
		return stdout.buf, nil
	}
	return nil, nil
}

// limitedBuffer retains no more than limit bytes and cancels its child
// command on the first byte beyond the limit.
type limitedBuffer struct {
	buf      []byte
	limit    int
	cancel   context.CancelFunc
	overflow bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.limit - len(b.buf)
	if len(p) <= remaining {
		b.buf = append(b.buf, p...)
		return len(p), nil
	}
	if remaining > 0 {
		b.buf = append(b.buf, p[:remaining]...)
	}
	if !b.overflow {
		b.overflow = true
		b.cancel()
	}
	// Report the bytes consumed so os/exec's pipe-copy goroutine can finish
	// normally while CommandContext terminates and reaps the child.
	return len(p), nil
}

// tailBuffer keeps only the most recent limit bytes written to it.
type tailBuffer struct {
	buf       []byte
	limit     int
	truncated bool
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	if len(p) >= b.limit {
		if len(p) > b.limit || len(b.buf) > 0 {
			b.truncated = true
		}
		b.buf = append(b.buf[:0], p[len(p)-b.limit:]...)
		return len(p), nil
	}
	if excess := len(b.buf) + len(p) - b.limit; excess > 0 {
		copy(b.buf, b.buf[excess:])
		b.buf = b.buf[:len(b.buf)-excess]
		b.truncated = true
	}
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *tailBuffer) String() string {
	if b.truncated {
		return "[... stderr truncated ...]\n" + string(b.buf)
	}
	return string(b.buf)
}
