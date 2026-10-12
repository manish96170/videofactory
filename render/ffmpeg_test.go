package render

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRunCommandBoundsStderrTail(t *testing.T) {
	t.Setenv("RENDER_HELPER_PROCESS", "1")
	t.Setenv("RENDER_HELPER_MODE", "stderr")

	_, err := runCommand(context.Background(), os.Args[0], "-test.run=^TestRunCommandHelperProcess$")
	if err == nil {
		t.Fatal("runCommand succeeded; want helper process failure")
	}
	if !strings.Contains(err.Error(), "stderr-tail-marker") {
		t.Fatalf("error does not contain the end of stderr: %v", err)
	}
	if strings.Contains(err.Error(), "stderr-head-marker") {
		t.Fatalf("error retained the beginning instead of the bounded stderr tail: %v", err)
	}
	if got, max := len(err.Error()), maxCapturedStderr+512; got > max {
		t.Fatalf("error retained %d bytes, want at most %d", got, max)
	}
}

func TestRunCommandOutputCancelsOnOverflow(t *testing.T) {
	started := t.TempDir() + "/helper-started"
	t.Setenv("RENDER_HELPER_PROCESS", "1")
	t.Setenv("RENDER_HELPER_MODE", "stdout-overflow")
	t.Setenv("RENDER_HELPER_STARTED", started)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err := runCommandOutput(ctx, os.Args[0], 32, "-test.run=^TestRunCommandHelperProcess$")
	if err == nil || !strings.Contains(err.Error(), "stdout exceeded 32-byte limit") {
		t.Fatalf("runCommandOutput error = %v, want stdout-overflow error", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("parent context expired instead of child being canceled on overflow: %v", ctx.Err())
	}
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Fatalf("overflow cancellation took too long: %v", elapsed)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatalf("helper process did not start: %v", err)
	}
}

func TestTailBufferRetainsFixedCapacity(t *testing.T) {
	got := tailBuffer{buf: make([]byte, 0, maxCapturedStderr), limit: maxCapturedStderr}
	chunk := bytes.Repeat([]byte("x"), maxCapturedStderr*16)
	if n, err := got.Write(chunk); err != nil || n != len(chunk) {
		t.Fatalf("Write() = (%d, %v), want (%d, nil)", n, err, len(chunk))
	}
	if len(got.buf) != maxCapturedStderr {
		t.Fatalf("retained %d bytes, want fixed capacity %d", len(got.buf), maxCapturedStderr)
	}
	if cap(got.buf) > maxCapturedStderr {
		t.Fatalf("allocated %d bytes, want no more than fixed capacity %d", cap(got.buf), maxCapturedStderr)
	}
	if !got.truncated {
		t.Fatal("truncated = false, want true")
	}
}

func TestRawVideoOutputSizeIsBounded(t *testing.T) {
	if _, err := rawVideoOutputSize(0, 10); err == nil {
		t.Fatal("rawVideoOutputSize accepted a non-positive width")
	}
	if _, err := rawVideoOutputSize(int(^uint(0)>>1), 2); err == nil {
		t.Fatal("rawVideoOutputSize accepted an overflowing frame size")
	}
	if _, err := rawVideoOutputSize(maxCapturedPixelOutput, 1); err == nil {
		t.Fatal("rawVideoOutputSize accepted output beyond the capture bound")
	}
	want := 1920 * 1080 * 3
	if got, err := rawVideoOutputSize(1920, 1080); err != nil || got != want {
		t.Fatalf("rawVideoOutputSize(1920, 1080) = (%d, %v), want (%d, nil)", got, err, want)
	}
}

// TestRunCommandHelperProcess is re-executed as a subprocess by the tests above.
func TestRunCommandHelperProcess(t *testing.T) {
	if os.Getenv("RENDER_HELPER_PROCESS") != "1" {
		return
	}

	switch os.Getenv("RENDER_HELPER_MODE") {
	case "stderr":
		_, _ = os.Stderr.Write([]byte("stderr-head-marker\n"))
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), maxCapturedStderr*32))
		_, _ = os.Stderr.Write([]byte("\nstderr-tail-marker\n"))
		os.Exit(19)
	case "stdout-overflow":
		if err := os.WriteFile(os.Getenv("RENDER_HELPER_STARTED"), []byte("started"), 0o600); err != nil {
			os.Exit(20)
		}
		chunk := bytes.Repeat([]byte("x"), 64<<10)
		for {
			if _, err := os.Stdout.Write(chunk); err != nil {
				os.Exit(21)
			}
		}
	default:
		os.Exit(22)
	}
}
