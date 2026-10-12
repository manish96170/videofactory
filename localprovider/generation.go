package localprovider

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/model"
	"github.com/manish96170/videofactory/resource"
	"github.com/manish96170/videofactory/worker"
)

// Runner is the existing worker.Runner seam. Injected implementations must be
// cooperative and concurrency-safe and must not retain/mutate arguments after
// return. Phase 14 uses deterministic fixtures; there is no default runtime.
type Runner interface {
	Run(context.Context, worker.CommandSpec, worker.Request, worker.Limits) (worker.RunResult, error)
}

type GenerationConfig struct {
	Command worker.CommandSpec
	Limits  worker.Limits
	// Exact workloads come from trusted configuration, never Params. Both draft
	// and final use the same protocol; quality implies no invented resolution.
	Workloads map[domain.Quality]domain.Workload
}

type Generator struct {
	*descriptor
	runner Runner
	config GenerationConfig
}

func NewGenerator(d *Descriptor, c GenerationConfig, runner Runner) (*Generator, error) {
	if d == nil || nilLike(runner) || !d.caps.Supports(GenerationTag) || !validCommand(c.Command) || c.Limits.RAMBytes <= 0 || c.Limits.Timeout < 0 || c.Limits.SampleInterval < 0 || len(c.Workloads) == 0 {
		return nil, ErrConfig
	}
	peak := max(d.profile.LoadRAMBytes, d.profile.ResidentRAMBytes+d.profile.WorkspaceRAMBytes)
	if c.Limits.RAMBytes < peak || float64(c.Limits.RAMBytes)/float64(resource.GB) < d.model.MinMemoryGB {
		return nil, ErrConfig
	}
	workloads := make(map[domain.Quality]domain.Workload, len(c.Workloads))
	for q, w := range c.Workloads {
		if (q != domain.QualityDraft && q != domain.QualityFinal) || w.Quality != string(q) {
			return nil, ErrConfig
		}
		if _, err := d.LoadSpec(w); err != nil {
			return nil, ErrConfig
		}
		workloads[q] = w
	}
	c.Workloads = workloads
	c.Command = cloneCommand(c.Command)
	return &Generator{d, runner, c}, nil
}

func cloneCommand(c worker.CommandSpec) worker.CommandSpec {
	c.Argv = append([]string(nil), c.Argv...)
	c.Env = append([]string(nil), c.Env...)
	return c
}

func validCommand(c worker.CommandSpec) bool {
	if !filepath.IsAbs(c.Exe) || (c.CWD != "" && !filepath.IsAbs(c.CWD)) {
		return false
	}
	for _, s := range append(append([]string{c.Exe, c.CWD}, c.Argv...), c.Env...) {
		if strings.ContainsRune(s, 0) {
			return false
		}
	}
	for _, s := range c.Env {
		if strings.IndexByte(s, '=') <= 0 {
			return false
		}
	}
	return true
}

func (g *Generator) SpecFor(q domain.Quality) (model.LoadSpec, error) {
	w, ok := g.config.Workloads[q]
	if !ok {
		return model.LoadSpec{}, ErrUnsupported
	}
	return g.LoadSpec(w)
}

// GenerationOutcome transfers worker lifecycle ownership even on failure.
// Lifecycle.Reply is always nil: unvalidated/error-accompanying payloads cannot
// escape. If Stopped=false, the caller must retain its reservation and use the
// Cleanup handle (or quarantine when absent). Success is protocol validation,
// NOT media validation. No method here releases a lease or unloads a model.
type GenerationOutcome struct {
	Result     domain.GenerationResult `json:"result"`
	Lifecycle  worker.RunResult        `json:"lifecycle"`
	Provenance Provenance              `json:"provenance"`
}

// Generate assumes the caller has already admitted the exact SpecFor workload
// and arranged exclusive model use. Configured worker limits are monitoring
// bounds, not a resource lease. The supported Params vocabulary is prompt/seed
// strings. Workload is injected from configuration; no request parameter may
// configure runtime identity, command, limits, profile or execution dimensions.
func (g *Generator) Generate(ctx context.Context, req domain.GenerationRequest) (GenerationOutcome, error) {
	out := GenerationOutcome{Lifecycle: worker.RunResult{Stopped: true}, Provenance: g.Provenance()}
	if err := checkContext(ctx); err != nil {
		return out, err
	}
	if !handle(string(req.ID)) || !handle(string(req.ShotID)) || req.ProviderID != g.ID() {
		return out, ErrRequest
	}
	spec, err := g.SpecFor(req.Quality)
	if err != nil {
		return out, err
	}
	for k, v := range req.Params {
		if k != "prompt" && k != "seed" {
			return out, ErrRequest
		}
		if _, ok := v.(string); !ok {
			return out, ErrRequest
		}
	}
	params, err := copyInput(req.Params)
	if err != nil {
		return out, err
	}
	if params == nil {
		params = make(map[string]any)
	}
	params["workload"] = spec.Workload
	req.Params = params
	raw, err := json.Marshal(req)
	if err != nil {
		return out, ErrRequest
	}
	h := worker.Header{Version: worker.ProtocolVersion, Operation: "generate", RequestID: req.ID, ProviderID: g.ID(), ModelID: g.model.ModelID, ModelRevision: g.model.Requirements.Revision, ProfileID: g.profile.ID, ProfileRevision: g.profile.Revision}
	wreq := worker.Request{Header: h, Payload: raw}
	wire, err := json.Marshal(wreq)
	if err != nil || len(wire) > worker.MaxStdinBytes {
		return out, errors.Join(ErrRequest, worker.ErrInputLimit)
	}
	if g.config.Limits.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, g.config.Limits.Timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	run, callErr := g.runner.Run(ctx, cloneCommand(g.config.Command), wreq, g.config.Limits)
	out.Lifecycle = run
	out.Lifecycle.Reply = nil
	// A retained cleanup token contradicts a completed lifecycle. Fail closed
	// even if a broken injected runner also claims Stopped=true.
	if run.Cleanup != nil {
		out.Lifecycle.Stopped = false
	}
	out.Lifecycle.Measurement = resource.CloneMeasurement(run.Measurement)
	if run.Measurement.Validate() != nil {
		out.Lifecycle.Measurement = resource.Measurement{}
	}
	// Cancellation wins without dropping outstanding cleanup ownership.
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if !out.Lifecycle.Stopped {
		return out, errors.Join(ErrCleanup, worker.ErrCleanup)
	}
	if callErr != nil {
		return out, dependencyError(callErr)
	}
	if run.Measurement.Validate() != nil || run.Reply == nil || run.Reply.Header != h || run.Reply.Status != "ok" || run.Reply.ErrorCode != "" {
		return out, ErrResult
	}
	result, err := worker.DecodeGenerationResult(run.Reply.Payload, req.ID)
	if err != nil || (result.AssetID != "" && !handle(string(result.AssetID))) || (result.Seed != "" && !handle(result.Seed)) {
		return out, ErrResult
	}
	if result.PeakMemoryMB != nil && (math.IsNaN(*result.PeakMemoryMB) || math.IsInf(*result.PeakMemoryMB, 0) || *result.PeakMemoryMB < 0) {
		return out, ErrResult
	}
	// Worker-reported measurements/provenance are not parent evidence. Only the
	// injected trusted runner's validated observations may populate measurements.
	result.DurationMs = 0
	result.PeakMemoryMB = nil
	if run.Measurement.RunMs != nil {
		result.DurationMs = *run.Measurement.RunMs
	}
	if peak := run.Measurement.ObservedPeak(); peak != nil {
		mb := float64(*peak) / 1e6
		result.PeakMemoryMB = &mb
	}
	p := out.Provenance
	result.Metadata = map[string]any{"providerId": p.ProviderID, "adapterRevision": p.AdapterRevision, "modelId": p.ModelID, "modelRevision": p.ModelRevision, "profileId": p.ProfileID, "profileRevision": p.ProfileRevision, "backend": string(p.Backend), "source": p.Source, "quality": string(req.Quality)}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	out.Result = result
	return out, nil
}
