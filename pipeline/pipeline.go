package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/checkpoint"
	"github.com/manish96170/videofactory/director"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
	"github.com/manish96170/videofactory/idgen"
	"github.com/manish96170/videofactory/intelligence"
	"github.com/manish96170/videofactory/preview"
	"github.com/manish96170/videofactory/project"
	"github.com/manish96170/videofactory/validation"
)

var (
	ErrInvalid     = errors.New("pipeline: invalid request or dependency")
	ErrPlan        = errors.New("pipeline: director returned an invalid plan")
	ErrAnalysis    = errors.New("pipeline: shot analysis failed")
	ErrCheckpoint  = errors.New("pipeline: checkpoint operation failed")
	ErrArtifact    = errors.New("pipeline: verified artifact evidence unavailable")
	ErrComposition = errors.New("pipeline: timeline composition failed")
	ErrFinalMedia  = errors.New("pipeline: final media verification failed")
	// ErrRetryable marks an explicitly transient dependency failure. A retry
	// policy is never consulted for errors lacking this marker.
	ErrRetryable = errors.New("pipeline: explicitly retryable transient failure")
)

const maxRetryAttempts = 3
const maxRetryDelay = time.Second

var finalNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]+\.mp4$`)

// Director, ShotAnalyzer, PreviewBuilder and Checkpoints are existing package
// contracts narrowed to the operations this synchronous coordinator needs.
type Director interface {
	Plan(context.Context, director.PlanRequest) (director.PlanResult, error)
}

type ShotAnalyzer interface {
	Analyze(context.Context, intelligence.Request) (intelligence.Result, error)
}

type PreviewBuilder interface {
	Build(context.Context, preview.BuildRequest) (preview.BuildResult, error)
}

type Checkpoints interface {
	Load(context.Context, checkpoint.Identity) (checkpoint.Checkpoint, error)
	Save(context.Context, checkpoint.Checkpoint) error
}

// ArtifactLookup provides the authoritative published completion digest used
// in checkpoints. A pipeline must be given the same controlled store used by
// its PreviewBuilder.
type ArtifactLookup interface {
	Lookup(context.Context, artifact.Identity) (artifact.Entry, bool, error)
}

// Shot is a verified, immutable Phase 10 preview view passed to the timeline
// composer. The composer must consume these views, not caller-supplied paths.
type Shot struct {
	ID   domain.ID
	View preview.View
}

type TimelineComposer interface {
	Compose(context.Context, domain.Timeline, []Shot, string) error
}

type FinalValidator interface {
	Validate(context.Context, validation.Request) (domain.ValidationResult, error)
}

// ApprovalRequest describes execution that the deterministic coordinator will
// not perform. It contains only stable, non-secret project/shot handles and a
// typed strategy; prompt text and provider choices are deliberately omitted.
type ApprovalRequest struct {
	ProjectID domain.ID
	ShotID    domain.ID
	Strategy  ExecutionStrategy
	Reason    PendingReason
}

// ApprovalGate and Executor are future, explicit seams. Run does not call
// either interface in this tranche: local/cloud/mixed work always returns a
// PendingApproval result and can never fall through to execution.
type ApprovalGate interface {
	Approve(context.Context, ApprovalRequest) (ApprovalReceipt, error)
}

type ApprovalReceipt interface {
	ApprovalID() string
}

type ExecutionRequest struct {
	ProjectID domain.ID
	ShotID    domain.ID
	Strategy  ExecutionStrategy
}

type Executor interface {
	Execute(context.Context, ExecutionRequest, ApprovalReceipt) error
}

type EventPublisher interface {
	Publish(context.Context, event.Event) error
}

// InputFactory returns the Phase 10 pixel-input projection for one planned shot.
// dependencyMedia maps already verified direct shot dependencies to their media
// digests. The pipeline also binds those digests into KeyInput.Dependencies using
// stable shot-index roles. The callback must declare source digests and resolved
// render settings; the pipeline checks quality, duration, primitive order/type,
// and key validity before passing the request to PreviewBuilder.
type InputFactory func(context.Context, domain.Project, domain.Shot, domain.Quality, map[domain.ID]artifact.Digest) (artifact.KeyInput, error)

type Dependencies struct {
	Director       Director
	Intelligence   ShotAnalyzer
	Preview        PreviewBuilder
	Checkpoints    Checkpoints
	Artifacts      ArtifactLookup
	Composer       TimelineComposer
	FinalValidator FinalValidator
	Events         EventPublisher // optional best-effort observation
	FinalRoot      string         // existing exclusively controlled directory for final artifacts
	Retry          RetryPolicy    // optional; zero value disables retries
}

// RetryPolicy is an opt-in, bounded policy for explicitly marked transient
// preview/composition errors. MaxAttempts includes the initial call and is
// hard-capped by the pipeline. Retryable may narrow (never broaden) the
// ErrRetryable marker. Delay is cancellation-aware.
type RetryPolicy struct {
	MaxAttempts int
	Delay       time.Duration
	Retryable   func(error) bool
}

type Pipeline struct {
	deps          Dependencies
	finalRoot     string
	finalRootInfo os.FileInfo
}

func New(deps Dependencies) (*Pipeline, error) {
	if nilDependency(deps.Director) || nilDependency(deps.Intelligence) || nilDependency(deps.Preview) || nilDependency(deps.Checkpoints) || nilDependency(deps.Artifacts) || nilDependency(deps.Composer) || nilDependency(deps.FinalValidator) {
		return nil, ErrInvalid
	}
	if deps.Retry.MaxAttempts < 0 || deps.Retry.MaxAttempts > maxRetryAttempts || deps.Retry.Delay < 0 || deps.Retry.Delay > maxRetryDelay {
		return nil, ErrInvalid
	}
	root, info, err := controlledRoot(deps.FinalRoot)
	if err != nil {
		return nil, ErrInvalid
	}
	return &Pipeline{deps: deps, finalRoot: root, finalRootInfo: info}, nil
}

type ExecutionStrategy string

const (
	StrategyDeterministic ExecutionStrategy = "deterministic"
	StrategyLocal         ExecutionStrategy = "local"
	StrategyCloud         ExecutionStrategy = "cloud"
	StrategyMixed         ExecutionStrategy = "mixed"
	StrategyUnspecified   ExecutionStrategy = "unspecified"
)

type Status string

const (
	StatusRunning           Status = "running"
	StatusPartial           Status = "partial"
	StatusFailed            Status = "failed"
	StatusPendingApproval   Status = "pending_approval"
	StatusPendingCapability Status = "pending_capability"
	StatusCompleted         Status = "completed"
)

type PendingReason string

const (
	ReasonNonDeterministic PendingReason = "non_deterministic_strategy"
	ReasonExecutionNeeded  PendingReason = "execution_required"
	ReasonCapability       PendingReason = "capability_unresolved"
)

type Pending struct {
	ShotID           domain.ID
	Strategy         ExecutionStrategy
	Reason           PendingReason
	ApprovalRequired bool
}

type Request struct {
	Project    domain.Project // ID and Name are required; existing plan/timeline are replaced
	Prompt     string
	FlowID     domain.ID
	FramingID  domain.ID
	Quality    domain.Quality
	OutputPath string // safe MP4 basename published under Dependencies.FinalRoot; existing files are never replaced
	Inputs     InputFactory
	Analysis   intelligence.Request // template; project/shot/quality/render facts are overwritten
	Strategy   ExecutionStrategy    // empty defaults to deterministic; others are approval-only
}

type ShotResult struct {
	ID       domain.ID
	Strategy intelligence.Strategy
	CacheHit bool
	View     preview.View
}

type Result struct {
	Status     Status
	Plan       director.PlanResult
	Project    domain.Project
	Timeline   domain.Timeline
	Shots      []ShotResult
	Pending    []Pending
	FinalPath  string
	FinalCheck *domain.ValidationResult
}

// Run executes one synchronous pipeline. On failure it returns the partial
// result and preserves completed checkpoint/artifact evidence; FinalPath is
// populated only after composition and final-media validation both succeed.
// Cancellation is checked between every dependency callback and never produces
// a synthetic final result.
func (p *Pipeline) Run(ctx context.Context, req Request) (out Result, runErr error) {
	defer func() {
		if runErr != nil {
			if out.Project.ID.IsZero() {
				out.Status = StatusFailed
			} else {
				out.Status = StatusPartial
			}
		}
	}()
	if ctx == nil || p == nil || nilDependency(p.deps.Director) || nilDependency(p.deps.Intelligence) || nilDependency(p.deps.Preview) || nilDependency(p.deps.Checkpoints) || nilDependency(p.deps.Artifacts) || nilDependency(p.deps.Composer) || nilDependency(p.deps.FinalValidator) {
		return Result{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.Prompt) == "" || req.Project.ID.IsZero() || strings.TrimSpace(req.Project.Name) == "" || (req.Quality != domain.QualityDraft && req.Quality != domain.QualityFinal) {
		return Result{}, ErrInvalid
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = StrategyDeterministic
	}
	if !knownStrategy(strategy) || (strategy == StrategyDeterministic && (!validFinalName(req.OutputPath) || req.Inputs == nil)) {
		return Result{}, ErrInvalid
	}
	if strategy == StrategyDeterministic {
		if err := p.checkFinalTarget(req.OutputPath); err != nil {
			return Result{}, err
		}
	}

	planResult, err := p.deps.Director.Plan(ctx, director.PlanRequest{ProjectID: req.Project.ID, Prompt: req.Prompt, FlowID: req.FlowID, FramingID: req.FramingID})
	if err = callbackErr(ctx, err); err != nil {
		return Result{}, err
	}
	ownedPlan, err := clonePlanResult(planResult)
	if err != nil || project.ValidatePlan(req.Project.ID, ownedPlan.Plan) != nil {
		return Result{}, ErrPlan
	}
	ownedPlan.Plan = stablePlan(req.Project.ID, req.Prompt, ownedPlan.Plan)
	if err := project.ValidatePlan(req.Project.ID, ownedPlan.Plan); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrPlan, err)
	}
	proj, err := cloneProject(req.Project)
	if err != nil {
		return Result{}, ErrInvalid
	}
	proj.Plan = &ownedPlan.Plan
	timeline, err := sequentialTimeline(proj.ID, ownedPlan.Plan)
	if err != nil {
		return Result{}, ErrPlan
	}
	proj.Timeline = &timeline
	// The plan and timeline are the authority used by every following stage.
	if err := project.Validate(proj); err != nil {
		return Result{}, fmt.Errorf("%w: %v", ErrPlan, err)
	}
	out = Result{Status: StatusRunning, Plan: ownedPlan, Project: proj, Timeline: *proj.Timeline}
	if strategy != StrategyDeterministic {
		out.Status = StatusPendingApproval
		out.Pending = []Pending{{Strategy: strategy, Reason: ReasonNonDeterministic, ApprovalRequired: true}}
		p.publish(ctx, event.TypePlanCreated, proj.ID, "", "pending_approval")
		return out, nil
	}

	projectHash, err := hashProject(proj)
	if err != nil {
		return out, ErrPlan
	}
	cpIdentity := checkpoint.Identity{ProjectID: proj.ID, ProjectHash: projectHash, Quality: req.Quality}
	workOrder, err := dependencyOrder(ownedPlan.Plan)
	if err != nil {
		return out, ErrPlan
	}
	cp, invalidated, err := p.loadCheckpoint(ctx, cpIdentity, ownedPlan.Plan)
	if err != nil {
		return out, err
	}
	if err := p.deps.Checkpoints.Save(ctx, cp); err != nil {
		return out, safeDependency(ErrCheckpoint, err)
	}
	p.publish(ctx, event.TypePlanCreated, proj.ID, "", "planned")

	mediaDigests := make(map[domain.ID]artifact.Digest, len(workOrder))
	var renderConfig *artifact.RenderInput
	for _, shot := range workOrder {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		dependencyMedia := make(map[domain.ID]artifact.Digest, len(shot.DependsOn))
		for _, dependency := range shot.DependsOn {
			digest, ok := mediaDigests[dependency]
			if !ok || digest == (artifact.Digest{}) {
				return out, ErrArtifact
			}
			dependencyMedia[dependency] = digest
		}
		inputProject, cloneErr := cloneProject(proj)
		if cloneErr != nil {
			return out, ErrPlan
		}
		shotCopy, cloneErr := cloneShot(shot)
		if cloneErr != nil {
			return out, ErrPlan
		}
		input, callErr := req.Inputs(ctx, inputProject, shotCopy, req.Quality, cloneDigestMap(dependencyMedia))
		if callErr = callbackErr(ctx, callErr); callErr != nil {
			return out, safeDependency(ErrInvalid, callErr)
		}
		input = input.Clone()
		if err := bindDependencyDigests(&input, shot, ownedPlan.Plan, dependencyMedia); err != nil {
			return out, err
		}
		if input.Quality != req.Quality || input.Shot.Duration != shot.Duration || len(input.Shot.Primitives) != len(shot.Primitives) {
			return out, ErrInvalid
		}
		for i := range shot.Primitives {
			if input.Shot.Primitives[i].Type != shot.Primitives[i].Type {
				return out, ErrInvalid
			}
		}
		key, keyErr := artifact.BuildKey(input)
		if keyErr != nil {
			return out, ErrInvalid
		}
		if renderConfig == nil {
			copy := input.Render
			renderConfig = &copy
		} else if !sameRenderInput(*renderConfig, input.Render) {
			return out, ErrInvalid // stream-copy composition requires matching encodes
		}
		identity := artifact.Identity{ProjectID: proj.ID, ShotID: shot.ID, Quality: req.Quality, Key: key}
		prior, hadPrior := checkpointShot(cp, shot.ID)
		invalidationCode := "rebuilt"
		if invalidated[shot.ID] || (hadPrior && prior.State == checkpoint.Complete && prior.Key != key) {
			if hadPrior && prior.Key != key {
				invalidationCode = "inputs_changed"
			} else {
				invalidationCode = "artifact_unavailable"
			}
		}
		upsertCheckpointShot(&cp, checkpoint.Shot{ShotID: shot.ID, Key: key, State: checkpoint.Pending}, ownedPlan.Plan)
		if err := p.deps.Checkpoints.Save(ctx, cp); err != nil {
			return out, safeDependency(ErrCheckpoint, err)
		}

		analysisReq := req.Analysis
		analysisReq.ID = shot.ID
		analysisReq.Project = proj
		analysisReq.ShotID = shot.ID
		analysisReq.Requirements.Kind = domain.AssetKindVideo
		analysisReq.Requirements.Quality = req.Quality
		analysisReq.Requirements.ExpectedDuration = shot.Duration
		analysisReq.Requirements.Width = input.Render.Width
		analysisReq.Requirements.Height = input.Render.Height
		analysisReq.Requirements.FPS = input.Render.FPS
		var ownedAnalysisReq intelligence.Request
		if err := snapshot(analysisReq, &ownedAnalysisReq); err != nil {
			return out, ErrInvalid
		}
		analysis, callErr := p.deps.Intelligence.Analyze(ctx, ownedAnalysisReq)
		if callErr = callbackErr(ctx, callErr); callErr != nil {
			return out, safeDependency(ErrAnalysis, callErr)
		}
		if analysis.ID != shot.ID || analysis.ShotID != shot.ID {
			return out, ErrAnalysis
		}
		if analysis.Strategy != intelligence.Deterministic || analysis.ConceptualPossible != intelligence.Yes || analysis.RenderedSupport != intelligence.Yes || analysis.MissingAudioReadiness {
			pendingStrategy, reason, approval, status := StrategyUnspecified, ReasonExecutionNeeded, true, StatusPendingApproval
			if analysis.Strategy == intelligence.Deterministic || analysis.Strategy == intelligence.OpNone {
				pendingStrategy, reason, approval, status = StrategyDeterministic, ReasonCapability, false, StatusPendingCapability
			}
			out.Status = status
			out.Pending = append(out.Pending, Pending{ShotID: shot.ID, Strategy: pendingStrategy, Reason: reason, ApprovalRequired: approval})
			return out, nil
		}
		markPending(&cp, identity)
		if err := p.deps.Checkpoints.Save(ctx, cp); err != nil {
			return out, safeDependency(ErrCheckpoint, err)
		}
		var built preview.BuildResult
		callErr = p.retry(ctx, func() error {
			buildProject, projectErr := cloneProject(proj)
			if projectErr != nil {
				return ErrPlan
			}
			var buildErr error
			built, buildErr = p.deps.Preview.Build(ctx, preview.BuildRequest{Project: buildProject, Identity: identity, Input: input.Clone()})
			return buildErr
		})
		if callErr = callbackErr(ctx, callErr); callErr != nil {
			return out, callErr
		}
		if built.View.Revision() == "" || built.View.Identity() != identity || built.View.MediaName() == "" {
			return out, preview.ErrInvalid
		}
		entry, found, callErr := p.deps.Artifacts.Lookup(ctx, identity)
		if callErr = callbackErr(ctx, callErr); callErr != nil {
			return out, safeDependency(ErrArtifact, callErr)
		}
		if !found || entry.Identity != identity || entry.Digest == (artifact.Digest{}) || entry.Media.Digest == (artifact.Digest{}) || entry.Validation == nil || entry.Validation.ID != shot.ID || !accepted(entry.Validation, shot.ID) {
			return out, ErrArtifact
		}
		mediaDigests[shot.ID] = entry.Media.Digest
		markComplete(&cp, identity, entry.Digest)
		if err := p.deps.Checkpoints.Save(ctx, cp); err != nil {
			return out, safeDependency(ErrCheckpoint, err)
		}
		if !built.CacheHit && (invalidated[shot.ID] || (hadPrior && prior.State == checkpoint.Complete)) {
			p.publish(ctx, event.TypeShotInvalidated, proj.ID, shot.ID, invalidationCode)
		}
		out.Shots = append(out.Shots, ShotResult{ID: shot.ID, Strategy: analysis.Strategy, CacheHit: built.CacheHit, View: built.View})
		p.publish(ctx, event.TypeRenderCompleted, proj.ID, shot.ID, "verified")
	}

	if err := ctx.Err(); err != nil {
		return out, err
	}
	shotResults := make(map[domain.ID]ShotResult, len(out.Shots))
	for _, s := range out.Shots {
		shotResults[s.ID] = s
	}
	verified := make([]Shot, 0, len(out.Shots))
	for _, entry := range out.Timeline.Entries {
		s, ok := shotResults[entry.ShotID]
		if !ok {
			return out, ErrArtifact
		}
		verified = append(verified, Shot{ID: s.ID, View: s.View})
	}
	var stage string
	defer func() {
		if stage != "" && p.sameFinalRoot() {
			_ = os.Remove(stage)
		}
	}()
	composeErr := p.retry(ctx, func() error {
		if stage != "" {
			_ = os.Remove(stage)
		}
		var stageErr error
		stage, stageErr = p.newFinalStage()
		if stageErr != nil {
			return stageErr
		}
		composeTimeline, cloneErr := cloneTimeline(out.Timeline)
		if cloneErr != nil {
			return ErrPlan
		}
		composeShots := append([]Shot(nil), verified...)
		return p.deps.Composer.Compose(ctx, composeTimeline, composeShots, stage)
	})
	if composeErr != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, safeDependency(ErrComposition, composeErr)
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	stageCurrent, err := os.Lstat(stage)
	if err != nil || !stageCurrent.Mode().IsRegular() || stageCurrent.Size() <= 0 || stageCurrent.Mode()&os.ModeSymlink != 0 || !p.sameFinalRoot() {
		return out, ErrFinalMedia
	}
	finalID := stableID("final", string(proj.ID), string(ownedPlan.Plan.ID))
	finalRequirements := validation.Requirements{Kind: domain.AssetKindVideo, Quality: req.Quality, ExpectedDuration: timelineDuration(out.Timeline), FPS: renderConfig.FPS, Width: renderConfig.Width, Height: renderConfig.Height}
	if finalRequirements.Validate() != nil {
		return out, ErrInvalid
	}
	checked, err := p.deps.FinalValidator.Validate(ctx, validation.Request{ID: finalID, TargetID: ownedPlan.Plan.ID, Asset: domain.Asset{ID: finalID, Kind: domain.AssetKindVideo, URI: stage}, Requirements: finalRequirements})
	if err = callbackErr(ctx, err); err != nil {
		return out, safeDependency(ErrFinalMedia, err)
	}
	if !accepted(&checked, ownedPlan.Plan.ID) || checked.ID != finalID {
		return out, ErrFinalMedia
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	target := filepath.Join(p.finalRoot, req.OutputPath)
	if !p.sameFinalRoot() || os.Link(stage, target) != nil {
		return out, ErrFinalMedia // link is an atomic, no-overwrite publication
	}
	_ = os.Remove(stage)
	if info, err := os.Lstat(target); err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(stageCurrent, info) {
		return out, ErrFinalMedia
	}
	clean := domain.ValidationResult{ID: checked.ID, TargetID: checked.TargetID, Status: checked.Status}
	out.FinalCheck = &clean
	out.FinalPath = target
	out.Status = StatusCompleted
	p.publish(ctx, event.TypeRenderCompleted, proj.ID, "", "complete")
	return out, nil
}

func (p *Pipeline) loadCheckpoint(ctx context.Context, identity checkpoint.Identity, plan domain.VideoPlan) (checkpoint.Checkpoint, map[domain.ID]bool, error) {
	byShot := make(map[domain.ID]bool, len(plan.Shots))
	for _, shot := range plan.Shots {
		byShot[shot.ID] = true
	}
	cp := checkpoint.Checkpoint{SchemaVersion: checkpoint.SchemaVersion, Identity: identity}
	invalidated := make(map[domain.ID]bool)
	prior, err := p.deps.Checkpoints.Load(ctx, identity)
	if err == nil {
		if prior.SchemaVersion != checkpoint.SchemaVersion || prior.Identity != identity {
			return checkpoint.Checkpoint{}, nil, ErrCheckpoint
		}
		for _, old := range prior.Shots {
			if _, ok := byShot[old.ShotID]; ok && old.State == checkpoint.Complete {
				artifactID := artifact.Identity{ProjectID: identity.ProjectID, ShotID: old.ShotID, Quality: identity.Quality, Key: old.Key}
				entry, found, lookupErr := p.deps.Artifacts.Lookup(ctx, artifactID)
				if lookupErr = callbackErr(ctx, lookupErr); lookupErr != nil {
					return checkpoint.Checkpoint{}, nil, safeDependency(ErrArtifact, lookupErr)
				}
				if found && entry.Identity == artifactID && entry.Digest == old.ArtifactDigest && entry.Media.Digest != (artifact.Digest{}) && entry.Validation != nil && entry.Validation.ID == old.ShotID && accepted(entry.Validation, old.ShotID) {
					cp.Shots = append(cp.Shots, old)
				} else {
					invalidated[old.ShotID] = true
				}
			}
		}
	} else if !errors.Is(err, checkpoint.ErrNotFound) && !errors.Is(err, checkpoint.ErrProjectHash) {
		if ctx.Err() != nil {
			return checkpoint.Checkpoint{}, nil, ctx.Err()
		}
		return checkpoint.Checkpoint{}, nil, safeDependency(ErrCheckpoint, err)
	}
	sortCheckpoint(&cp, plan)
	return cp, invalidated, nil
}

func dependencyOrder(plan domain.VideoPlan) ([]domain.Shot, error) {
	graph, err := project.BuildDependencyGraph(plan.Shots)
	if err != nil {
		return nil, err
	}
	ids, err := graph.TopologicalOrder()
	if err != nil {
		return nil, err
	}
	byID := make(map[domain.ID]domain.Shot, len(plan.Shots))
	for _, shot := range plan.Shots {
		byID[shot.ID] = shot
	}
	ordered := make([]domain.Shot, 0, len(ids))
	for _, id := range ids {
		shot, ok := byID[id]
		if !ok {
			return nil, ErrPlan
		}
		ordered = append(ordered, shot)
	}
	return ordered, nil
}

func bindDependencyDigests(input *artifact.KeyInput, shot domain.Shot, plan domain.VideoPlan, digests map[domain.ID]artifact.Digest) error {
	if len(shot.DependsOn) == 0 {
		return nil
	}
	shotByID := make(map[domain.ID]domain.Shot, len(plan.Shots))
	for _, candidate := range plan.Shots {
		shotByID[candidate.ID] = candidate
	}
	if input.Dependencies == nil {
		input.Dependencies = make(map[string]artifact.Digest)
	}
	for _, dependency := range shot.DependsOn {
		dependencyShot, ok := shotByID[dependency]
		digest, found := digests[dependency]
		if !ok || !found || digest == (artifact.Digest{}) {
			return ErrArtifact
		}
		role := fmt.Sprintf("shot-dependency-index-%d", dependencyShot.Index)
		if existing, exists := input.Dependencies[role]; exists && existing != digest {
			return ErrInvalid
		}
		input.Dependencies[role] = digest
	}
	return nil
}

func upsertCheckpointShot(cp *checkpoint.Checkpoint, shot checkpoint.Shot, plan domain.VideoPlan) {
	for i := range cp.Shots {
		if cp.Shots[i].ShotID == shot.ShotID {
			cp.Shots[i] = shot
			sortCheckpoint(cp, plan)
			return
		}
	}
	cp.Shots = append(cp.Shots, shot)
	sortCheckpoint(cp, plan)
}

func sortCheckpoint(cp *checkpoint.Checkpoint, plan domain.VideoPlan) {
	order := make(map[domain.ID]int, len(plan.Shots))
	for _, shot := range plan.Shots {
		order[shot.ID] = shot.Index
	}
	sort.Slice(cp.Shots, func(i, j int) bool { return order[cp.Shots[i].ShotID] < order[cp.Shots[j].ShotID] })
}

func sameRenderInput(a, b artifact.RenderInput) bool {
	x, errX := json.Marshal(a)
	y, errY := json.Marshal(b)
	return errX == nil && errY == nil && reflect.DeepEqual(x, y)
}

func cloneDigestMap(in map[domain.ID]artifact.Digest) map[domain.ID]artifact.Digest {
	out := make(map[domain.ID]artifact.Digest, len(in))
	for id, digest := range in {
		out[id] = digest
	}
	return out
}

func markComplete(cp *checkpoint.Checkpoint, id artifact.Identity, digest artifact.Digest) {
	for i := range cp.Shots {
		if cp.Shots[i].ShotID == id.ShotID {
			cp.Shots[i].Key = id.Key
			cp.Shots[i].State = checkpoint.Complete
			cp.Shots[i].ArtifactDigest = digest
			return
		}
	}
}

func markPending(cp *checkpoint.Checkpoint, id artifact.Identity) {
	for i := range cp.Shots {
		if cp.Shots[i].ShotID == id.ShotID {
			cp.Shots[i].Key = id.Key
			cp.Shots[i].State = checkpoint.Pending
			return
		}
	}
}

func sequentialTimeline(projectID domain.ID, plan domain.VideoPlan) (domain.Timeline, error) {
	shots := append([]domain.Shot(nil), plan.Shots...)
	sort.Slice(shots, func(i, j int) bool { return shots[i].Index < shots[j].Index })
	timeline := domain.Timeline{ID: stableID("timeline", string(projectID), string(plan.ID)), ProjectID: projectID}
	var offset time.Duration
	for _, shot := range shots {
		if shot.Duration <= 0 || offset > time.Duration(math.MaxInt64)-shot.Duration {
			return domain.Timeline{}, ErrPlan
		}
		timeline.Entries = append(timeline.Entries, domain.TimelineEntry{ShotID: shot.ID, Start: offset, End: offset + shot.Duration})
		for _, primitive := range shot.Primitives {
			timeline.Elements = append(timeline.Elements, visualElement(shot, primitive))
		}
		offset += shot.Duration
	}
	return timeline, nil
}

func visualElement(shot domain.Shot, primitive domain.Primitive) domain.VisualElement {
	e := domain.VisualElement{ID: primitive.ID, ShotID: shot.ID, Type: string(primitive.Type), End: shot.Duration}
	if raw, ok := primitive.Properties["bounds"].(map[string]any); ok {
		if x, xok := number(raw["x"]); xok {
			if y, yok := number(raw["y"]); yok {
				if w, wok := number(raw["width"]); wok {
					if h, hok := number(raw["height"]); hok {
						e.Bounds = &domain.Bounds{X: x, Y: y, Width: w, Height: h}
					}
				}
			}
		}
	}
	keys := map[domain.PrimitiveType][]string{
		domain.PrimitiveTypeText:       {"text", "color", "fontSize"},
		domain.PrimitiveTypeImage:      {"assetPath"},
		domain.PrimitiveTypeBackground: {"color"},
		domain.PrimitiveTypeTransition: {"style"},
	}
	if allowed, ok := keys[primitive.Type]; ok {
		e.EditableProperties = make(map[string]any)
		for _, key := range allowed {
			if value, exists := primitive.Properties[key]; exists {
				e.EditableProperties[key] = value
			}
		}
	}
	return e
}

func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case float32:
		f := float64(n)
		return f, !math.IsNaN(f) && !math.IsInf(f, 0)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func stablePlan(projectID domain.ID, prompt string, plan domain.VideoPlan) domain.VideoPlan {
	plan.ProjectID = projectID
	plan.CreatedAt = time.Unix(0, 0).UTC()
	oldToNew := make(map[domain.ID]domain.ID, len(plan.Shots))
	for i := range plan.Shots {
		shot := &plan.Shots[i]
		original := shot.ID
		shot.ID = stableID("shot", string(projectID), prompt, fmt.Sprint(shot.Index), string(shot.Category), fmt.Sprint(shot.Duration), primitiveFingerprint(shot.Primitives))
		oldToNew[original] = shot.ID
		for j := range shot.Primitives {
			shot.Primitives[j].ID = stableID("visual", string(shot.ID), fmt.Sprint(j), string(shot.Primitives[j].Type), jsonFingerprint(shot.Primitives[j].Properties))
		}
	}
	sort.Slice(plan.Shots, func(i, j int) bool { return plan.Shots[i].Index < plan.Shots[j].Index })
	planParts := []string{string(projectID), prompt, string(plan.FlowID), string(plan.FramingID)}
	for _, shot := range plan.Shots {
		planParts = append(planParts, string(shot.ID))
	}
	plan.ID = stableID("plan", planParts...)
	for i := range plan.Shots {
		plan.Shots[i].PlanID = plan.ID
	}
	for i := range plan.Shots {
		for j, dependency := range plan.Shots[i].DependsOn {
			if mapped, ok := oldToNew[dependency]; ok {
				plan.Shots[i].DependsOn[j] = mapped
			}
		}
	}
	return plan
}

func primitiveFingerprint(primitives []domain.Primitive) string {
	type projection struct {
		Type       domain.PrimitiveType
		Properties map[string]any
	}
	values := make([]projection, 0, len(primitives))
	for _, p := range primitives {
		values = append(values, projection{Type: p.Type, Properties: p.Properties})
	}
	return jsonFingerprint(values)
}

func jsonFingerprint(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func stableID(prefix string, parts ...string) domain.ID {
	data, _ := json.Marshal(parts)
	sum := sha256.Sum256(data)
	return domain.ID(prefix + "-" + hex.EncodeToString(sum[:]))
}

func hashProject(proj domain.Project) (artifact.Digest, error) {
	// Created/updated timestamps describe run timing, not authoritative visual
	// content. Normalize them so an equivalent prompt plan resumes across runs.
	proj.CreatedAt, proj.UpdatedAt = time.Time{}, time.Time{}
	if proj.Plan != nil {
		plan := *proj.Plan
		plan.CreatedAt = time.Time{}
		proj.Plan = &plan
	}
	b, err := json.Marshal(proj)
	if err != nil {
		return artifact.Digest{}, err
	}
	return artifact.Digest(sha256.Sum256(b)), nil
}

func clonePlanResult(in director.PlanResult) (director.PlanResult, error) {
	b, err := json.Marshal(in)
	if err != nil {
		return director.PlanResult{}, err
	}
	var out director.PlanResult
	if err := json.Unmarshal(b, &out); err != nil {
		return director.PlanResult{}, err
	}
	return out, nil
}

func cloneProject(in domain.Project) (domain.Project, error) {
	var out domain.Project
	err := snapshot(in, &out)
	return out, err
}

func cloneShot(in domain.Shot) (domain.Shot, error) {
	var out domain.Shot
	err := snapshot(in, &out)
	return out, err
}

func cloneTimeline(in domain.Timeline) (domain.Timeline, error) {
	var out domain.Timeline
	err := snapshot(in, &out)
	return out, err
}

func snapshot(in, out any) error {
	b, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func accepted(v *domain.ValidationResult, target domain.ID) bool {
	return v != nil && v.TargetID == target && (v.Status == domain.ValidationPassed || v.Status == domain.ValidationWarning) && v.FailureClass == "" && !v.RetryRecommended
}

func callbackErr(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

type dependencyError struct{ code, cause error }

func (e *dependencyError) Error() string        { return e.code.Error() }
func (e *dependencyError) Unwrap() error        { return e.cause }
func (e *dependencyError) Is(target error) bool { return target == e.code }

func safeDependency(code, cause error) error {
	if cause == nil {
		return nil
	}
	return &dependencyError{code: code, cause: cause}
}

func knownStrategy(s ExecutionStrategy) bool {
	switch s {
	case StrategyDeterministic, StrategyLocal, StrategyCloud, StrategyMixed:
		return true
	default:
		return false
	}
}

func timelineDuration(t domain.Timeline) time.Duration {
	var end time.Duration
	for _, entry := range t.Entries {
		if entry.End > end {
			end = entry.End
		}
	}
	return end
}

func validFinalName(name string) bool {
	return name != "" && filepath.Base(name) == name && !strings.ContainsAny(name, `/\\:`) && finalNamePattern.MatchString(name)
}

func controlledRoot(root string) (string, os.FileInfo, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil, ErrInvalid
	}
	info, err := os.Lstat(filepath.Clean(root))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, ErrInvalid
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", nil, ErrInvalid
	}
	info, err = os.Lstat(abs)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, ErrInvalid
	}
	return abs, info, nil
}

func (p *Pipeline) sameFinalRoot() bool {
	if p == nil || p.finalRoot == "" || p.finalRootInfo == nil {
		return false
	}
	info, err := os.Lstat(p.finalRoot)
	return err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 && os.SameFile(p.finalRootInfo, info)
}

func (p *Pipeline) checkFinalTarget(name string) error {
	if !validFinalName(name) || !p.sameFinalRoot() {
		return ErrFinalMedia
	}
	_, err := os.Lstat(filepath.Join(p.finalRoot, name))
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return ErrFinalMedia
	}
	return nil
}

func (p *Pipeline) newFinalStage() (string, error) {
	if !p.sameFinalRoot() {
		return "", ErrFinalMedia
	}
	f, err := os.CreateTemp(p.finalRoot, ".pipeline-stage-*.mp4")
	if err != nil {
		return "", ErrFinalMedia
	}
	name := f.Name()
	info, statErr := f.Stat()
	closeErr := f.Close()
	if statErr != nil || closeErr != nil || info == nil || !info.Mode().IsRegular() || !p.sameFinalRoot() {
		_ = os.Remove(name)
		return "", ErrFinalMedia
	}
	return name, nil
}

func (p *Pipeline) retry(ctx context.Context, operation func() error) error {
	maxAttempts := p.deps.Retry.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 1
	}
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := operation()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil || attempt >= maxAttempts || !errors.Is(err, ErrRetryable) {
			return err
		}
		if p.deps.Retry.Retryable != nil && !p.deps.Retry.Retryable(err) {
			return err
		}
		if delay := p.deps.Retry.Delay; delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
}

func checkpointShot(cp checkpoint.Checkpoint, id domain.ID) (checkpoint.Shot, bool) {
	for _, shot := range cp.Shots {
		if shot.ShotID == id {
			return shot, true
		}
	}
	return checkpoint.Shot{}, false
}

func (p *Pipeline) publish(ctx context.Context, kind event.Type, projectID, shotID domain.ID, code string) {
	if p == nil || nilDependency(p.deps.Events) || ctx.Err() != nil {
		return
	}
	// The publisher receives a fresh scalar-only payload that this method never
	// retains or mutates. Best-effort publication cannot change pipeline results.
	child, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	_ = p.deps.Events.Publish(child, event.Event{ID: domain.ID(idgen.New()), Type: kind, ProjectID: projectID, ShotID: shotID, OccurredAt: time.Now().UTC(), Payload: map[string]any{"code": code}})
}

func nilDependency(v any) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return rv.IsNil()
	default:
		return false
	}
}
