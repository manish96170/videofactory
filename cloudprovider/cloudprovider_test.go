package cloudprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/provider"
)

type sourceFunc func(context.Context) (Credential, error)

func (f sourceFunc) Acquire(ctx context.Context) (Credential, error) { return f(ctx) }

type transportStub struct {
	submit   func(context.Context, Credential, SubmitRequest) (Reply, error)
	poll     func(context.Context, Credential, JobRef) (Reply, error)
	download func(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error)
}

func (s *transportStub) Submit(ctx context.Context, c Credential, r SubmitRequest) (Reply, error) {
	return s.submit(ctx, c, r)
}
func (s *transportStub) Poll(ctx context.Context, c Credential, r JobRef) (Reply, error) {
	return s.poll(ctx, c, r)
}
func (s *transportStub) Download(ctx context.Context, c Credential, r JobRef, a Artifact) (io.ReadCloser, error) {
	return s.download(ctx, c, r, a)
}

type cancelTransport struct {
	*transportStub
	cancel func(context.Context, Credential, JobRef) (Reply, error)
}

func (s *cancelTransport) Cancel(ctx context.Context, c Credential, r JobRef) (Reply, error) {
	return s.cancel(ctx, c, r)
}

type sinkStub struct {
	stage    *stageStub
	beginErr error
	begin    func(context.Context, Target)
	calls    int
}

func (s *sinkStub) Begin(ctx context.Context, target Target) (Staging, error) {
	s.calls++
	if s.begin != nil {
		s.begin(ctx, target)
	}
	return s.stage, s.beginErr
}

type stageStub struct {
	bytes.Buffer
	commits, aborts int
	commit          func(context.Context) error
	abort           func(context.Context) error
	write           func([]byte) (int, error)
}

func (s *stageStub) Write(b []byte) (int, error) {
	if s.write != nil {
		return s.write(b)
	}
	return s.Buffer.Write(b)
}
func (s *stageStub) Commit(ctx context.Context) error {
	s.commits++
	if s.commit != nil {
		return s.commit(ctx)
	}
	return nil
}
func (s *stageStub) Abort(ctx context.Context) error {
	s.aborts++
	s.Reset()
	if s.abort != nil {
		return s.abort(ctx)
	}
	return nil
}

type readerStub struct {
	io.Reader
	closes   int
	closeErr error
}

func (r *readerStub) Close() error { r.closes++; return r.closeErr }

func config() Config {
	latency := int64(1200)
	return Config{Capabilities: domain.ProviderCapabilities{ProviderID: "fixture-cloud", Tags: []string{"generation", "video"}, CostPerUnit: 0.02, CostUnit: "USD/second", EstimatedLatencyMs: &latency}, AdapterRevision: "r1", Policy: Policy{MaxAttempts: 3, MaxPolls: 5, MinInterval: time.Second, RetryDelay: 2 * time.Second, MaxDelay: time.Hour, AttemptTimeout: time.Minute, CleanupTimeout: time.Second, MaxDownloadBytes: 1024}}
}
func request() domain.GenerationRequest {
	return domain.GenerationRequest{ID: "request-1", ShotID: "shot-1", ProviderID: "fixture-cloud", Quality: domain.QualityDraft, Params: map[string]any{"prompt": "private prompt", "seed": "42"}}
}
func fixtureArtifact(data []byte) Artifact {
	sum := sha256.Sum256(data)
	return Artifact{ID: "media-1", Size: int64(len(data)), SHA256: hex.EncodeToString(sum[:])}
}
func queued(r SubmitRequest) Reply {
	return Reply{Ref: JobRef{ProviderID: "fixture-cloud", RequestID: r.RequestID, JobID: "job-1"}, Status: Queued}
}
func newAdapter(t *testing.T, c Config, transport Transport, source CredentialSource) *Adapter {
	t.Helper()
	d, err := NewDescriptor(c)
	if err != nil {
		t.Fatal(err)
	}
	if source == nil {
		source = sourceFunc(func(context.Context) (Credential, error) { return NewCredential("test-only-secret"), nil })
	}
	a, err := NewAdapter(d, source, transport)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func newJob(t *testing.T, a *Adapter) *Job {
	t.Helper()
	j, err := a.NewJob(request())
	if err != nil {
		t.Fatal(err)
	}
	return j
}

var epoch = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestDescriptorIsolationAndRegistry(t *testing.T) {
	c := config()
	d, err := NewDescriptor(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Capabilities.Tags[0] = "changed"
	*c.Capabilities.EstimatedLatencyMs = 9
	first, _ := d.Capabilities(context.Background())
	if first.Local || !first.Supports("generation") || *first.EstimatedLatencyMs != 1200 {
		t.Fatalf("changed: %+v", first)
	}
	first.Tags[0] = "changed-again"
	*first.EstimatedLatencyMs = 0
	second, _ := d.Capabilities(context.Background())
	if !second.Supports("generation") || *second.EstimatedLatencyMs != 1200 {
		t.Fatal("aliased result")
	}
	r := provider.NewRegistry()
	if err := r.Register(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	_, snap, err := r.Get(context.Background(), d.ID())
	if err != nil || snap.Health != domain.ProviderHealthUnknown {
		t.Fatalf("health inferred: %+v %v", snap, err)
	}
	if _, err := d.Capabilities(nil); !errors.Is(err, ErrRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Capabilities(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"local":              func(c *Config) { c.Capabilities.Local = true },
		"cost unknown":       func(c *Config) { c.Capabilities.CostUnit = "" },
		"cost nan":           func(c *Config) { c.Capabilities.CostPerUnit = math.NaN() },
		"latency unknown":    func(c *Config) { c.Capabilities.EstimatedLatencyMs = nil },
		"missing tag":        func(c *Config) { c.Capabilities.Tags = []string{"video"} },
		"duplicate tag":      func(c *Config) { c.Capabilities.Tags = []string{"generation", "generation"} },
		"unbounded attempts": func(c *Config) { c.Policy.MaxAttempts = 9 },
		"unbounded polls":    func(c *Config) { c.Policy.MaxPolls = 0 },
		"zero interval":      func(c *Config) { c.Policy.MinInterval = 0 },
		"short max delay":    func(c *Config) { c.Policy.MaxDelay = time.Nanosecond },
		"zero timeout":       func(c *Config) { c.Policy.AttemptTimeout = 0 },
		"zero cleanup":       func(c *Config) { c.Policy.CleanupTimeout = 0 },
		"unbounded download": func(c *Config) { c.Policy.MaxDownloadBytes = math.MaxInt64 },
	} {
		t.Run(name, func(t *testing.T) {
			c := config()
			mutate(&c)
			if _, err := NewDescriptor(c); !errors.Is(err, ErrConfig) {
				t.Fatal(err)
			}
		})
	}
	d, _ := NewDescriptor(config())
	var source sourceFunc
	if _, err := NewAdapter(d, source, &transportStub{}); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
	var transport *transportStub
	if _, err := NewAdapter(d, sourceFunc(func(context.Context) (Credential, error) { return Credential{}, nil }), transport); !errors.Is(err, ErrConfig) {
		t.Fatal(err)
	}
}

func TestSubmitPollDownloadAndOwnership(t *testing.T) {
	ctx := context.Background()
	data := []byte("verified fixture media")
	artifact := fixtureArtifact(data)
	stream := &readerStub{Reader: bytes.NewReader(data)}
	var payloads [][]byte
	polls := 0
	transport := &transportStub{
		submit: func(_ context.Context, cred Credential, r SubmitRequest) (Reply, error) {
			if cred.Value() != "test-only-secret" || r.Version != 1 {
				t.Fatal("runtime credential/protocol missing")
			}
			payloads = append(payloads, append([]byte(nil), r.Payload...))
			r.Payload[0] = '!'
			if len(payloads) == 1 {
				return Reply{}, &TransportError{Kind: ErrTransient, RetryAfter: 3 * time.Second}
			}
			return queued(r), nil
		},
		poll: func(_ context.Context, _ Credential, ref JobRef) (Reply, error) {
			polls++
			if polls == 1 {
				return Reply{Ref: ref, Status: Running, PollAfter: 4 * time.Second}, nil
			}
			return Reply{Ref: ref, Status: Succeeded, Artifact: &artifact}, nil
		},
		download: func(_ context.Context, _ Credential, ref JobRef, a Artifact) (io.ReadCloser, error) {
			if a != fixtureArtifact(data) || ref.JobID != "job-1" {
				t.Fatal("wrong download binding")
			}
			return stream, nil
		},
	}
	a := newAdapter(t, config(), transport, nil)
	req := request()
	j, err := a.NewJob(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Params["prompt"] = "mutated"
	if _, err = j.Submit(ctx, epoch); !errors.Is(err, ErrTransient) {
		t.Fatal(err)
	}
	if _, err = j.Submit(ctx, epoch.Add(time.Second)); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	s, err := j.Submit(ctx, epoch.Add(3*time.Second))
	if err != nil || s.Status != Queued {
		t.Fatalf("%+v %v", s, err)
	}
	if !bytes.Equal(payloads[0], payloads[1]) || !bytes.Contains(payloads[1], []byte("private prompt")) {
		t.Fatal("retry payload not immutable")
	}
	if _, err = j.Poll(ctx, epoch.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Poll(ctx, epoch.Add(7*time.Second)); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	if _, err = j.Poll(ctx, epoch.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	// The transport's old reply pointer cannot mutate accepted integrity facts.
	artifact.SHA256 = strings.Repeat("0", 64)
	stage := &stageStub{}
	sink := &sinkStub{stage: stage}
	result, err := j.Download(ctx, epoch.Add(9*time.Second), sink)
	if err != nil {
		t.Fatal(err)
	}
	if stage.commits != 1 || stage.aborts != 0 || stream.closes != 1 || !bytes.Equal(stage.Bytes(), data) {
		t.Fatalf("bad lifecycle: %+v %+v", stage, stream)
	}
	if result.Target.Artifact != fixtureArtifact(data) || result.Target.ShotID != "shot-1" || result.Quality != domain.QualityDraft || result.Provenance.Source != "configured" {
		t.Fatalf("bad result: %+v", result)
	}
	s = j.Snapshot()
	if !s.Downloaded || s.SubmitAttempts != 2 || s.PollAttempts != 2 || s.DownloadAttempts != 1 {
		t.Fatalf("bad status: %+v", s)
	}
	if _, err = j.Download(ctx, epoch.Add(10*time.Second), sink); !errors.Is(err, ErrState) {
		t.Fatal(err)
	}
	wire, _ := json.Marshal(struct {
		Status Snapshot
		Result DownloadResult
	}{s, result})
	if bytes.Contains(wire, []byte("private prompt")) || bytes.Contains(wire, []byte("test-only-secret")) {
		t.Fatal("sensitive output")
	}
}

func TestCredentialsAndSafeErrors(t *testing.T) {
	secret := "synthetic-secret-do-not-return"
	cred := NewCredential(secret)
	for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if strings.Contains(fmt.Sprintf(format, cred), secret) {
			t.Fatal("format leaked credential")
		}
	}
	if b, err := json.Marshal(cred); err == nil || bytes.Contains(b, []byte(secret)) || strings.Contains(err.Error(), secret) {
		t.Fatal("serialized credential")
	}
	calls := 0
	tr := &transportStub{submit: func(context.Context, Credential, SubmitRequest) (Reply, error) {
		calls++
		return Reply{}, errors.New(secret)
	}}
	a := newAdapter(t, config(), tr, sourceFunc(func(context.Context) (Credential, error) { return cred, errors.New(secret) }))
	j := newJob(t, a)
	if _, err := j.Submit(context.Background(), epoch); err != ErrAuth {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("transport called after credential error")
	}
	a = newAdapter(t, config(), tr, nil)
	j = newJob(t, a)
	if _, err := j.Submit(context.Background(), epoch); err != ErrDependency || strings.Contains(fmt.Sprintf("%+v", err), secret) {
		t.Fatal(err)
	}
	if _, err := j.Submit(context.Background(), epoch.Add(time.Hour)); err != ErrExhausted || calls != 1 {
		t.Fatalf("permanent retry: %d %v", calls, err)
	}
}

func TestRequestAdmission(t *testing.T) {
	a := newAdapter(t, config(), &transportStub{}, nil)
	for name, mutate := range map[string]func(*domain.GenerationRequest){
		"provider":      func(r *domain.GenerationRequest) { r.ProviderID = "other" },
		"quality":       func(r *domain.GenerationRequest) { r.Quality = "unknown" },
		"path id":       func(r *domain.GenerationRequest) { r.ID = "../secret" },
		"endpoint":      func(r *domain.GenerationRequest) { r.Params = map[string]any{"endpoint": "https://invalid.example"} },
		"credential":    func(r *domain.GenerationRequest) { r.Params = map[string]any{"prompt": NewCredential("secret")} },
		"oversize":      func(r *domain.GenerationRequest) { r.Params = map[string]any{"prompt": strings.Repeat("x", 65537)} },
		"wire oversize": func(r *domain.GenerationRequest) { r.Params = map[string]any{"prompt": strings.Repeat("\x00", 65536)} },
	} {
		t.Run(name, func(t *testing.T) {
			r := request()
			mutate(&r)
			if _, err := a.NewJob(r); err != ErrRequest {
				t.Fatal(err)
			}
		})
	}
}

func TestRetriesRateGateAndPollBudget(t *testing.T) {
	ctx := context.Background()
	calls := 0
	tr := &transportStub{submit: func(_ context.Context, _ Credential, r SubmitRequest) (Reply, error) {
		calls++
		return Reply{}, &TransportError{Kind: ErrRateLimited, RetryAfter: 10 * time.Second}
	}}
	a := newAdapter(t, config(), tr, nil)
	j := newJob(t, a)
	if _, err := j.Submit(ctx, epoch); err != ErrRateLimited {
		t.Fatal(err)
	}
	req := request()
	req.ID = "request-2"
	other, _ := a.NewJob(req)
	if _, err := other.Submit(ctx, epoch.Add(time.Second)); !errors.Is(err, ErrNotReady) {
		t.Fatal("rate gate not shared", err)
	}
	for i := 1; i < 3; i++ {
		if _, err := j.Submit(ctx, epoch.Add(time.Duration(i)*10*time.Second)); err != ErrRateLimited {
			t.Fatal(err)
		}
	}
	if _, err := j.Submit(ctx, epoch.Add(time.Hour)); err != ErrExhausted || calls != 3 {
		t.Fatalf("unbounded retries %d %v", calls, err)
	}
	tr = &transportStub{submit: func(_ context.Context, _ Credential, r SubmitRequest) (Reply, error) { return queued(r), nil }, poll: func(_ context.Context, _ Credential, r JobRef) (Reply, error) {
		return Reply{Ref: r, Status: Queued}, nil
	}}
	a = newAdapter(t, config(), tr, nil)
	j = newJob(t, a)
	if _, err := j.Submit(ctx, epoch); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 5; i++ {
		if _, err := j.Poll(ctx, epoch.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := j.Poll(ctx, epoch.Add(time.Hour)); err != ErrExhausted {
		t.Fatal(err)
	}
	if j.Snapshot().PollAttempts != 5 {
		t.Fatal(j.Snapshot())
	}
}

func TestPermanentPrecedenceAndRetryAfterBounds(t *testing.T) {
	for name, fixture := range map[string]struct {
		err  error
		want error
	}{
		"auth":           {errors.Join(&TransportError{Kind: ErrTransient}, fmt.Errorf("secret: %w", ErrAuth)), ErrAuth},
		"invalid":        {errors.Join(ErrTransient, ErrRequest), ErrRequest},
		"permanent":      {errors.Join(ErrRateLimited, ErrPermanent), ErrPermanent},
		"long retry":     {&TransportError{Kind: ErrRateLimited, RetryAfter: 2 * time.Hour}, ErrExhausted},
		"negative retry": {&TransportError{Kind: ErrTransient, RetryAfter: -time.Second}, ErrExhausted},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			tr := &transportStub{submit: func(context.Context, Credential, SubmitRequest) (Reply, error) { calls++; return Reply{}, fixture.err }}
			j := newJob(t, newAdapter(t, config(), tr, nil))
			if _, err := j.Submit(context.Background(), epoch); err != fixture.want {
				t.Fatal(err)
			}
			if _, err := j.Submit(context.Background(), epoch.Add(3*time.Hour)); err != ErrExhausted || calls != 1 {
				t.Fatal("retried permanent failure", err)
			}
		})
	}
}

func TestMalformedRepliesAndNoRegression(t *testing.T) {
	for name, mutate := range map[string]func(*Reply){
		"request":             func(r *Reply) { r.Ref.RequestID = "other" },
		"provider":            func(r *Reply) { r.Ref.ProviderID = "other" },
		"url":                 func(r *Reply) { r.Ref.JobID = "https://secret.example" },
		"unknown":             func(r *Reply) { r.Status = "unknown" },
		"no artifact":         func(r *Reply) { r.Status = Succeeded },
		"unexpected artifact": func(r *Reply) { r.Artifact = &Artifact{} },
		"invalid digest": func(r *Reply) {
			r.Status = Succeeded
			r.Artifact = &Artifact{ID: "asset", Size: 2, SHA256: strings.Repeat("z", 64)}
		},
		"oversize": func(r *Reply) {
			a := fixtureArtifact([]byte("media"))
			a.Size = 1025
			r.Status = Succeeded
			r.Artifact = &a
		},
		"invalid delay": func(r *Reply) { r.PollAfter = -time.Second },
	} {
		t.Run(name, func(t *testing.T) {
			tr := &transportStub{submit: func(_ context.Context, _ Credential, q SubmitRequest) (Reply, error) {
				r := queued(q)
				mutate(&r)
				return r, nil
			}}
			j := newJob(t, newAdapter(t, config(), tr, nil))
			s, err := j.Submit(context.Background(), epoch)
			if err != ErrResult || s.Status != New || s.Ref.JobID != "" {
				t.Fatalf("accepted malformed result %+v %v", s, err)
			}
		})
	}
	tr := &transportStub{submit: func(_ context.Context, _ Credential, r SubmitRequest) (Reply, error) {
		v := queued(r)
		v.Status = Running
		return v, nil
	}, poll: func(_ context.Context, _ Credential, r JobRef) (Reply, error) {
		return Reply{Ref: r, Status: Queued}, nil
	}}
	j := newJob(t, newAdapter(t, config(), tr, nil))
	_, _ = j.Submit(context.Background(), epoch)
	if s, err := j.Poll(context.Background(), epoch.Add(time.Second)); err != ErrResult || s.Status != Running {
		t.Fatalf("regressed: %+v %v", s, err)
	}
}

func TestCancellationAndLateSuccess(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	tr := &transportStub{submit: func(_ context.Context, _ Credential, r SubmitRequest) (Reply, error) { stop(); return queued(r), nil }}
	j := newJob(t, newAdapter(t, config(), tr, nil))
	if s, err := j.Submit(ctx, epoch); err != context.Canceled || s.Status != New {
		t.Fatalf("late success: %+v %v", s, err)
	}
	if _, err := j.Submit(nil, epoch); err != ErrRequest {
		t.Fatal(err)
	}
	tr = &transportStub{submit: func(_ context.Context, _ Credential, r SubmitRequest) (Reply, error) { return queued(r), nil }}
	j = newJob(t, newAdapter(t, config(), tr, nil))
	_, _ = j.Submit(context.Background(), epoch)
	if _, err := j.Cancel(context.Background(), epoch.Add(time.Second)); err != ErrUnsupported {
		t.Fatal(err)
	}
	ct := &cancelTransport{transportStub: tr, cancel: func(_ context.Context, _ Credential, r JobRef) (Reply, error) {
		return Reply{Ref: r, Status: Canceled}, nil
	}}
	j = newJob(t, newAdapter(t, config(), ct, nil))
	_, _ = j.Submit(context.Background(), epoch)
	if s, err := j.Cancel(context.Background(), epoch.Add(time.Second)); err != nil || s.Status != Canceled {
		t.Fatalf("cancel: %+v %v", s, err)
	}
	if _, err := j.Poll(context.Background(), epoch.Add(2*time.Second)); err != ErrState {
		t.Fatal(err)
	}
}

func TestConcurrentJobCallsAndCallbackSnapshot(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var j *Job
	tr := &transportStub{submit: func(_ context.Context, _ Credential, r SubmitRequest) (Reply, error) {
		if j.Snapshot().Status != New {
			t.Error("unexpected callback snapshot")
		}
		close(entered)
		<-release
		return queued(r), nil
	}}
	j = newJob(t, newAdapter(t, config(), tr, nil))
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := j.Submit(context.Background(), epoch); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	if _, err := j.Submit(context.Background(), epoch.Add(time.Second)); err != ErrBusy {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
	if j.Snapshot().SubmitAttempts != 1 {
		t.Fatal(j.Snapshot())
	}
}
