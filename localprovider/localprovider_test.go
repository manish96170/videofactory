package localprovider_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/audio"
	"github.com/manish96170/videofactory/decision"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/localprovider"
	"github.com/manish96170/videofactory/model"
	"github.com/manish96170/videofactory/provider"
	"github.com/manish96170/videofactory/resource"
	"github.com/manish96170/videofactory/worker"
)

type runnerFunc func(context.Context, worker.CommandSpec, worker.Request, worker.Limits) (worker.RunResult, error)

func (f runnerFunc) Run(ctx context.Context, c worker.CommandSpec, r worker.Request, l worker.Limits) (worker.RunResult, error) {
	return f(ctx, c, r, l)
}

type decisionFunc func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error)

func (f decisionFunc) Decide(ctx context.Context, r domain.DecisionRequest) (domain.DecisionResult, error) {
	return f(ctx, r)
}

type asrFunc func(context.Context, audio.TranscriptionRequest) (audio.TranscriptionResult, error)

func (f asrFunc) Transcribe(ctx context.Context, r audio.TranscriptionRequest) (audio.TranscriptionResult, error) {
	return f(ctx, r)
}

func config() localprovider.Config {
	return localprovider.Config{AdapterRevision: "a1", ProfileID: "small", Model: domain.ModelCapabilities{
		ProviderID: "fixture-local", ModelID: "fixture-model", Local: true,
		Tags: []string{localprovider.GenerationTag, localprovider.DecisionTag, localprovider.ASRTag},
		Requirements: &domain.ModelRequirements{Version: 1, Revision: "m1", ExecutionProfiles: []domain.ExecutionProfile{{
			ID: "small", Revision: "p1", Trusted: true, MemoryKnown: true,
			LoadRAMBytes: 128_000_000, ResidentRAMBytes: 64_000_000, WorkspaceRAMBytes: 64_000_000,
			CPU: 1, Backend: domain.BackendNone,
			Envelope: domain.WorkloadEnvelope{Precisions: []string{"fp16"}, Qualities: []string{"draft", "final"}, Offloads: []string{"none"}, Width: domain.IntRange{Min: 16, Max: 32}, Height: domain.IntRange{Min: 16, Max: 32}, Frames: domain.IntRange{Min: 1, Max: 2}},
		}}},
	}}
}

func descriptor(t *testing.T) *localprovider.Descriptor {
	t.Helper()
	d, err := localprovider.NewDescriptor(config())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func generationConfig() localprovider.GenerationConfig {
	return localprovider.GenerationConfig{
		// This path is only passed to an in-memory fixture. No executable exists.
		Command: worker.CommandSpec{Exe: "/fixture/runtime", Argv: []string{"fixture-only"}, Env: []string{"MODE=fixture"}},
		Limits:  worker.Limits{RAMBytes: 160_000_000},
		Workloads: map[domain.Quality]domain.Workload{
			domain.QualityDraft: {Precision: "fp16", Width: 16, Height: 16, Frames: 1, Quality: "draft", Offload: "none"},
			domain.QualityFinal: {Precision: "fp16", Width: 32, Height: 32, Frames: 2, Quality: "final", Offload: "none"},
		},
	}
}

func generationRequest() domain.GenerationRequest {
	return domain.GenerationRequest{ID: "request-1", ShotID: "shot-1", ProviderID: "fixture-local", Quality: domain.QualityDraft, Params: map[string]any{"prompt": "fixture"}}
}

func success(r worker.Request) worker.RunResult {
	raw, _ := json.Marshal(domain.GenerationResult{ID: "result-1", RequestID: r.Header.RequestID, AssetID: "asset-1", Seed: "42", DurationMs: 999, PeakMemoryMB: ptr(999.0), Metadata: map[string]any{"providerId": "spoofed", "private": "discard"}})
	return worker.RunResult{Stopped: true, Reply: &worker.Reply{Header: r.Header, Status: "ok", Payload: raw}}
}

func ptr[T any](v T) *T { return &v }

func TestDescriptorDeclarationsAndRegistry(t *testing.T) {
	c := config()
	c.Model.Requirements.ExecutionProfiles[0].GPU = 1
	c.Model.Requirements.ExecutionProfiles[0].Backend = domain.BackendMetal
	d, err := localprovider.NewDescriptor(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Model.Tags[0] = "mutated"
	c.Model.Requirements.ExecutionProfiles[0].Envelope.Precisions[0] = "mutated"
	caps, err := d.Capabilities(context.Background())
	if err != nil || !caps.Local || !caps.Hardware.RequiresGPU || caps.Hardware.MinMemoryGB != .128 || !caps.Supports(localprovider.GenerationTag) {
		t.Fatalf("caps=%+v err=%v", caps, err)
	}
	caps.Tags[0] = "changed"
	caps.Hardware.MinMemoryGB = 99
	modelCaps := d.ModelCapabilities()
	modelCaps.Requirements.ExecutionProfiles[0].Envelope.Qualities[0] = "changed"
	w := generationConfig().Workloads[domain.QualityDraft]
	spec, err := d.LoadSpec(w)
	if err != nil || spec.Profile.Backend != domain.BackendMetal || spec.Key.Revision != "m1" {
		t.Fatalf("spec=%+v err=%v", spec, err)
	}
	spec.Profile.Envelope.Precisions[0] = "changed"
	if _, err := d.LoadSpec(w); err != nil {
		t.Fatal("load spec aliased:", err)
	}
	registry := provider.NewRegistry()
	if err := registry.Register(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	_, snapshot, err := registry.Get(context.Background(), d.ID())
	if err != nil || snapshot.Health != domain.ProviderHealthUnknown || snapshot.Capabilities.Hardware.MinMemoryGB != .128 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	if d.Provenance().Source != "configured" || d.Provenance().Backend != domain.BackendMetal {
		t.Fatal(d.Provenance())
	}
	w.Width = 100
	if _, err := d.LoadSpec(w); !errors.Is(err, localprovider.ErrUnsupported) {
		t.Fatal(err)
	}
	if _, err := d.Capabilities(nil); !errors.Is(err, localprovider.ErrRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.Capabilities(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInvalidDescriptorsAndDependencies(t *testing.T) {
	for name, change := range map[string]func(*localprovider.Config){
		"remote":          func(c *localprovider.Config) { c.Model.Local = false },
		"unknown":         func(c *localprovider.Config) { c.Model.Requirements = nil },
		"untrusted":       func(c *localprovider.Config) { c.Model.Requirements.ExecutionProfiles[0].Trusted = false },
		"unbounded":       func(c *localprovider.Config) { c.Model.Requirements.ExecutionProfiles[0].MemoryKnown = false },
		"bad backend":     func(c *localprovider.Config) { c.Model.Requirements.ExecutionProfiles[0].Backend = domain.BackendMetal },
		"missing profile": func(c *localprovider.Config) { c.ProfileID = "missing" },
		"unsafe identity": func(c *localprovider.Config) { c.Model.ModelID = "/private/model" },
		"duplicate tags":  func(c *localprovider.Config) { c.Model.Tags = []string{"asr", "asr"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := config()
			change(&c)
			if _, err := localprovider.NewDescriptor(c); !errors.Is(err, localprovider.ErrConfig) {
				t.Fatal(err)
			}
		})
	}
	d := descriptor(t)
	if _, err := localprovider.NewGenerator(d, generationConfig(), runnerFunc(nil)); !errors.Is(err, localprovider.ErrConfig) {
		t.Fatal(err)
	}
	if _, err := localprovider.NewDecision(d, []string{"classify"}, decisionFunc(nil)); !errors.Is(err, localprovider.ErrConfig) {
		t.Fatal(err)
	}
	if _, err := localprovider.NewASR(d, asrFunc(nil)); !errors.Is(err, localprovider.ErrConfig) {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*localprovider.GenerationConfig){
		"relative executable": func(c *localprovider.GenerationConfig) { c.Command.Exe = "runtime" },
		"nul argv":            func(c *localprovider.GenerationConfig) { c.Command.Argv[0] = "bad\x00" },
		"environment":         func(c *localprovider.GenerationConfig) { c.Command.Env[0] = "bad" },
		"low limit":           func(c *localprovider.GenerationConfig) { c.Limits.RAMBytes = 1 },
		"negative timeout":    func(c *localprovider.GenerationConfig) { c.Limits.Timeout = -1 },
		"bad workload": func(c *localprovider.GenerationConfig) {
			w := c.Workloads[domain.QualityDraft]
			w.Width = 100
			c.Workloads[domain.QualityDraft] = w
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := generationConfig()
			change(&c)
			_, err := localprovider.NewGenerator(d, c, runnerFunc(func(context.Context, worker.CommandSpec, worker.Request, worker.Limits) (worker.RunResult, error) {
				t.Fatal("called at construction")
				return worker.RunResult{}, nil
			}))
			if !errors.Is(err, localprovider.ErrConfig) {
				t.Fatal(err)
			}
		})
	}
}

func TestGenerationQualityOwnershipAndProvenance(t *testing.T) {
	c := generationConfig()
	var qualities []domain.Quality
	runMs := int64(12)
	runner := runnerFunc(func(_ context.Context, command worker.CommandSpec, r worker.Request, limits worker.Limits) (worker.RunResult, error) {
		if command.Exe != "/fixture/runtime" || command.Argv[0] != "fixture-only" || command.Env[0] != "MODE=fixture" || limits.RAMBytes != 160_000_000 {
			t.Fatalf("runtime changed: %+v %+v", command, limits)
		}
		command.Argv[0] = "mutated by worker"
		command.Env[0] = "MUTATED=worker"
		req, err := worker.DecodeGenerationRequest(r.Payload)
		if err != nil {
			t.Fatal(err)
		}
		qualities = append(qualities, req.Quality)
		width := float64(16)
		if req.Quality == domain.QualityFinal {
			width = 32
		}
		if req.Params["workload"].(map[string]any)["width"] != width {
			t.Fatal(req.Params)
		}
		result := success(r)
		result.Measurement = resource.Measurement{RunMs: &runMs, TimingSource: resource.SourceParentClock, TimingCompleteness: resource.MeasurementComplete}
		return result, nil
	})
	g, err := localprovider.NewGenerator(descriptor(t), c, runner)
	if err != nil {
		t.Fatal(err)
	}
	c.Command.Argv[0] = "mutated caller"
	c.Command.Env[0] = "MUTATED=caller"
	delete(c.Workloads, domain.QualityDraft)
	req := generationRequest()
	for _, q := range []domain.Quality{domain.QualityDraft, domain.QualityFinal, domain.QualityDraft} {
		req.Quality = q
		out, err := g.Generate(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		if out.Result.DurationMs != 12 || out.Result.PeakMemoryMB != nil || out.Result.Metadata["providerId"] != g.ID() || out.Result.Metadata["private"] != nil || out.Result.Metadata["quality"] != string(q) || out.Lifecycle.Reply != nil {
			t.Fatalf("out=%+v", out)
		}
		*out.Lifecycle.Measurement.RunMs = 400
		if runMs != 12 {
			t.Fatal("measurement alias")
		}
		out.Result.Metadata["quality"] = "changed"
	}
	if !reflect.DeepEqual(qualities, []domain.Quality{domain.QualityDraft, domain.QualityFinal, domain.QualityDraft}) || len(req.Params) != 1 {
		t.Fatal(qualities, req.Params)
	}
}

func TestGenerationRejectsInputBeforeCallback(t *testing.T) {
	g, err := localprovider.NewGenerator(descriptor(t), generationConfig(), runnerFunc(func(context.Context, worker.CommandSpec, worker.Request, worker.Limits) (worker.RunResult, error) {
		t.Fatal("unexpected callback")
		return worker.RunResult{}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*domain.GenerationRequest){
		"provider":          func(r *domain.GenerationRequest) { r.ProviderID = "another" },
		"quality":           func(r *domain.GenerationRequest) { r.Quality = "preview" },
		"command injection": func(r *domain.GenerationRequest) { r.Params["argv"] = []any{"evil"} },
		"workload override": func(r *domain.GenerationRequest) { r.Params["workload"] = map[string]any{"width": 8192} },
		"non string":        func(r *domain.GenerationRequest) { r.Params["prompt"] = 42 },
		"oversized":         func(r *domain.GenerationRequest) { r.Params["prompt"] = strings.Repeat("a", worker.MaxStdinBytes) },
	} {
		t.Run(name, func(t *testing.T) {
			r := generationRequest()
			change(&r)
			out, err := g.Generate(context.Background(), r)
			if err == nil || out.Result.ID != "" || !out.Lifecycle.Stopped {
				t.Fatal(out, err)
			}
		})
	}
	if _, err := g.Generate(nil, generationRequest()); !errors.Is(err, localprovider.ErrRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := g.Generate(ctx, generationRequest()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestGenerationFailuresAndCleanupHandoff(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*worker.RunResult)
		err    error
		want   error
	}{
		{"callback", func(*worker.RunResult) {}, errors.New("PRIVATE dependency detail"), localprovider.ErrDependency},
		{"monitor", func(*worker.RunResult) {}, errors.Join(worker.ErrMonitor, errors.New("PRIVATE")), worker.ErrMonitor},
		{"cleanup", func(r *worker.RunResult) { r.Stopped = false; r.Cleanup = &worker.CleanupHandle{} }, errors.New("PRIVATE"), localprovider.ErrCleanup},
		{"unproven", func(r *worker.RunResult) { r.Stopped = false }, nil, localprovider.ErrCleanup},
		{"contradictory cleanup", func(r *worker.RunResult) { r.Cleanup = &worker.CleanupHandle{} }, nil, localprovider.ErrCleanup},
		{"header", func(r *worker.RunResult) { r.Reply.Header.ModelRevision = "wrong" }, nil, localprovider.ErrResult},
		{"error payload", func(r *worker.RunResult) { r.Reply.Status = "error" }, nil, localprovider.ErrResult},
		{"correlation", func(r *worker.RunResult) { r.Reply.Payload = json.RawMessage(`{"id":"result-1","requestId":"other"}`) }, nil, localprovider.ErrResult},
		{"oversized reply", func(r *worker.RunResult) {
			r.Reply.Payload = json.RawMessage(strings.Repeat(" ", worker.MaxStdoutBytes+1))
		}, nil, localprovider.ErrResult},
		{"negative measurement", func(r *worker.RunResult) { r.Measurement.RunMs = ptr(int64(-1)) }, nil, localprovider.ErrResult},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var cleanup *worker.CleanupHandle
			g, err := localprovider.NewGenerator(descriptor(t), generationConfig(), runnerFunc(func(_ context.Context, _ worker.CommandSpec, r worker.Request, _ worker.Limits) (worker.RunResult, error) {
				out := success(r)
				tc.change(&out)
				cleanup = out.Cleanup
				return out, tc.err
			}))
			if err != nil {
				t.Fatal(err)
			}
			out, err := g.Generate(context.Background(), generationRequest())
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "PRIVATE") || out.Result.ID != "" || out.Lifecycle.Reply != nil || out.Lifecycle.Cleanup != cleanup {
				t.Fatalf("out=%+v err=%v", out, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cleanup := &worker.CleanupHandle{}
	g, err := localprovider.NewGenerator(descriptor(t), generationConfig(), runnerFunc(func(_ context.Context, _ worker.CommandSpec, r worker.Request, _ worker.Limits) (worker.RunResult, error) {
		cancel()
		out := success(r)
		out.Stopped = false
		out.Cleanup = cleanup
		return out, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Generate(ctx, generationRequest())
	if !errors.Is(err, context.Canceled) || out.Result.ID != "" || out.Lifecycle.Cleanup != cleanup || out.Lifecycle.Stopped {
		t.Fatal(out, err)
	}
}

func TestDecisionBoundary(t *testing.T) {
	input := map[string]any{"nested": map[string]any{"value": "original"}}
	d, err := localprovider.NewDecision(descriptor(t), []string{"classify"}, decisionFunc(func(_ context.Context, r domain.DecisionRequest) (domain.DecisionResult, error) {
		r.Input["nested"].(map[string]any)["value"] = "changed"
		return domain.DecisionResult{Decision: domain.DecisionNo, Score: .99, ReasonCode: "MATCH"}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	r := domain.DecisionRequest{ID: "d1", Kind: "classify", Input: input}
	result, err := d.Decide(context.Background(), r)
	if err != nil || result.Provider != d.ID() || result.Decision != domain.DecisionNo || input["nested"].(map[string]any)["value"] != "original" {
		t.Fatal(result, err, input)
	}
	registry := provider.NewRegistry()
	if err := registry.Register(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Decide(nil, r); !errors.Is(err, localprovider.ErrRequest) {
		t.Fatal(err)
	}
	r.Kind = "unknown"
	if _, err := d.Decide(context.Background(), r); !errors.Is(err, decision.ErrUnsupportedKind) {
		t.Fatal(err)
	}
	r.Kind = "classify"
	r.Input["cycle"] = r.Input
	if _, err := d.Decide(context.Background(), r); !errors.Is(err, decision.ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestDecisionFailureMapping(t *testing.T) {
	for _, tc := range []struct {
		name      string
		result    domain.DecisionResult
		err, want error
	}{
		{"nan", domain.DecisionResult{Decision: domain.DecisionYes, Score: math.NaN()}, nil, decision.ErrMalformedResult},
		{"identity", domain.DecisionResult{Decision: domain.DecisionYes, Score: 1, Provider: "other"}, nil, decision.ErrMalformedResult},
		{"verdict", domain.DecisionResult{Decision: "invalid", Score: 1}, nil, decision.ErrMalformedResult},
		{"transient", domain.DecisionResult{}, errors.Join(errors.New("PRIVATE"), decision.ErrTransient), decision.ErrTransient},
		{"permanent wins", domain.DecisionResult{}, errors.Join(decision.ErrMalformedResult, decision.ErrTransient), decision.ErrMalformedResult},
		{"worker permanent wins", domain.DecisionResult{}, errors.Join(worker.ErrProtocol, decision.ErrTransient), worker.ErrProtocol},
		{"error payload", domain.DecisionResult{Decision: domain.DecisionYes, Score: 1}, errors.New("PRIVATE"), localprovider.ErrDependency},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := localprovider.NewDecision(descriptor(t), []string{"classify"}, decisionFunc(func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) { return tc.result, tc.err }))
			if err != nil {
				t.Fatal(err)
			}
			result, err := d.Decide(context.Background(), domain.DecisionRequest{ID: "d1", Kind: "classify"})
			if !errors.Is(err, tc.want) || strings.Contains(err.Error(), "PRIVATE") || result != (domain.DecisionResult{}) || (tc.name == "permanent wins" && errors.Is(err, decision.ErrTransient)) {
				t.Fatal(result, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	d, err := localprovider.NewDecision(descriptor(t), []string{"classify"}, decisionFunc(func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
		cancel()
		return domain.DecisionResult{Decision: domain.DecisionYes, Score: 1}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := d.Decide(ctx, domain.DecisionRequest{ID: "d1", Kind: "classify"})
	if !errors.Is(err, context.Canceled) || result != (domain.DecisionResult{}) {
		t.Fatal(result, err)
	}
}

func transcriptionRequest() audio.TranscriptionRequest {
	return audio.TranscriptionRequest{ID: "t1", Asset: domain.AudioAsset{ID: "audio-1", ShotID: "shot-1", URI: "fixture.wav", Codec: "wav", Duration: time.Second, SampleRateHz: 16000, Channels: 1, Metadata: map[string]any{"nested": map[string]any{"key": "original"}}}}
}

func TestASRValidationOwnershipAndCancellation(t *testing.T) {
	req := transcriptionRequest()
	words := []domain.WordTiming{{AudioAssetID: "audio-1", Word: "fixture", End: time.Second}}
	a, err := localprovider.NewASR(descriptor(t), asrFunc(func(_ context.Context, r audio.TranscriptionRequest) (audio.TranscriptionResult, error) {
		r.Asset.Metadata["nested"].(map[string]any)["key"] = "changed"
		return audio.TranscriptionResult{Words: words}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := a.Transcribe(context.Background(), req)
	if err != nil || req.Asset.Metadata["nested"].(map[string]any)["key"] != "original" {
		t.Fatal(result, err)
	}
	result.Words[0].Word = "changed"
	if words[0].Word != "fixture" {
		t.Fatal("output alias")
	}
	for _, tc := range []struct {
		name   string
		result audio.TranscriptionResult
		err    error
	}{
		{"wrong asset", audio.TranscriptionResult{Words: []domain.WordTiming{{AudioAssetID: "other", Word: "x", End: time.Second}}}, nil},
		{"out of range", audio.TranscriptionResult{Words: []domain.WordTiming{{AudioAssetID: "audio-1", Word: "x", End: 2 * time.Second}}}, nil},
		{"error payload", audio.TranscriptionResult{Words: words}, errors.New("PRIVATE")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := localprovider.NewASR(descriptor(t), asrFunc(func(context.Context, audio.TranscriptionRequest) (audio.TranscriptionResult, error) {
				return tc.result, tc.err
			}))
			if err != nil {
				t.Fatal(err)
			}
			result, err := a.Transcribe(context.Background(), req)
			if !errors.Is(err, audio.ErrDependency) || len(result.Words) != 0 || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal(result, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	a, err = localprovider.NewASR(descriptor(t), asrFunc(func(context.Context, audio.TranscriptionRequest) (audio.TranscriptionResult, error) {
		cancel()
		return audio.TranscriptionResult{Words: words}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err = a.Transcribe(ctx, req)
	if !errors.Is(err, context.Canceled) || len(result.Words) != 0 {
		t.Fatal(result, err)
	}
	if _, err := a.Transcribe(nil, req); !errors.Is(err, localprovider.ErrRequest) {
		t.Fatal(err)
	}
}

func TestConcurrentDiscoveryAndGeneration(t *testing.T) {
	g, err := localprovider.NewGenerator(descriptor(t), generationConfig(), runnerFunc(func(_ context.Context, _ worker.CommandSpec, r worker.Request, _ worker.Limits) (worker.RunResult, error) {
		return success(r), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			caps, err := g.Capabilities(context.Background())
			if err != nil {
				t.Error(err)
				return
			}
			caps.Tags[0] = "mutated"
			out, err := g.Generate(context.Background(), generationRequest())
			if err != nil {
				t.Error(err)
				return
			}
			out.Result.Metadata["providerId"] = "mutated"
		}()
	}
	wg.Wait()
}

func TestCooperativeTimeoutAndTrustedMeasurements(t *testing.T) {
	c := generationConfig()
	c.Limits.Timeout = time.Millisecond
	g, err := localprovider.NewGenerator(descriptor(t), c, runnerFunc(func(ctx context.Context, _ worker.CommandSpec, r worker.Request, _ worker.Limits) (worker.RunResult, error) {
		<-ctx.Done()
		return success(r), nil // A late success must still be discarded.
	}))
	if err != nil {
		t.Fatal(err)
	}
	out, err := g.Generate(context.Background(), generationRequest())
	if !errors.Is(err, context.DeadlineExceeded) || out.Result.ID != "" || out.Lifecycle.Reply != nil {
		t.Fatal(out, err)
	}
	g, err = localprovider.NewGenerator(descriptor(t), generationConfig(), runnerFunc(func(_ context.Context, _ worker.CommandSpec, r worker.Request, _ worker.Limits) (worker.RunResult, error) {
		out := success(r)
		out.Measurement.FamilyRSS = &resource.MemoryPeak{Bytes: 2_000_000, Source: resource.SourceNativeFamilyRSS, Completeness: resource.MeasurementPartial}
		out.Measurement.ProcessPeakRSS = &resource.MemoryPeak{Bytes: 1_000_000, Source: resource.SourceOSProcessPeak, Completeness: resource.MeasurementComplete}
		return out, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	out, err = g.Generate(context.Background(), generationRequest())
	if err != nil || out.Result.DurationMs != 0 || out.Result.PeakMemoryMB == nil || *out.Result.PeakMemoryMB != 2 || out.Lifecycle.Measurement.FamilyRSS.Completeness != resource.MeasurementPartial {
		t.Fatal(out, err)
	}
}

type fixtureLoader struct {
	key            model.Key
	loads, unloads int
}

func (l *fixtureLoader) Identity() model.Key                        { return l.key }
func (l *fixtureLoader) Load(context.Context, model.LoadSpec) error { l.loads++; return nil }
func (l *fixtureLoader) Unload(context.Context) (bool, error)       { l.unloads++; return true, nil }

func TestModelAndResourceLifecycleRemainCallerOwned(t *testing.T) {
	ctx := context.Background()
	d := descriptor(t)
	spec, err := d.LoadSpec(generationConfig().Workloads[domain.QualityDraft])
	if err != nil {
		t.Fatal(err)
	}
	budget, err := resource.NewManager(resource.ReferenceConfig(4, 2, resource.AcceleratorFacts{Known: true, Backend: domain.BackendNone}, 0, "fixture-hardware"))
	if err != nil {
		t.Fatal(err)
	}
	history, err := resource.NewHistory(0)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := model.NewManager(budget, history, model.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	loader := &fixtureLoader{key: spec.Key}
	if err := mgr.Register(ctx, d.ModelCapabilities(), loader); err != nil {
		t.Fatal(err)
	}
	g, err := localprovider.NewGenerator(d, generationConfig(), runnerFunc(func(_ context.Context, _ worker.CommandSpec, r worker.Request, _ worker.Limits) (worker.RunResult, error) {
		if budget.Snapshot().Used.Slots != 1 || loader.loads != 1 {
			t.Fatal("missing preexisting admission")
		}
		return success(r), nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if loader.loads != 0 || budget.Snapshot().Used.Slots != 0 {
		t.Fatal("constructor acquired resources")
	}
	use, err := mgr.Acquire(ctx, spec.Key, spec.Profile.ID, spec.Workload)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Generate(ctx, generationRequest()); err != nil {
		t.Fatal(err)
	}
	if loader.unloads != 0 || budget.Snapshot().Used.Slots != 1 {
		t.Fatal("adapter changed lifecycle")
	}
	if err := use.Release(); err != nil {
		t.Fatal(err)
	}
	if budget.Snapshot().Used.Slots != 1 {
		t.Fatal("use release refunded model")
	}
	if err := mgr.Unload(ctx, spec.Key); err != nil {
		t.Fatal(err)
	}
	if loader.unloads != 1 || budget.Snapshot().Used.Slots != 0 {
		t.Fatal("explicit unload failed")
	}
}
