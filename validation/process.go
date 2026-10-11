package validation

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"time"
)

// FFmpeg allocation limits guard individual allocations, not whole-process RAM.
// Trusted executables must not daemonize or spawn escaping descendants. This is
// a synchronous native command, not the model worker/resource manager lifecycle.
func runNative(ctx context.Context, cwd, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, safeError("runtime_missing", errors.Join(ErrRuntimeMissing, err))
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(child, path, args...)
	cmd.Dir = cwd
	cmd.WaitDelay = 250 * time.Millisecond
	out := &boundedCapture{limit: 64 * 1024, cancel: cancel}
	stderr := &boundedCapture{limit: 8 * 1024, cancel: cancel}
	cmd.Stdout = out
	cmd.Stderr = stderr
	err = cmd.Run()
	ctxErr := ctx.Err()
	if errors.Is(ctxErr, context.Canceled) {
		return nil, ctxErr
	}
	if out.breached || stderr.breached {
		if ctxErr != nil {
			return nil, errors.Join(ErrInspectionLimit, ctxErr)
		}
		return nil, ErrInspectionLimit
	}
	if ctxErr != nil {
		return nil, ctxErr
	}
	if err != nil {
		return nil, safeError("native_failed", errors.Join(ErrInvalidMedia, err))
	}
	return out.Bytes(), nil
}

type boundedCapture struct {
	buffer   bytes.Buffer
	limit    int
	cancel   context.CancelFunc
	breached bool
}

func (w *boundedCapture) Len() int      { return w.buffer.Len() }
func (w *boundedCapture) Bytes() []byte { return w.buffer.Bytes() }

func (w *boundedCapture) Write(p []byte) (int, error) {
	remaining := w.limit - w.Len()
	if len(p) <= remaining {
		return w.buffer.Write(p)
	}
	w.buffer.Write(p[:remaining])
	w.breached = true
	w.cancel()
	return len(p), nil
}
