// Package intelligence recommends a shot strategy; it never renders, generates,
// reserves resources, loads models, or authorizes remote spending.
package intelligence

import (
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
	"github.com/manish96170/videofactory/provider"
	"github.com/manish96170/videofactory/resource"
	"github.com/manish96170/videofactory/validation"
)

type Fact string

const (
	Unknown   Fact = "unknown"
	Yes       Fact = "yes"
	No        Fact = "no"
	Uncertain Fact = "uncertain"
)

type Strategy string

const (
	OpNone        Strategy = "none"
	Deterministic Strategy = "deterministic"
	ReuseVideo    Strategy = "reuse_video"
	StaticImage   Strategy = "static_image"
	AnimateImage  Strategy = "animate_image"
	GenerateVideo Strategy = "generate_video"
)

// Candidate IDs are explicit caller-trusted shot associations, NOT DependsOn
// (which contains shot IDs). Empty judgments mean unknown; positive judgments
// must express suitability for this shot's visual intent, not just file presence.
type Candidate struct {
	AssetID                                      domain.ID
	Suitable, ImageSufficient, AnimationSuitable domain.DecisionVerdict
}
type Request struct {
	ID           domain.ID
	Project      domain.Project
	ShotID       domain.ID
	Requirements validation.Requirements // explicit VIDEO output, including duration
	Candidates   []Candidate             // ordered, distinct, maximum 8
	Route        provider.RouteRequest   // additional tags and ALL caller constraints survive
	Bindings     []resource.PlanRequest  // optional trusted bindings, maximum 16
	// Explicit caller-trusted audio/composition capabilities, not inferred from
	// unrelated Route tags. Missing tags block new audio-required video routing.
	AudioRequiredTags []string
}
type Judgment struct {
	Kind    string
	AssetID domain.ID
	Verdict Fact
	Score   float64
	Code    string // owned vocabulary; never adapter reason/prose
}
type AssetEvidence struct {
	AssetID    domain.ID
	Code       string
	Validation *domain.ValidationResult
}
type Result struct {
	ID, ShotID                                       domain.ID
	Strategy                                         Strategy
	ConceptualPossible, RenderedSupport, InputsReady Fact
	MissingAudioReadiness                            bool // requires separate composition; no silent audio fulfillment
	SourceAssetID                                    domain.ID
	Code                                             string
	Judgments                                        []Judgment
	Assets                                           []AssetEvidence
	RequiredTags                                     []string
	Routing                                          *provider.RouteResult
	ResourceStatus                                   string // unknown, or resource.PlanStatus; not an execution promise
	Resource                                         *resource.PlanResult
}
type Primitives interface {
	Get(domain.PrimitiveType) (domain.PrimitiveCapabilities, bool)
}
type Router interface {
	Route(context.Context, provider.RouteRequest) (provider.RouteResult, error)
}
type Decider interface {
	Decide(context.Context, domain.DecisionRequest) (domain.DecisionResult, error)
}
type Planner interface {
	Plan(context.Context, resource.PlanRequest) (resource.PlanResult, error)
}
type Analyzer struct {
	primitives Primitives
	validator  validation.Validator
	router     Router
	decider    Decider
	planner    Planner
}

var (
	ErrInvalidRequest    = errors.New("intelligence: invalid_request")
	ErrInvalidDependency = errors.New("intelligence: invalid_dependency")
)

// Dependencies are caller-owned and must cooperate with cancellation, be safe
// for concurrent calls, and not retain/asynchronously mutate inputs or outputs.
// Optional typed-nil decider/planner are treated as absent.
func New(primitives Primitives, validator validation.Validator, router Router, decider Decider, planner Planner) (*Analyzer, error) {
	if isNil(primitives) || isNil(validator) || isNil(router) {
		return nil, ErrInvalidDependency
	}
	if isNil(decider) {
		decider = nil
	}
	if isNil(planner) {
		planner = nil
	}
	return &Analyzer{primitives, validator, router, decider, planner}, nil
}

// Analyze snapshots JSON-compatible inputs (ADR-008); callers must not mutate
// during snapshotting. Each callback receives separate inputs. Cooperative calls
// have a fixed 5s ceiling, without goroutines or retries; parent cancellation wins.
func (a *Analyzer) Analyze(ctx context.Context, req Request) (out Result, err error) {
	if ctx == nil {
		return out, ErrInvalidRequest
	}
	if err = ctx.Err(); err != nil {
		return out, err
	}
	if a == nil || isNil(a.primitives) || isNil(a.validator) || isNil(a.router) {
		return out, ErrInvalidDependency
	}
	if len(req.Candidates) > 8 || len(req.Bindings) > 16 {
		return out, ErrInvalidRequest
	}
	var ownedReq Request
	if err = snapshot(req, &ownedReq); err != nil {
		return out, safeError("invalid_request", errors.Join(ErrInvalidRequest, err))
	}
	req = ownedReq
	defer func() {
		if e := ctx.Err(); e != nil {
			out, err = Result{}, e
		}
	}()
	if blank(string(req.ID)) || blank(string(req.ShotID)) || req.Project.Plan == nil || blank(string(req.Project.ID)) {
		return out, ErrInvalidRequest
	}
	if e := project.ValidatePlan(req.Project.ID, *req.Project.Plan); e != nil {
		return out, safeError("invalid_source", errors.Join(ErrInvalidRequest, e))
	}
	var shot domain.Shot
	for _, s := range req.Project.Plan.Shots {
		if s.ID == req.ShotID {
			shot = s
			break
		}
	}
	if shot.ID.IsZero() || req.Requirements.Kind != domain.AssetKindVideo || req.Requirements.ExpectedDuration != shot.Duration {
		return out, ErrInvalidRequest
	}
	if err = req.Requirements.Validate(); err != nil {
		return out, safeError("invalid_quality", errors.Join(ErrInvalidRequest, err))
	}
	if len(req.AudioRequiredTags) > 8 {
		return out, ErrInvalidRequest
	}
	audioTags := map[string]bool{}
	for _, tag := range req.AudioRequiredTags {
		if blank(tag) || audioTags[tag] {
			return out, ErrInvalidRequest
		}
		audioTags[tag] = true
	}
	seen := map[domain.ID]bool{}
	for _, c := range req.Candidates {
		if blank(string(c.AssetID)) || seen[c.AssetID] || !verdict(c.Suitable, true) || !verdict(c.ImageSufficient, true) || !verdict(c.AnimationSuitable, true) {
			return out, ErrInvalidRequest
		}
		seen[c.AssetID] = true
	}
	// Validate policy even if the eventual strategy needs no generation provider.
	policy := req.Route.Clone()
	policy.RequiredTags = append(policy.RequiredTags, "deterministic")
	if e := policy.Validate(); e != nil {
		return out, safeError("invalid_policy", errors.Join(ErrInvalidRequest, e))
	}
	out = Result{ID: req.ID, ShotID: shot.ID, Strategy: OpNone, ConceptualPossible: Unknown, RenderedSupport: Unknown, InputsReady: Unknown, MissingAudioReadiness: req.Requirements.AudioRequired, ResourceStatus: "unknown", Code: "unresolved"}
	caps := make([]domain.PrimitiveCapabilities, 0, len(shot.Primitives))
	known, allDet, supported := len(shot.Primitives) > 0, len(shot.Primitives) > 0, true
	for _, p := range shot.Primitives {
		if e := ctx.Err(); e != nil {
			return Result{}, e
		}
		c, ok := a.primitives.Get(p.Type)
		if e := ctx.Err(); e != nil {
			return Result{}, e
		}
		if !ok || c.Type != p.Type {
			known, allDet = false, false
			continue
		}
		caps = append(caps, c)
		allDet = allDet && c.Deterministic
		supported = supported && renderSupport(p.Type)
	}
	if known {
		out.RenderedSupport = No
		if supported {
			out.RenderedSupport = Yes
		}
	}
	if allDet && known {
		out.ConceptualPossible, out.Strategy, out.Code = Yes, Deterministic, "deterministic_first"
		out.InputsReady = cheapInputs(shot, caps)
		out.MissingAudioReadiness = req.Requirements.AudioRequired
		return a.route(ctx, req, out, capabilityTags(caps))
	}
	d := a.judge(ctx, req, shot, "deterministic_possible", "", "")
	out.Judgments = append(out.Judgments, d)
	out.ConceptualPossible = d.Verdict
	if d.Verdict == Yes {
		out.Strategy, out.Code = Deterministic, "conceptual_only"
		return out, nil
	}
	resolved := d.Verdict == No && known
	for _, c := range req.Candidates {
		evidence := AssetEvidence{AssetID: c.AssetID, Code: "invalid_association"}
		var asset domain.Asset
		matches := 0
		for _, v := range req.Project.Assets {
			if v.ID == c.AssetID {
				asset = domain.Asset{ID: v.ID, Kind: v.Kind, URI: v.URI}
				matches++
			}
		}
		if matches != 1 || blank(asset.URI) || (asset.Kind != domain.AssetKindVideo && asset.Kind != domain.AssetKindImage) {
			out.Assets = append(out.Assets, evidence)
			resolved = false
			continue
		}
		r := req.Requirements
		if asset.Kind == domain.AssetKindImage {
			r.Kind, r.ExpectedDuration, r.FPS, r.AudioRequired, r.ImageMinimumDimensions = domain.AssetKindImage, 0, 0, false, true
		}
		child, cancel := context.WithTimeout(ctx, 5*time.Second)
		var v domain.ValidationResult
		e := child.Err()
		if e == nil {
			v, e = a.validator.Validate(child, validation.Request{ID: req.ID, TargetID: shot.ID, Asset: asset, Requirements: r})
		}
		e = callbackError(ctx, child, e)
		cancel()
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		if e != nil {
			evidence.Code = "validation_error"
			resolved = false
		} else {
			clean, valid := cleanValidation(v, req.ID, shot.ID, r.Quality)
			evidence.Validation = &clean
			evidence.Code = "validation_blocked"
			if valid {
				evidence.Code = "verified"
			} else {
				resolved = false
			}
		}
		out.Assets = append(out.Assets, evidence)
		if evidence.Code != "verified" {
			continue
		}
		kind, hint := "asset_reuse_suitable", c.Suitable
		if asset.Kind == domain.AssetKindImage {
			kind, hint = "image_sufficient", c.ImageSufficient
		}
		j := a.judge(ctx, req, shot, kind, asset.ID, hint)
		out.Judgments = append(out.Judgments, j)
		if j.Verdict == Yes {
			out.SourceAssetID, out.InputsReady = asset.ID, Yes
			if asset.Kind == domain.AssetKindVideo {
				out.Strategy, out.Code = ReuseVideo, "verified_reuse"
				out.MissingAudioReadiness = false // video validation included AudioRequired
				return out, nil
			}
			out.Strategy, out.Code, out.MissingAudioReadiness = StaticImage, "verified_static_image", req.Requirements.AudioRequired
			c, ok := a.primitives.Get(domain.PrimitiveTypeImage)
			if !ok || c.Type != domain.PrimitiveTypeImage {
				return out, nil
			}
			return a.route(ctx, req, out, c.Tags)
		}
		resolved = resolved && j.Verdict == No
		if asset.Kind != domain.AssetKindImage {
			continue
		}
		j = a.judge(ctx, req, shot, "image_animation_suitable", asset.ID, c.AnimationSuitable)
		out.Judgments = append(out.Judgments, j)
		if j.Verdict == Yes {
			c, ok := a.primitives.Get(domain.PrimitiveTypeAnimatedImage)
			if !ok || c.Type != domain.PrimitiveTypeAnimatedImage {
				resolved = false
				continue
			}
			out.SourceAssetID, out.InputsReady, out.Strategy, out.Code = asset.ID, Yes, AnimateImage, "verified_animation_input"
			out.MissingAudioReadiness = req.Requirements.AudioRequired
			return a.route(ctx, req, out, c.Tags)
		}
		resolved = resolved && j.Verdict == No
	}
	if resolved {
		out.Strategy, out.Code = GenerateVideo, "generation_advisory"
		return a.route(ctx, req, out, capabilityTags(caps))
	}
	return out, nil
}

func (a *Analyzer) judge(ctx context.Context, req Request, shot domain.Shot, kind string, asset domain.ID, hint domain.DecisionVerdict) Judgment {
	j := Judgment{Kind: kind, AssetID: asset, Verdict: Unknown, Code: "unavailable"}
	if hint != "" {
		j.Verdict, j.Code = Fact(hint), "caller_suitability"
		return j
	}
	if a.decider == nil || ctx.Err() != nil {
		return j
	}
	// Deliberately omit prose, properties, URIs, metadata and private category/type
	// vocabularies. This is a bounded scalar descriptor, not a model prompt parser.
	input := map[string]any{"durationNs": float64(shot.Duration), "primitiveCount": float64(len(shot.Primitives)), "hasCandidate": asset != "", "width": float64(req.Requirements.Width), "height": float64(req.Requirements.Height), "fps": req.Requirements.FPS, "audioRequired": req.Requirements.AudioRequired}
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, e := a.decider.Decide(child, domain.DecisionRequest{ID: req.ID, Kind: kind, ProjectID: req.Project.ID, ShotID: shot.ID, Input: input})
	if callbackError(ctx, child, e) != nil {
		j.Code = "decision_error"
		return j
	}
	if !verdict(v.Decision, false) || math.IsNaN(v.Score) || math.IsInf(v.Score, 0) || v.Score < 0 || v.Score > 1 {
		j.Code = "invalid_judgment"
		return j
	}
	// The existing decision.Engine recomputes configurable policy. Preserve its
	// structured verdict, never infer classification from reason/prose.
	j.Verdict, j.Score, j.Code = Fact(v.Decision), v.Score, "classified"
	return j
}

func capabilityTags(caps []domain.PrimitiveCapabilities) []string {
	var tags []string
	for _, c := range caps {
		tags = append(tags, c.Tags...)
	}
	return unique(tags)
}
func unique(tags []string) []string {
	m := map[string]bool{}
	for _, t := range tags {
		m[t] = true
	}
	out := make([]string, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
