package worker

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/manish96170/videofactory/resource"
)

// CommandSpec is trusted runtime configuration, never generation parameters.
// Exe must be absolute; Env is explicit (nil means empty, not ambient inheritance).
// JSON serialization intentionally omits every sensitive field.
type CommandSpec struct {
	Exe  string   `json:"-"`
	Argv []string `json:"-"`
	CWD  string   `json:"-"`
	Env  []string `json:"-"`
}

type Limits struct {
	RAMBytes int64 `json:"ramBytes"`
	// Zero Timeout uses the caller's context without an invented job deadline.
	Timeout time.Duration `json:"timeout"`
	// Zero chooses the reference 100ms interval; negative is invalid.
	SampleInterval time.Duration `json:"sampleInterval"`
}

type Config struct {
	Monitor        Monitor          `json:"-"`
	Clock          func() time.Time `json:"-"`
	CleanupTimeout time.Duration    `json:"cleanupTimeout"`
}

type Runner struct {
	monitor        Monitor
	clock          func() time.Time
	cleanupTimeout time.Duration
}

func NewRunner(c Config) (*Runner, error) {
	if c.CleanupTimeout < 0 {
		return nil, ErrInvalidConfig
	}
	if c.Monitor == nil {
		c.Monitor = NativeMonitor{}
	} else {
		v := reflect.ValueOf(c.Monitor)
		switch v.Kind() {
		case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
			if v.IsNil() {
				return nil, ErrInvalidConfig
			}
		}
	}
	if c.Clock == nil {
		c.Clock = time.Now
	}
	if c.CleanupTimeout == 0 {
		c.CleanupTimeout = 5 * time.Second
	}
	return &Runner{monitor: c.Monitor, clock: c.Clock, cleanupTimeout: c.CleanupTimeout}, nil
}

type RunResult struct {
	Reply       *Reply               `json:"reply,omitempty"`
	Measurement resource.Measurement `json:"measurement"`
	Stopped     bool                 `json:"stopped"`
	// Cleanup is an owned lifecycle handle, not history evidence or a DTO.
	Cleanup             *CleanupHandle `json:"-"`
	StderrCapturedBytes int            `json:"stderrCapturedBytes"`
	StderrTruncated     bool           `json:"stderrTruncated"`
}

func validSpec(s CommandSpec) bool {
	if !filepath.IsAbs(s.Exe) || strings.IndexByte(s.Exe, 0) >= 0 || (s.CWD != "" && !filepath.IsAbs(s.CWD)) {
		return false
	}
	for _, v := range append(append([]string{s.CWD}, s.Argv...), s.Env...) {
		if strings.IndexByte(v, 0) >= 0 {
			return false
		}
	}
	for _, v := range s.Env {
		i := strings.IndexByte(v, '=')
		if i <= 0 {
			return false
		}
	}
	return true
}

// Run returns only after proven cleanup, or with a handle owning all remaining
// tasks. Caller cancellation wins at the final boundary, including late native
// callbacks and cleanup. Even ordinary leader exit kills remaining descendants.
func (r *Runner) Run(ctx context.Context, spec CommandSpec, request Request, limits Limits) (result RunResult, err error) {
	result.Stopped = true // no child ownership until successful Start
	defer func() {
		if ctx != nil && ctx.Err() != nil {
			result.Reply = nil
			err = ctx.Err()
		}
	}()
	if ctx == nil || r == nil || r.monitor == nil || r.clock == nil || !validSpec(spec) || limits.RAMBytes <= 0 || limits.Timeout < 0 || limits.SampleInterval < 0 {
		return result, ErrInvalidConfig
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if !platformSupported() {
		return result, ErrUnsupported
	}
	input, err := encodeRequest(request)
	if err != nil {
		return result, err
	}
	spec.Argv = append([]string(nil), spec.Argv...)
	spec.Env = append([]string{}, spec.Env...)
	interval := limits.SampleInterval
	if interval == 0 {
		interval = 100 * time.Millisecond
	}
	runCtx, cancel := context.WithCancel(ctx)
	if limits.Timeout > 0 {
		cancel()
		runCtx, cancel = context.WithTimeout(ctx, limits.Timeout)
	}
	defer cancel()
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	cmd := exec.Command(spec.Exe, spec.Argv...)
	cmd.Dir, cmd.Env = spec.CWD, spec.Env
	configureGroup(cmd)
	stdinR, stdinW, e := os.Pipe()
	if e != nil {
		return result, ErrStart
	}
	defer stdinR.Close()
	stdoutR, stdoutW, e := os.Pipe()
	if e != nil {
		stdinW.Close()
		return result, ErrStart
	}
	defer stdoutW.Close()
	stderrR, stderrW, e := os.Pipe()
	if e != nil {
		stdinW.Close()
		stdoutR.Close()
		return result, ErrStart
	}
	defer stderrW.Close()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdinR, stdoutW, stderrW
	start := r.clock()
	if runCtx.Err() != nil {
		stdinW.Close()
		stdoutR.Close()
		stderrR.Close()
		return result, runCtx.Err()
	}
	if e = cmd.Start(); e != nil {
		stdinW.Close()
		stdoutR.Close()
		stderrR.Close()
		return result, ErrStart
	}
	stdinR.Close()
	stdoutW.Close()
	stderrW.Close()
	result.Stopped = false
	monitorCtx, monitorCancel := context.WithCancel(runCtx)
	h := newCleanupHandle(ProcessGroup{ID: cmd.Process.Pid}, r.monitor, monitorCancel, stdinW, stdoutR, stderrR)
	h.ramLimit = limits.RAMBytes
	// Exactly one Wait path. No exec-managed pipe copy goroutines exist: the
	// child ends are *os.File and our three I/O tasks below are explicitly owned.
	go func() {
		e := cmd.Wait()
		h.mu.Lock()
		h.waitFailed = e != nil
		h.processState = cmd.ProcessState
		h.mu.Unlock()
		close(h.waitDone)
	}()
	go func() {
		defer close(h.stdinDone)
		_, e := stdinW.Write(input)
		stdinW.Close()
		if e != nil {
			h.mu.Lock()
			h.stdinFailed = true
			h.mu.Unlock()
			h.notify(ErrWorker)
		}
	}()
	go h.collect(stdoutR, MaxStdoutBytes, true)
	go h.collect(stderrR, MaxStderrBytes, false)
	go h.observe(monitorCtx, interval, limits.RAMBytes)
	select {
	case <-runCtx.Done():
		err = runCtx.Err()
	case err = <-h.events:
	case <-h.waitDone:
	case <-h.stdoutDone:
		// A closed output stream can expose a protocol failure before the
		// leader exits. Valid EOF alone is not permission to abandon Wait.
		h.mu.Lock()
		output, overflow, failed := h.stdout, h.outputOverflow, h.stdoutFailed
		h.mu.Unlock()
		if overflow {
			err = ErrOutputLimit
		} else if failed {
			err = ErrWorker
		} else {
			_, err = decodeReply(output, request.Header)
		}
		if err == nil {
			select {
			case <-runCtx.Done():
				err = runCtx.Err()
			case err = <-h.events:
			case <-h.waitDone:
			}
		}
	}
	// Separately bounded cleanup must not inherit caller cancellation.
	cleanupCtx, cleanupCancel := context.WithTimeout(context.WithoutCancel(ctx), r.cleanupTimeout)
	stopped, cleanupErr := h.RetryStop(cleanupCtx)
	cleanupCancel()
	result.Stopped = stopped
	if !stopped {
		result.Cleanup = h
	}
	result.Measurement = h.measurement(stopped && err == nil && runCtx.Err() == nil)
	end := r.clock()
	if !end.Before(start) {
		ms := end.Sub(start).Milliseconds()
		result.Measurement.RunMs = &ms
		result.Measurement.TimingSource = resource.SourceParentClock
		result.Measurement.TimingCompleteness = resource.MeasurementPartial
		if stopped {
			result.Measurement.TimingCompleteness = resource.MeasurementComplete
		}
	}
	h.mu.Lock()
	result.StderrCapturedBytes, result.StderrTruncated = len(h.stderr), h.stderrTruncated
	failed, monitorFailed, outputOverflow, ioFailed := h.waitFailed, h.monitorFailed, h.outputOverflow, h.stdoutFailed || h.stdinFailed
	runtimeExitFailure := h.processState != nil && h.processState.Exited() && h.processState.ExitCode() != 0
	var output []byte
	if stopped {
		output = h.stdout
	}
	// A returned quarantine handle retains lifecycle state, not raw I/O. Any
	// still-owned reader only drains after this boundary; successful output is
	// held locally for the bounded typed decode below.
	h.discardIO = true
	h.stdout, h.stderr = nil, nil
	h.mu.Unlock()
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	if runCtx.Err() != nil {
		return result, runCtx.Err()
	}
	if p := result.Measurement.ObservedPeak(); p != nil && *p > limits.RAMBytes {
		return result, ErrLimitExceeded
	}
	if outputOverflow {
		return result, ErrOutputLimit
	}
	if monitorFailed {
		return result, ErrMonitor
	}
	if runtimeExitFailure {
		return result, ErrWorker
	}
	if err != nil {
		return result, err
	}
	if cleanupErr != nil {
		return result, ErrCleanup
	}
	if failed || ioFailed {
		return result, ErrWorker
	}
	result.Reply, err = decodeReply(output, request.Header)
	return result, err
}

// CleanupHandle cannot be serialized and exposes no raw runtime configuration.
// Keep it until RetryStop proves true. Pending Wait/reader/monitor/proof tasks
// remain reachable here; retry joins those same tasks rather than abandoning
// them or launching an additional Wait/query when a prior query is pending.
type CleanupHandle struct {
	group           ProcessGroup
	monitor         Monitor
	monitorCancel   context.CancelFunc
	stdin           *os.File
	stdoutPipe      *os.File
	stderrPipe      *os.File
	waitDone        chan struct{}
	stdinDone       chan struct{}
	stdoutDone      chan struct{}
	stderrDone      chan struct{}
	monitorDone     chan struct{}
	events          chan error
	gate            chan struct{}
	mu              sync.Mutex
	processState    *os.ProcessState
	waitFailed      bool
	monitorFailed   bool
	familyPartial   bool
	limitExceeded   bool
	ramLimit        int64
	stdinFailed     bool
	stdoutFailed    bool
	outputOverflow  bool
	stderrTruncated bool
	stdout          []byte
	stderr          []byte
	discardIO       bool
	familyPeak      *int64
	proof           *proofTask // accessed only while gate is owned
	stopped         bool
}

type proofTask struct {
	done   chan struct{}
	proved bool
}

func (h *CleanupHandle) MarshalJSON() ([]byte, error) { return nil, ErrInvalidConfig }

var _ json.Marshaler = (*CleanupHandle)(nil)

func newCleanupHandle(group ProcessGroup, monitor Monitor, cancel context.CancelFunc, stdin, stdout, stderr *os.File) *CleanupHandle {
	h := &CleanupHandle{group: group, monitor: monitor, monitorCancel: cancel, stdin: stdin, stdoutPipe: stdout, stderrPipe: stderr,
		waitDone: make(chan struct{}), stdinDone: make(chan struct{}), stdoutDone: make(chan struct{}), stderrDone: make(chan struct{}), monitorDone: make(chan struct{}), events: make(chan error, 8), gate: make(chan struct{}, 1)}
	h.gate <- struct{}{}
	return h
}
func (h *CleanupHandle) notify(err error) {
	select {
	case h.events <- err:
	default:
	}
}
func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func (h *CleanupHandle) collect(pipe *os.File, max int, stdout bool) {
	done := h.stderrDone
	if stdout {
		done = h.stdoutDone
	}
	defer close(done)
	defer pipe.Close()
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			h.mu.Lock()
			if h.discardIO {
				h.mu.Unlock()
				if err != nil {
					return
				}
				continue
			}
			dst := &h.stderr
			if stdout {
				dst = &h.stdout
			}
			left := max - len(*dst)
			keep := n
			if keep > left {
				keep = left
			}
			*dst = appendCapture(*dst, buf[:keep], max)
			if n > left {
				if stdout {
					h.outputOverflow = true
				} else {
					h.stderrTruncated = true
				}
			}
			h.mu.Unlock()
			if stdout && n > left {
				h.notify(ErrOutputLimit)
			}
		}
		if err != nil {
			if stdout && err != io.EOF && !closed(h.waitDone) {
				h.mu.Lock()
				h.stdoutFailed = true
				h.mu.Unlock()
				h.notify(ErrWorker)
			}
			return
		}
	}
}

// Keep both length and backing-array capacity within the capture bound.
func appendCapture(dst, p []byte, max int) []byte {
	if len(dst)+len(p) > cap(dst) {
		n := 2 * cap(dst)
		if n < len(dst)+len(p) {
			n = len(dst) + len(p)
		}
		if n > max {
			n = max
		}
		copyOf := make([]byte, len(dst), n)
		copy(copyOf, dst)
		dst = copyOf
	}
	return append(dst, p...)
}

func (h *CleanupHandle) observe(ctx context.Context, interval time.Duration, ram int64) {
	defer close(h.monitorDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		s, err := h.monitor.Sample(ctx, h.group)
		if err == nil {
			err = validateSample(s)
		}
		// Late canceled observations are not evidence, but the task is still joined.
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			h.mu.Lock()
			h.monitorFailed = true
			h.mu.Unlock()
			h.notify(ErrMonitor)
			return
		}
		if h.recordFamilySample(s, ram) {
			h.notify(ErrLimitExceeded)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Both observe and cleanup consume only validated, uncanceled native samples.
// A breach is sticky evidence, not permission to stop the cleanup/join path.
func (h *CleanupHandle) recordFamilySample(s Sample, ram int64) bool {
	if s.Live == 0 || s.RSSBytes == nil {
		return false
	}
	v := *s.RSSBytes
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.familyPeak == nil || v > *h.familyPeak {
		h.familyPeak = &v
	}
	if v > ram {
		h.familyPartial = true
		h.limitExceeded = true
		return true
	}
	return false
}

func (h *CleanupHandle) stoppedError() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.limitExceeded {
		return ErrLimitExceeded
	}
	return nil
}

// RetryStop requires a bounded caller context. It signals the owned group,
// waits for a native no-live-members proof and joins ALL known tasks. A signal
// request, canceled callback, leader exit, or a failed query alone is not proof.
// A proven stop can return true with ErrLimitExceeded: the bool is stop proof,
// while the error reports a retained run/cleanup RAM breach, not uncertain stop.
func (h *CleanupHandle) RetryStop(ctx context.Context) (bool, error) {
	if h == nil || ctx == nil {
		return false, ErrCleanup
	}
	if _, ok := ctx.Deadline(); !ok {
		return false, ErrInvalidConfig
	}
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case <-h.gate:
	}
	defer func() { h.gate <- struct{}{} }()
	if h.stopped {
		return true, h.stoppedError()
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	h.monitorCancel()
	h.stdin.Close()
	if !validGroup(h.group) {
		return false, ErrCleanup
	}
	if h.proof == nil || (closed(h.proof.done) && !h.proof.proved) {
		p := &proofTask{done: make(chan struct{})}
		h.proof = p
		go func() {
			defer func() {
				if !p.proved {
					h.mu.Lock()
					h.familyPartial = true
					h.mu.Unlock()
				}
				close(p.done)
			}()
			// macOS can return EPERM while a group contains only an exiting
			// zombie. A failed signal is never proof, but a later native empty
			// observation plus reap/join is sufficient even in that race.
			signalErr := killGroup(h.group)
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				if ctx.Err() != nil {
					return
				}
				s, err := h.monitor.Sample(ctx, h.group)
				if ctx.Err() != nil || err != nil || validateSample(s) != nil {
					return
				}
				h.recordFamilySample(s, h.ramLimit)
				if s.Live == 0 && closed(h.waitDone) {
					p.proved = true
					return
				}
				if s.Live > 0 && signalErr != nil {
					return
				}
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
				}
			}
		}()
	}
	select {
	case <-h.proof.done:
		if !h.proof.proved {
			h.stdoutPipe.Close()
			h.stderrPipe.Close()
			return false, ErrCleanup
		}
	case <-ctx.Done():
		h.stdoutPipe.Close()
		h.stderrPipe.Close()
		return false, ErrCleanup
	}
	for _, done := range []chan struct{}{h.waitDone, h.stdinDone, h.stdoutDone, h.stderrDone, h.monitorDone} {
		select {
		case <-done:
		case <-ctx.Done():
			h.stdoutPipe.Close()
			h.stderrPipe.Close()
			return false, ErrCleanup
		}
	}
	h.stopped = true
	return true, h.stoppedError()
}

func (h *CleanupHandle) measurement(collectionComplete bool) resource.Measurement {
	h.mu.Lock()
	defer h.mu.Unlock()
	var m resource.Measurement
	if h.familyPeak != nil {
		m.FamilyRSS = &resource.MemoryPeak{Bytes: *h.familyPeak, Source: resource.SourceNativeFamilyRSS, Completeness: resource.MeasurementPartial}
		if collectionComplete && !h.monitorFailed && !h.familyPartial {
			m.FamilyRSS.Completeness = resource.MeasurementComplete
		}
	}
	if value, ok := processPeak(h.processState); ok {
		m.ProcessPeakRSS = &resource.MemoryPeak{Bytes: value, Source: resource.SourceOSProcessPeak, Completeness: resource.MeasurementComplete}
	}
	return m
}
