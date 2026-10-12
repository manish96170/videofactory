//go:build darwin || linux

package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/manish96170/videofactory/resource"
)

// These helpers execute this test binary directly: no shell, Python, downloads,
// or environment mutation. The role is explicit per-child trusted config.
func TestWorkerHelper(t *testing.T) {
	role := os.Getenv("LVG_WORKER_TEST_ROLE")
	if role == "" {
		return
	}
	os.Exit(helperMain(role))
}

func helperMain(role string) int {
	if role == "descendant" {
		ready := os.NewFile(3, "ready")
		if _, err := ready.Write([]byte{1}); err != nil {
			return 2
		}
		ready.Close()
		time.Sleep(time.Hour)
		return 0
	}
	if role == "no-stdin" {
		time.Sleep(time.Hour)
		return 0
	}
	input, err := io.ReadAll(io.LimitReader(os.Stdin, MaxStdinBytes+1))
	if err != nil || len(input) > MaxStdinBytes {
		return 3
	}
	var request Request
	if strictJSON(input, &request) != nil {
		return 4
	}
	if role == "barrier-success" {
		timeout := time.NewTimer(5 * time.Second)
		defer timeout.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Stat(os.Getenv("LVG_WORKER_READY_FILE")); err == nil {
				break
			}
			select {
			case <-timeout.C:
				return 13
			case <-tick.C:
			}
		}
	}
	reply := testReply()
	reply.Header = request.Header
	if role == "wait" {
		time.Sleep(time.Hour)
		return 0
	}
	if role == "descendant-exit" || role == "descendant-wait" {
		exe, err := os.Executable()
		if err != nil {
			return 5
		}
		r, w, err := os.Pipe()
		if err != nil {
			return 6
		}
		child := exec.Command(exe, "-test.run=^TestWorkerHelper$")
		child.Env = []string{"LVG_WORKER_TEST_ROLE=descendant"}
		child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
		child.ExtraFiles = []*os.File{w}
		if child.Start() != nil {
			return 7
		}
		w.Close()
		var ack [1]byte
		if _, err = io.ReadFull(r, ack[:]); err != nil {
			return 8
		}
		r.Close()
		if os.WriteFile(os.Getenv("LVG_WORKER_PID_FILE"), []byte(strconv.Itoa(child.Process.Pid)), 0600) != nil {
			return 9
		}
		if role == "descendant-wait" {
			time.Sleep(time.Hour)
			return 0
		}
		// Deliberately no Wait: the parent runner must clean the group after our
		// ordinary exit, despite the descendant retaining stdout/stderr writers.
	}
	switch role {
	case "malformed", "malformed-open":
		os.Stdout.Write([]byte("{"))
		if role == "malformed-open" {
			os.Stdout.Close()
			time.Sleep(time.Hour)
		}
		return 0
	case "missing":
		os.Stdout.Write([]byte(`{"status":"ok"}`))
		return 0
	case "oversized":
		block := bytes.Repeat([]byte{'x'}, 4096)
		for {
			if _, err := os.Stdout.Write(block); err != nil {
				return 0
			}
		}
	case "stderr":
		block := bytes.Repeat([]byte("credential-secret!"), 1024)
		for n := 0; n < 40; n++ {
			if _, err := os.Stderr.Write(block); err != nil {
				return 10
			}
		}
	case "error":
		reply.Status = "error"
		reply.ErrorCode = "failed"
		reply.Payload = json.RawMessage(`{"credential":"secret","notGenerationResult":true}`)
	case "exit-error":
		os.Stderr.Write([]byte("credential-secret runtime path"))
		return 11
	case "wrong-model":
		reply.Header.ModelID = "wrong"
	case "wrong-request":
		reply.Payload = json.RawMessage(`{"id":"result","requestId":"wrong"}`)
	case "unknown":
		os.Stdout.Write([]byte(`{"unknown":1}`))
		return 0
	}
	if json.NewEncoder(os.Stdout).Encode(reply) != nil {
		return 12
	}
	if role == "trailing" {
		os.Stdout.Write([]byte(`{}`))
	}
	return 0
}

type monitorFunc func(context.Context, ProcessGroup) (Sample, error)

func (f monitorFunc) Sample(ctx context.Context, g ProcessGroup) (Sample, error) { return f(ctx, g) }

func TestNewRunnerRejectsTypedNilMonitor(t *testing.T) {
	for _, tc := range []struct {
		name    string
		monitor Monitor
	}{
		{"pointer", (*NativeMonitor)(nil)},
		{"function", monitorFunc(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Constructor-only regression: no subprocess or callback is invoked.
			r, err := NewRunner(Config{Monitor: tc.monitor})
			if r != nil || !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("typed-nil monitor admitted: runner=%t error=%v", r != nil, err)
			}
		})
	}
}

func TestNewRunnerNilMonitorDefaultsNative(t *testing.T) {
	r, err := NewRunner(Config{Monitor: nil})
	if err != nil || r == nil {
		t.Fatal("plain nil monitor should select native default", err)
	}
	if _, ok := r.monitor.(NativeMonitor); !ok {
		t.Fatal("plain nil monitor did not select NativeMonitor")
	}
}

func helperSpec(t *testing.T, role string) CommandSpec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return CommandSpec{Exe: exe, Argv: []string{"-test.run=^TestWorkerHelper$"}, Env: []string{"LVG_WORKER_TEST_ROLE=" + role}}
}

func fakeRunner(t *testing.T, c Config) *Runner {
	t.Helper()
	if c.Monitor == nil {
		c.Monitor = monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) { return Sample{}, nil })
	}
	r, err := NewRunner(c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func standardLimits() Limits { return Limits{RAMBytes: 2_000_000_000, Timeout: 5 * time.Second} }

func assertStopped(t *testing.T, result RunResult) {
	t.Helper()
	if !result.Stopped || result.Cleanup != nil {
		t.Fatalf("stop proof absent: %+v", result)
	}
	if err := result.Measurement.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestCoreHelperSuccess(t *testing.T) {
	var calls int
	clock := func() time.Time {
		calls++
		return time.Unix(100, 0).Add(time.Duration(calls-1) * 37 * time.Millisecond)
	}
	r := fakeRunner(t, Config{Clock: clock})
	result, err := r.Run(context.Background(), helperSpec(t, "success"), testRequest(), standardLimits())
	if err != nil || result.Reply == nil {
		t.Fatal(result, err)
	}
	assertStopped(t, result)
	if result.Measurement.RunMs == nil || *result.Measurement.RunMs != 37 || result.Measurement.TimingSource != resource.SourceParentClock {
		t.Fatal(result.Measurement)
	}
	if result.Measurement.FamilyRSS != nil {
		t.Fatal("unknown family observation invented", result.Measurement)
	}
	if result.Measurement.ProcessPeakRSS == nil || result.Measurement.ProcessPeakRSS.Source != resource.SourceOSProcessPeak {
		t.Fatal(result.Measurement)
	}
	// Worker-claimed 1e100 MB did not influence the native measurement or limit.
	if *result.Measurement.ObservedPeak() >= standardLimits().RAMBytes {
		t.Fatal(result.Measurement)
	}
}

func TestHelperExactSerializedStdinBound(t *testing.T) {
	request := testRequest()
	base, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	request.Payload = append(request.Payload, bytes.Repeat([]byte{' '}, MaxStdinBytes-len(base))...)
	result, err := fakeRunner(t, Config{}).Run(context.Background(), helperSpec(t, "success"), request, standardLimits())
	if err != nil || result.Reply == nil {
		t.Fatal(result, err)
	}
	assertStopped(t, result)
}

func TestHelperReplyFailures(t *testing.T) {
	for _, c := range []struct {
		role string
		want error
	}{{"malformed", ErrProtocol}, {"malformed-open", ErrProtocol}, {"missing", ErrProtocol}, {"trailing", ErrProtocol}, {"unknown", ErrProtocol}, {"wrong-model", ErrProtocol}, {"wrong-request", ErrProtocol}, {"error", ErrWorker}, {"exit-error", ErrWorker}} {
		t.Run(c.role, func(t *testing.T) {
			result, err := fakeRunner(t, Config{}).Run(context.Background(), helperSpec(t, c.role), testRequest(), standardLimits())
			if !errors.Is(err, c.want) || result.Reply != nil || strings.Contains(err.Error(), "credential") {
				t.Fatal(result, err)
			}
			assertStopped(t, result)
		})
	}
}

func TestHelperBoundedStderrAndSafeSerialization(t *testing.T) {
	spec := helperSpec(t, "stderr")
	spec.Env = append(spec.Env, "PRIVATE_KEY=credential-secret")
	result, err := fakeRunner(t, Config{}).Run(context.Background(), spec, testRequest(), standardLimits())
	if err != nil {
		t.Fatal(err)
	}
	assertStopped(t, result)
	if result.StderrCapturedBytes != MaxStderrBytes || !result.StderrTruncated {
		t.Fatal(result)
	}
	for _, v := range []any{spec, Config{Monitor: NativeMonitor{}, Clock: time.Now}, result, result.Measurement} {
		b := mustJSON(t, v)
		if strings.Contains(string(b), "credential") || strings.Contains(string(b), spec.Exe) {
			t.Fatal("raw runtime data serialized")
		}
	}
	if _, err := json.Marshal(&CleanupHandle{}); err == nil {
		t.Fatal("cleanup handle serialized")
	}
}

func TestOversizedStdoutWriterCanceledAndJoined(t *testing.T) {
	start := time.Now()
	result, err := fakeRunner(t, Config{}).Run(context.Background(), helperSpec(t, "oversized"), testRequest(), standardLimits())
	if !errors.Is(err, ErrOutputLimit) || time.Since(start) > 3*time.Second {
		t.Fatal(result, err)
	}
	assertStopped(t, result)
	if result.Reply != nil {
		t.Fatal(result.Reply)
	}
}

func TestParentCancelTimeoutAndBlockedStdin(t *testing.T) {
	for _, role := range []string{"wait", "no-stdin"} {
		t.Run(role, func(t *testing.T) {
			entered := make(chan struct{})
			var once sync.Once
			m := monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) {
				once.Do(func() { close(entered) })
				return Sample{}, nil
			})
			r := fakeRunner(t, Config{Monitor: m})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			request := testRequest()
			if role == "no-stdin" {
				request.Payload = append(request.Payload, bytes.Repeat([]byte{' '}, MaxStdinBytes/2)...)
			}
			type outcome struct {
				result RunResult
				err    error
			}
			done := make(chan outcome, 1)
			spec := helperSpec(t, role)
			go func() {
				result, err := r.Run(ctx, spec, request, standardLimits())
				done <- outcome{result, err}
			}()
			await(t, entered)
			cancel()
			select {
			case out := <-done:
				if !errors.Is(out.err, context.Canceled) {
					t.Fatal(out.err)
				}
				assertStopped(t, out.result)
			case <-time.After(5 * time.Second):
				t.Fatal("cancellation hung")
			}
		})
	}
	limits := standardLimits()
	limits.Timeout = 50 * time.Millisecond
	result, err := fakeRunner(t, Config{}).Run(context.Background(), helperSpec(t, "wait"), testRequest(), limits)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(result, err)
	}
	assertStopped(t, result)
}

func TestCallerCancellationWinsDuringFinalClock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls int
	clock := func() time.Time {
		calls++
		if calls == 2 {
			cancel()
		}
		return time.Unix(100+int64(calls), 0)
	}
	result, err := fakeRunner(t, Config{Clock: clock}).Run(ctx, helperSpec(t, "success"), testRequest(), standardLimits())
	if !errors.Is(err, context.Canceled) || result.Reply != nil {
		t.Fatal(result, err)
	}
	assertStopped(t, result)
}

func TestCallerCancellationWinsDuringCleanupCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := filepath.Join(t.TempDir(), "ready")
	spec := helperSpec(t, "barrier-success")
	spec.Env = append(spec.Env, "LVG_WORKER_READY_FILE="+ready)
	var calls atomic.Int32
	m := monitorFunc(func(sampleCtx context.Context, group ProcessGroup) (Sample, error) {
		if calls.Add(1) == 1 {
			if os.WriteFile(ready, []byte{1}, 0600) != nil {
				return Sample{}, ErrMonitor
			}
			<-sampleCtx.Done()
			return Sample{}, sampleCtx.Err()
		}
		// The cleanup query is separately bounded and must still prove stop,
		// but the late parent cancellation must suppress a successful reply.
		cancel()
		return Sample{}, nil
	})
	result, err := fakeRunner(t, Config{Monitor: m}).Run(ctx, spec, testRequest(), standardLimits())
	if !errors.Is(err, context.Canceled) || result.Reply != nil {
		t.Fatal(result, err)
	}
	assertStopped(t, result)
}

func TestMonitorBreachAndFailureFailClosed(t *testing.T) {
	for _, breach := range []bool{true, false} {
		t.Run(strconv.FormatBool(breach), func(t *testing.T) {
			var calls atomic.Int32
			m := monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) {
				if calls.Add(1) == 1 {
					if !breach {
						return Sample{}, errors.New("credential-secret")
					}
					v := int64(3_000_000_000)
					return Sample{Live: 1, RSSBytes: &v, Source: resource.SourceNativeFamilyRSS}, nil
				}
				return Sample{}, nil
			})
			result, err := fakeRunner(t, Config{Monitor: m}).Run(context.Background(), helperSpec(t, "wait"), testRequest(), standardLimits())
			want := ErrMonitor
			if breach {
				want = ErrLimitExceeded
			}
			if !errors.Is(err, want) {
				t.Fatal(result, err)
			}
			assertStopped(t, result)
			if breach && (result.Measurement.FamilyRSS == nil || result.Measurement.FamilyRSS.Bytes != 3_000_000_000) {
				t.Fatal(result.Measurement)
			}
			if breach && result.Measurement.FamilyRSS.Completeness != resource.MeasurementPartial {
				t.Fatal("aborted sampling marked complete", result.Measurement)
			}
			if !breach && result.Measurement.FamilyRSS != nil {
				t.Fatal(result.Measurement)
			}
		})
	}
}

func TestCleanupFamilyRSSBreachPreservedUntilProvenStop(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	spec := helperSpec(t, "barrier-success")
	spec.Env = append(spec.Env, "LVG_WORKER_READY_FILE="+ready)
	var calls atomic.Int32
	m := monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) {
		switch calls.Add(1) {
		case 1:
			v := int64(100)
			return Sample{Live: 1, RSSBytes: &v, Source: resource.SourceNativeFamilyRSS}, nil
		case 2:
			// The second observe callback proves the first sample was consumed.
			// Only now may the helper reply/exit and trigger cleanup.
			if os.WriteFile(ready, []byte{1}, 0600) != nil {
				return Sample{}, ErrMonitor
			}
			<-ctx.Done()
			return Sample{}, ctx.Err()
		case 3:
			v := int64(3_000_000_000)
			return Sample{Live: 1, RSSBytes: &v, Source: resource.SourceNativeFamilyRSS}, nil
		default:
			return Sample{}, nil
		}
	})
	limits := standardLimits()
	limits.SampleInterval = time.Millisecond
	result, err := fakeRunner(t, Config{Monitor: m}).Run(context.Background(), spec, testRequest(), limits)
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("cleanup breach discarded: error=%v peak=%+v", err, result.Measurement.FamilyRSS)
	}
	assertStopped(t, result)
	if result.Reply != nil || calls.Load() < 4 {
		t.Fatal("breach returned before full cleanup", result.Reply, calls.Load())
	}
	p := result.Measurement.FamilyRSS
	if p == nil || p.Bytes != 3_000_000_000 || p.Source != resource.SourceNativeFamilyRSS || p.Completeness != resource.MeasurementPartial {
		t.Fatal("cleanup evidence lost", p)
	}
}

func TestUncanceledObserveFailureAfterLeaderReaped(t *testing.T) {
	peak := int64(100)
	h := &CleanupHandle{
		group: ProcessGroup{ID: 12345}, waitDone: make(chan struct{}), monitorDone: make(chan struct{}), events: make(chan error, 1), familyPeak: &peak,
		monitor: monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) {
			return Sample{}, errors.New("credential-secret")
		}),
	}
	close(h.waitDone)
	h.observe(context.Background(), time.Hour, standardLimits().RAMBytes)
	if !h.monitorFailed {
		t.Fatal("uncanceled monitor failure discarded after leader reap")
	}
	select {
	case err := <-h.events:
		if !errors.Is(err, ErrMonitor) {
			t.Fatal(err)
		}
	default:
		t.Fatal("monitor failure not reported")
	}
	if m := h.measurement(true); m.FamilyRSS == nil || m.FamilyRSS.Completeness != resource.MeasurementPartial {
		t.Fatal("failed collection marked complete", m)
	}
}

func TestCanceledCleanupRSSIsNotEvidenceAndRetryStillJoins(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	spec := helperSpec(t, "barrier-success")
	spec.Env = append(spec.Env, "LVG_WORKER_READY_FILE="+ready)
	var calls atomic.Int32
	m := monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) {
		switch calls.Add(1) {
		case 1:
			v := int64(100)
			return Sample{Live: 1, RSSBytes: &v, Source: resource.SourceNativeFamilyRSS}, nil
		case 2:
			if os.WriteFile(ready, []byte{1}, 0600) != nil {
				return Sample{}, ErrMonitor
			}
			<-ctx.Done()
			return Sample{}, ctx.Err()
		case 3:
			<-ctx.Done()
			v := int64(3_000_000_000)
			return Sample{Live: 1, RSSBytes: &v, Source: resource.SourceNativeFamilyRSS}, nil
		default:
			return Sample{}, nil
		}
	})
	limits := standardLimits()
	limits.SampleInterval = time.Millisecond
	result, err := fakeRunner(t, Config{Monitor: m, CleanupTimeout: 100 * time.Millisecond}).Run(context.Background(), spec, testRequest(), limits)
	if !errors.Is(err, ErrCleanup) || result.Stopped || result.Cleanup == nil {
		t.Fatal(result, err)
	}
	await(t, result.Cleanup.proof.done)
	if p := result.Measurement.FamilyRSS; p == nil || p.Bytes != 100 || p.Completeness != resource.MeasurementPartial {
		t.Fatal("canceled callback fabricated evidence", p)
	}
	retry, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if stopped, err := result.Cleanup.RetryStop(retry); !stopped || err != nil {
		t.Fatal(stopped, err)
	}
	if p := result.Cleanup.measurement(true).FamilyRSS; p == nil || p.Bytes != 100 || p.Completeness != resource.MeasurementPartial {
		t.Fatal("retry consumed canceled observation", p)
	}
}

func TestProvenRetryRetainsBreachOutcome(t *testing.T) {
	h := &CleanupHandle{stopped: true, limitExceeded: true, gate: make(chan struct{}, 1)}
	h.gate <- struct{}{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for n := 0; n < 2; n++ {
		if stopped, err := h.RetryStop(ctx); !stopped || !errors.Is(err, ErrLimitExceeded) {
			t.Fatal("proven stop lost breach outcome", stopped, err)
		}
	}
}

func TestCanceledObserveFailureAfterLeaderReapedIsIgnored(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &CleanupHandle{
		waitDone: make(chan struct{}), monitorDone: make(chan struct{}), events: make(chan error, 1),
		monitor: monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) {
			cancel()
			return Sample{}, errors.New("credential-secret")
		}),
	}
	close(h.waitDone)
	h.observe(ctx, time.Hour, standardLimits().RAMBytes)
	if h.monitorFailed || len(h.events) != 0 {
		t.Fatal("intentionally canceled callback became monitor failure")
	}
}

func TestOSPeakBreachWhenSamplingMisses(t *testing.T) {
	limits := standardLimits()
	limits.RAMBytes = 1
	result, err := fakeRunner(t, Config{}).Run(context.Background(), helperSpec(t, "success"), testRequest(), limits)
	if !errors.Is(err, ErrLimitExceeded) || result.Measurement.FamilyRSS != nil || result.Measurement.ProcessPeakRSS == nil || result.Measurement.ProcessPeakRSS.Bytes <= 1 {
		t.Fatal(result, err)
	}
	assertStopped(t, result)
}

func await(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("barrier timed out")
	}
}

func TestFailedProofRetainsHandleAndRetryOwnsPendingQuery(t *testing.T) {
	blocked := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	m := monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) {
		switch calls.Add(1) {
		case 1:
			return Sample{}, ErrMonitor
		case 2:
			close(blocked)
			<-release
			return Sample{}, nil
		default:
			return Sample{}, nil
		}
	})
	r := fakeRunner(t, Config{Monitor: m, CleanupTimeout: 100 * time.Millisecond})
	result, err := r.Run(context.Background(), helperSpec(t, "wait"), testRequest(), standardLimits())
	if !errors.Is(err, ErrMonitor) || result.Stopped || result.Cleanup == nil {
		t.Fatal(result, err)
	}
	await(t, blocked)
	h := result.Cleanup
	h.mu.Lock()
	retainedIO := len(h.stdout) + len(h.stderr)
	h.mu.Unlock()
	if retainedIO != 0 {
		t.Fatal("quarantine retained raw I/O")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	stopped, err := h.RetryStop(ctx)
	cancel()
	if stopped || err == nil || calls.Load() != 2 {
		t.Fatal("retry abandoned pending query", stopped, err, calls.Load())
	}
	close(release)
	await(t, h.proof.done)
	ctx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stopped, err = h.RetryStop(ctx)
	if !stopped || err != nil {
		t.Fatal(stopped, err)
	}
	for _, done := range []chan struct{}{h.waitDone, h.stdinDone, h.stdoutDone, h.stderrDone, h.monitorDone, h.proof.done} {
		if !closed(done) {
			t.Fatal("unjoined task")
		}
	}
	if stopped, err = h.RetryStop(ctx); !stopped || err != nil {
		t.Fatal("retry not idempotent", stopped, err)
	}
}

func TestBlockedMonitorPreventsProofUntilJoined(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	m := monitorFunc(func(ctx context.Context, g ProcessGroup) (Sample, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return Sample{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := fakeRunner(t, Config{Monitor: m, CleanupTimeout: 100 * time.Millisecond})
	spec := helperSpec(t, "wait")
	type outcome struct {
		result RunResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() { result, err := r.Run(ctx, spec, testRequest(), standardLimits()); done <- outcome{result, err} }()
	await(t, entered)
	cancel()
	var out outcome
	select {
	case out = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bounded cleanup hung")
	}
	if !errors.Is(out.err, context.Canceled) || out.result.Stopped || out.result.Cleanup == nil {
		t.Fatal(out)
	}
	close(release)
	await(t, out.result.Cleanup.monitorDone)
	retry, stop := context.WithTimeout(context.Background(), 3*time.Second)
	defer stop()
	if proved, err := out.result.Cleanup.RetryStop(retry); !proved || err != nil {
		t.Fatal(proved, err)
	}
}

func TestNoChildFailurePathsAndValidation(t *testing.T) {
	r := fakeRunner(t, Config{})
	spec := helperSpec(t, "success")
	bad := spec
	bad.Exe = filepath.Join(t.TempDir(), "nonexistent")
	result, err := r.Run(context.Background(), bad, testRequest(), standardLimits())
	if !errors.Is(err, ErrStart) {
		t.Fatal(err)
	}
	assertStopped(t, result)
	if result.Measurement.ProcessPeakRSS != nil || result.Measurement.RunMs != nil {
		t.Fatal(result)
	}
	for _, s := range []CommandSpec{{Exe: "relative"}, {Exe: spec.Exe, Env: []string{"bad"}}, {Exe: spec.Exe, Argv: []string{"bad\x00arg"}}, {Exe: spec.Exe, CWD: "relative"}} {
		result, err = r.Run(context.Background(), s, testRequest(), standardLimits())
		if !errors.Is(err, ErrInvalidConfig) {
			t.Fatal(err)
		}
		assertStopped(t, result)
	}
	limits := standardLimits()
	limits.RAMBytes = 0
	result, err = r.Run(context.Background(), spec, testRequest(), limits)
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	assertStopped(t, result)
	result, err = r.Run(nil, spec, testRequest(), standardLimits())
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	assertStopped(t, result)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err = r.Run(ctx, spec, testRequest(), standardLimits())
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	assertStopped(t, result)
	request := testRequest()
	request.Payload = make([]byte, MaxStdinBytes+1)
	result, err = r.Run(context.Background(), spec, request, standardLimits())
	if !errors.Is(err, ErrInputLimit) {
		t.Fatal(err)
	}
	assertStopped(t, result)
	if validGroup(ProcessGroup{ID: syscall.Getpgrp()}) {
		t.Fatal("parent group admitted")
	}
	if err := killGroup(ProcessGroup{ID: syscall.Getpgrp()}); !errors.Is(err, ErrCleanup) {
		t.Fatal(err)
	}
}

func TestConcurrentRunnerAndBackwardClockUnknownTiming(t *testing.T) {
	r := fakeRunner(t, Config{})
	spec := helperSpec(t, "success")
	type outcome struct {
		result RunResult
		err    error
	}
	done := make(chan outcome, 4)
	for n := 0; n < 4; n++ {
		go func() {
			result, err := r.Run(context.Background(), spec, testRequest(), standardLimits())
			done <- outcome{result, err}
		}()
	}
	for n := 0; n < 4; n++ {
		select {
		case out := <-done:
			if out.err != nil {
				t.Fatal(out.err)
			}
			assertStopped(t, out.result)
		case <-time.After(5 * time.Second):
			t.Fatal("concurrent runner hung")
		}
	}
	var calls int
	clock := func() time.Time { calls++; return time.Unix(100-int64(calls), 0) }
	result, err := fakeRunner(t, Config{Clock: clock}).Run(context.Background(), spec, testRequest(), standardLimits())
	if err != nil {
		t.Fatal(err)
	}
	assertStopped(t, result)
	if result.Measurement.RunMs != nil || result.Measurement.TimingSource != "" || result.Measurement.TimingCompleteness != "" {
		t.Fatal("invented timing", result.Measurement)
	}
}

func nativeRunner(t *testing.T) *Runner {
	t.Helper()
	if _, err := exec.LookPath("ps"); err != nil {
		t.Skip("native ps system dependency unavailable")
	}
	r, err := NewRunner(Config{Monitor: NativeMonitor{QueryTimeout: time.Second}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNativeSuccessAndFamilyCleanupWithInheritedPipes(t *testing.T) {
	for _, role := range []string{"success", "descendant-exit", "descendant-wait"} {
		t.Run(role, func(t *testing.T) {
			r := nativeRunner(t)
			spec := helperSpec(t, role)
			pidFile := filepath.Join(t.TempDir(), "pid")
			spec.Env = append(spec.Env, "LVG_WORKER_PID_FILE="+pidFile)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			type outcome struct {
				result RunResult
				err    error
			}
			done := make(chan outcome, 1)
			go func() { result, err := r.Run(ctx, spec, testRequest(), standardLimits()); done <- outcome{result, err} }()
			if role == "descendant-wait" {
				waitFile(t, pidFile)
				cancel()
			}
			var out outcome
			select {
			case out = <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("inherited pipes hung")
			}
			if role == "descendant-wait" {
				if !errors.Is(out.err, context.Canceled) {
					t.Fatal(out.err)
				}
			} else if out.err != nil {
				t.Fatal(out.result, out.err)
			}
			assertStopped(t, out.result)
			if role != "success" {
				b, err := os.ReadFile(pidFile)
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(string(b))
				if err != nil {
					t.Fatal(err)
				}
				query, stop := context.WithTimeout(context.Background(), 2*time.Second)
				defer stop()
				// pgid is the helper leader's pid, not the descendant pid. The
				// descendant may remain as a kernel-managed zero-RSS zombie.
				// A whole-list query has an unambiguous zero-exit success; do
				// not mistake a target-only ps exit/timeout for PID absence.
				cmd := exec.CommandContext(query, "ps", "-axo", "pid=,stat=")
				cmd.WaitDelay = 100 * time.Millisecond
				capture := &queryBuffer{cancel: stop}
				diagnostics := &queryBuffer{cancel: stop}
				cmd.Stdout, cmd.Stderr = capture, diagnostics
				err = cmd.Run()
				b = capture.data
				if len(diagnostics.data) > 0 {
					err = ErrMonitor
				}
				if err = verifyDescendantStopped(query, b, err, pid); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

// A failed/canceled/malformed whole-list query proves nothing. Only a valid
// successful list containing no target, or a dead/zombie target, is proof.
func verifyDescendantStopped(ctx context.Context, output []byte, queryErr error, pid int) error {
	if ctx.Err() != nil || queryErr != nil || pid <= 0 {
		return ErrMonitor
	}
	seen := make(map[int]struct{})
	rows := 0
	stopped := true
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		fields := strings.Fields(string(line))
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return ErrMonitor
		}
		id, err := strconv.Atoi(fields[0])
		if err != nil || id <= 0 {
			return ErrMonitor
		}
		rows++
		// The whole-list query must be structurally usable, but status is
		// relevant only for the descendant PID; unrelated platform statuses
		// must not invalidate otherwise-positive absence evidence.
		if id != pid {
			continue
		}
		if _, duplicate := seen[id]; duplicate {
			return ErrMonitor
		}
		seen[id] = struct{}{}
		if len(fields[1]) == 0 || !strings.ContainsRune("RSDTIWUPEZX", rune(fields[1][0])) {
			return ErrMonitor
		}
		if fields[1][0] != 'Z' && fields[1][0] != 'X' {
			stopped = false
		}
	}
	if rows == 0 {
		return ErrMonitor
	}
	if !stopped {
		return ErrCleanup
	}
	return nil
}

func TestDescendantProofQueryFailsClosed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name   string
		ctx    context.Context
		output string
		err    error
	}{
		{"timeout", ctx, "", context.DeadlineExceeded},
		{"launch", context.Background(), "", os.ErrNotExist},
		{"unexpected-status", context.Background(), "", errors.New("unexpected utility exit")},
		{"malformed", context.Background(), "42 bad-state extra", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := verifyDescendantStopped(tc.ctx, []byte(tc.output), tc.err, 42); !errors.Is(err, ErrMonitor) {
				t.Fatal("unreliable query accepted as proof", err)
			}
		})
	}
}

func TestDescendantProofSuccessfulWholeList(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   error
	}{
		{"9 S\n", nil}, {"9 ?Es\n42 Z\n", nil}, {"9 S\n42 Z\n", nil}, {"42 X\n", nil}, {"42 T\n", ErrCleanup},
		{"42 R\n", ErrCleanup}, {"42 ?\n", ErrMonitor}, {"42 ?Es\n", ErrMonitor}, {"42 Z\n42 R\n", ErrMonitor}, {"\n", ErrMonitor}, {"not-pid S\n", ErrMonitor},
	} {
		if err := verifyDescendantStopped(context.Background(), []byte(tc.output), nil, 42); !errors.Is(err, tc.want) {
			t.Fatal("incorrect PID proof outcome", err, tc.want)
		}
	}
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-timer.C:
			t.Fatal("helper readiness timed out")
		case <-tick.C:
		}
	}
}
