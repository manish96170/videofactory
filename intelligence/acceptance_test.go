package intelligence

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/decision"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/provider"
	"github.com/manish96170/videofactory/registry"
	"github.com/manish96170/videofactory/resource"
	"github.com/manish96170/videofactory/validation"
)

type acceptanceValidator func(context.Context, validation.Request) (domain.ValidationResult, error)

func (f acceptanceValidator) Validate(c context.Context, r validation.Request) (domain.ValidationResult, error) {
	return f(c, r)
}

type acceptanceRouter func(context.Context, provider.RouteRequest) (provider.RouteResult, error)

func (f acceptanceRouter) Route(c context.Context, r provider.RouteRequest) (provider.RouteResult, error) {
	return f(c, r)
}

type acceptanceDecider func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error)

func (f acceptanceDecider) Decide(c context.Context, r domain.DecisionRequest) (domain.DecisionResult, error) {
	return f(c, r)
}

type acceptancePlanner func(context.Context, resource.PlanRequest) (resource.PlanResult, error)

func (f acceptancePlanner) Plan(c context.Context, r resource.PlanRequest) (resource.PlanResult, error) {
	return f(c, r)
}

type acceptanceInspector func(context.Context, validation.InspectionRequest) (validation.InspectionFacts, error)

func (f acceptanceInspector) Inspect(c context.Context, r validation.InspectionRequest) (validation.InspectionFacts, error) {
	return f(c, r)
}

type acceptancePrimitives map[domain.PrimitiveType]domain.PrimitiveCapabilities

func (p acceptancePrimitives) Get(k domain.PrimitiveType) (domain.PrimitiveCapabilities, bool) {
	c, ok := p[k]
	return c, ok
}

type acceptanceProvider struct{ caps domain.ProviderCapabilities }

func (p *acceptanceProvider) ID() string { return p.caps.ProviderID }
func (p *acceptanceProvider) Capabilities(context.Context) (domain.ProviderCapabilities, error) {
	return p.caps, nil
}

// Independent literals and JSON oracles deliberately do not use production clone helpers.
func acceptanceRequest(human bool) Request {
	s := domain.Shot{ID: "shot", PlanID: "plan", Category: domain.ShotCategoryUI, Duration: 2 * time.Second, Deterministic: true,
		Primitives: []domain.Primitive{{ID: "bg", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#000000"}}, {ID: "title", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "PRIVATE_PROPS", "fontSize": 24}}}}
	if human {
		s.Category, s.Deterministic = domain.ShotCategoryHumanAction, false
		s.Primitives = []domain.Primitive{{ID: "human", Type: domain.PrimitiveTypeHumanAction, Properties: map[string]any{"prose": "PRIVATE_PROPS", "nested": map[string]any{"list": []any{"PRIVATE_PROPS"}}}}}
	}
	return Request{ID: "analysis", ShotID: "shot", Project: domain.Project{ID: "project", Name: "PRIVATE_PROSE", Plan: &domain.VideoPlan{ID: "plan", ProjectID: "project", FlowID: "flow", FramingID: "frame", CreatedAt: time.Unix(1, 0), Shots: []domain.Shot{s}}},
		Requirements: validation.Requirements{Kind: domain.AssetKindVideo, Quality: domain.QualityDraft, ExpectedDuration: 2 * time.Second, FPS: 24, Width: 640, Height: 360}}
}
func acceptanceBoundRequest() Request {
	r := acceptanceRequest(false)
	r.Bindings = []resource.PlanRequest{{ID: "analysis", TargetID: "shot", ProfileID: "profile", Workload: domain.Workload{Precision: "fp16", Width: 640, Height: 360, Frames: 48, Quality: "preview", Offload: "none"},
		Capabilities: domain.ModelCapabilities{ProviderID: "local", ModelID: "model", Local: true, Tags: []string{"deterministic", "renders-background", "renders-text"}, Requirements: &domain.ModelRequirements{Version: 1, Revision: "model-rev", ExecutionProfiles: []domain.ExecutionProfile{{ID: "profile", Revision: "profile-rev", Trusted: true, MemoryKnown: true, LoadRAMBytes: 1000, ResidentRAMBytes: 700, WorkspaceRAMBytes: 300, CPU: 1, Backend: domain.BackendNone,
			Envelope: domain.WorkloadEnvelope{Precisions: []string{"fp16"}, Width: domain.IntRange{Min: 640, Max: 640}, Height: domain.IntRange{Min: 360, Max: 360}, Frames: domain.IntRange{Min: 48, Max: 48}, Qualities: []string{"preview"}, Offloads: []string{"none"}}}}}}}}
	return r
}
func acceptanceNew(t *testing.T, v validation.Validator, rt Router, d Decider, p Planner) *Analyzer {
	t.Helper()
	a, err := New(registry.NewPrimitiveRegistry(), v, rt, d, p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func acceptanceUnusedValidator(t *testing.T) validation.Validator {
	return acceptanceValidator(func(context.Context, validation.Request) (domain.ValidationResult, error) {
		t.Error("unexpected validation")
		return domain.ValidationResult{}, errors.New("unexpected")
	})
}
func acceptanceUnusedRouter(t *testing.T) Router {
	return acceptanceRouter(func(context.Context, provider.RouteRequest) (provider.RouteResult, error) {
		t.Error("unexpected routing")
		return provider.RouteResult{}, errors.New("unexpected")
	})
}
func acceptanceNo(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
	return domain.DecisionResult{Decision: domain.DecisionNo, Score: 0, ReasonCode: "PRIVATE_REASON"}, nil
}
func acceptancePassed(r validation.Request) domain.ValidationResult {
	return domain.ValidationResult{ID: r.ID, TargetID: r.TargetID, Status: domain.ValidationPassed, Details: map[string]any{"quality": string(r.Requirements.Quality), "issues": []validation.Issue{}}}
}
func acceptanceJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
func acceptanceSafe(t *testing.T, v any) {
	t.Helper()
	if b := acceptanceJSON(t, v); strings.Contains(b, "PRIVATE_") {
		t.Errorf("private callback/input text escaped: %s", b)
	}
}
func acceptanceEngine(t *testing.T, table map[string]domain.DecisionResult) *decision.Engine {
	t.Helper()
	f, err := decision.NewFixtureProvider("fixture", table)
	if err != nil {
		t.Fatal(err)
	}
	reg := decision.NewRegistry()
	if err = reg.Register(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	cfg := decision.DefaultConfig("fixture")
	cfg.MaxAttempts, cfg.RetryDelay = 1, 0
	e, err := decision.NewEngine(reg, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func acceptanceRegister(t *testing.T, reg *provider.Registry, caps domain.ProviderCapabilities, healthy bool) {
	t.Helper()
	if err := reg.Register(context.Background(), &acceptanceProvider{caps}); err != nil {
		t.Fatal(err)
	}
	if healthy {
		if err := reg.SetHealth(context.Background(), caps.ProviderID, domain.ProviderHealthHealthy); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAcceptanceDeterministicAndRequestBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name           string
		typ            domain.PrimitiveType
		support, ready Fact
		tags           []string
	}{
		{"plain", "", Yes, Yes, []string{"deterministic", "renders-background", "renders-text"}},
		{"unsupported-exact-code", domain.PrimitiveTypeCodeEditor, No, Unknown, []string{"deterministic", "renders-code"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := acceptanceRequest(false)
			if tc.typ != "" {
				r.Project.Plan.Shots[0].Primitives = []domain.Primitive{{ID: "code", Type: tc.typ}}
			}
			decisions, routes := 0, 0
			a := acceptanceNew(t, acceptanceUnusedValidator(t), acceptanceRouter(func(_ context.Context, got provider.RouteRequest) (provider.RouteResult, error) {
				routes++
				if !reflect.DeepEqual(got.RequiredTags, tc.tags) {
					t.Errorf("tags=%v want=%v", got.RequiredTags, tc.tags)
				}
				return provider.RouteResult{}, provider.ErrNoEligibleProviders
			}), acceptanceDecider(func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				decisions++
				return domain.DecisionResult{Decision: domain.DecisionYes, Score: 1}, nil
			}), nil)
			out, err := a.Analyze(context.Background(), r)
			if err != nil || out.Strategy != Deterministic || out.ConceptualPossible != Yes || out.RenderedSupport != tc.support || out.InputsReady != tc.ready || decisions != 0 || routes != 1 || len(out.Judgments) != 0 {
				t.Fatalf("out=%+v decisions=%d routes=%d err=%v", out, decisions, routes, err)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Request)
	}{
		{"request-id", func(r *Request) { r.ID = " " }}, {"project-id", func(r *Request) { r.Project.ID = "" }},
		{"missing-plan", func(r *Request) { r.Project.Plan = nil }}, {"plan-project", func(r *Request) { r.Project.Plan.ProjectID = "other" }},
		{"shot-not-found", func(r *Request) { r.ShotID = "absent" }}, {"shot-plan", func(r *Request) { r.Project.Plan.Shots[0].PlanID = "other" }},
		{"asset-not-shot-dependency", func(r *Request) { r.Project.Plan.Shots[0].DependsOn = []domain.ID{"asset"} }},
		{"duration-exact", func(r *Request) { r.Requirements.ExpectedDuration++ }}, {"video-kind", func(r *Request) { r.Requirements.Kind = domain.AssetKindImage }},
		{"fps-zero", func(r *Request) { r.Requirements.FPS = 0 }}, {"fps-nan", func(r *Request) { r.Requirements.FPS = math.NaN() }},
		{"dimensions", func(r *Request) { r.Requirements.Width = 0 }}, {"quality", func(r *Request) { r.Requirements.Quality = "invented" }},
		{"duplicate-association", func(r *Request) { r.Candidates = []Candidate{{AssetID: "asset"}, {AssetID: "asset"}} }},
		{"candidate-cap", func(r *Request) { r.Candidates = make([]Candidate, 9) }}, {"binding-cap", func(r *Request) { r.Bindings = make([]resource.PlanRequest, 17) }},
		{"bad-hint", func(r *Request) { r.Candidates = []Candidate{{AssetID: "asset", Suitable: "PRIVATE_VERDICT"}} }},
		{"non-json-properties", func(r *Request) { r.Project.Plan.Shots[0].Primitives[0].Properties["callback"] = func() {} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := acceptanceRequest(false)
			tc.mutate(&r)
			a := acceptanceNew(t, acceptanceUnusedValidator(t), acceptanceUnusedRouter(t), nil, nil)
			out, err := a.Analyze(context.Background(), r)
			if !errors.Is(err, ErrInvalidRequest) || !reflect.DeepEqual(out, Result{}) {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if strings.Contains(err.Error(), "PRIVATE_") {
				t.Error("unsafe diagnostic")
			}
		})
	}
}

func TestAcceptanceAssociatedMeasuredAssets(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		kind                       domain.AssetKind
		candidate                  Candidate
		warning, audio, undersized bool
		strategy                   Strategy
		routes                     int
	}{
		{"video-pass", domain.AssetKindVideo, Candidate{Suitable: domain.DecisionYes}, false, true, false, ReuseVideo, 0},
		{"video-warning", domain.AssetKindVideo, Candidate{Suitable: domain.DecisionYes}, true, true, false, ReuseVideo, 0},
		{"static-image", domain.AssetKindImage, Candidate{ImageSufficient: domain.DecisionYes}, false, false, false, StaticImage, 1},
		{"static-image-audio", domain.AssetKindImage, Candidate{ImageSufficient: domain.DecisionYes}, false, true, false, StaticImage, 1},
		{"animate-image", domain.AssetKindImage, Candidate{ImageSufficient: domain.DecisionNo, AnimationSuitable: domain.DecisionYes}, false, false, false, AnimateImage, 1},
		{"image-too-small", domain.AssetKindImage, Candidate{ImageSufficient: domain.DecisionYes}, false, false, true, OpNone, 0},
		{"video-unsuitable", domain.AssetKindVideo, Candidate{Suitable: domain.DecisionNo}, false, false, false, GenerateVideo, 1},
		{"video-unknown", domain.AssetKindVideo, Candidate{}, false, false, false, OpNone, 0},
		{"image-uncertain", domain.AssetKindImage, Candidate{ImageSufficient: domain.DecisionUncertain, AnimationSuitable: domain.DecisionUncertain}, false, false, false, OpNone, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := acceptanceRequest(true)
			r.Requirements.Quality, r.Requirements.AudioRequired, r.Requirements.WarnUniformFrame = domain.QualityFinal, tc.audio, tc.warning
			if tc.audio && tc.kind == domain.AssetKindImage {
				r.AudioRequiredTags = []string{"composes-audio"}
			}
			r.Project.Assets = []domain.Asset{{ID: "asset", Kind: tc.kind, URI: "PRIVATE_URI.fixture", Metadata: map[string]any{"width": 9999, "PRIVATE_METADATA": "PRIVATE_METADATA"}}}
			tc.candidate.AssetID = "asset"
			r.Candidates = []Candidate{tc.candidate}
			inspectCalls, validateCalls, routes := 0, 0, 0
			checker, err := validation.NewMediaChecker(acceptanceInspector(func(_ context.Context, got validation.InspectionRequest) (validation.InspectionFacts, error) {
				inspectCalls++
				if got.URI != "PRIVATE_URI.fixture" || got.Kind != tc.kind {
					t.Errorf("wrong source: %+v", got)
				}
				f := validation.InspectionFacts{Kind: tc.kind, Duration: 2 * time.Second, FPS: 24, Width: 1280, Height: 720, CodecName: "png", VisualDecoded: true}
				if tc.kind == domain.AssetKindVideo {
					f.Width, f.Height, f.CodecName = 640, 360, "h264"
				} else {
					f.Duration, f.FPS = 0, 0
				}
				if tc.audio && tc.kind == domain.AssetKindVideo {
					f.AudioTracks, f.SampleRate, f.Channels, f.AudioDecoded = 1, 48000, 2, true
				}
				if tc.undersized {
					f.Width = 639
				}
				if tc.warning {
					uniform := true
					f.UniformFrame = &uniform
				}
				return f, nil
			}), validation.DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			v := acceptanceValidator(func(ctx context.Context, got validation.Request) (domain.ValidationResult, error) {
				validateCalls++
				want := r.Requirements
				if tc.kind == domain.AssetKindImage {
					want.Kind, want.ExpectedDuration, want.FPS, want.AudioRequired, want.ImageMinimumDimensions = domain.AssetKindImage, 0, 0, false, true
				}
				if got.ID != "analysis" || got.TargetID != "shot" || got.Asset.Metadata != nil || got.Requirements != want {
					t.Errorf("validation contract: %+v", got)
				}
				return checker.Validate(ctx, got)
			})
			d := acceptanceDecider(func(ctx context.Context, got domain.DecisionRequest) (domain.DecisionResult, error) {
				if got.Kind == "deterministic_possible" {
					return acceptanceNo(ctx, got)
				}
				return domain.DecisionResult{}, errors.New("PRIVATE_REASON unknown suitability")
			})
			a := acceptanceNew(t, v, acceptanceRouter(func(_ context.Context, got provider.RouteRequest) (provider.RouteResult, error) {
				routes++
				tags := []string{"ai-capable", "realistic-human-motion"}
				if tc.strategy == StaticImage {
					tags = []string{"deterministic", "renders-image"}
					if tc.audio {
						tags = []string{"composes-audio", "deterministic", "renders-image"}
					}
				}
				if tc.strategy == AnimateImage {
					tags = []string{"ai-capable", "image-animation"}
				}
				if !reflect.DeepEqual(got.RequiredTags, tags) {
					t.Errorf("operation tags=%v want=%v", got.RequiredTags, tags)
				}
				return provider.RouteResult{EligibleIDs: []string{"local"}}, nil
			}), d, nil)
			out, err := a.Analyze(context.Background(), r)
			if err != nil || out.Strategy != tc.strategy || validateCalls != 1 || inspectCalls != 1 || routes != tc.routes {
				t.Fatalf("out=%+v validation=%d inspection=%d routes=%d err=%v", out, validateCalls, inspectCalls, routes, err)
			}
			chosen := tc.strategy == ReuseVideo || tc.strategy == StaticImage || tc.strategy == AnimateImage
			if chosen && (out.InputsReady != Yes || out.SourceAssetID != "asset" || out.Assets[0].Code != "verified") {
				t.Error("verified asset not selected")
			}
			if !chosen && (out.InputsReady == Yes || out.SourceAssetID != "") {
				t.Error("unverified suitability manufactured readiness")
			}
			if tc.strategy == ReuseVideo && out.MissingAudioReadiness {
				t.Error("measured audio reuse requires no provider")
			}
			if tc.kind == domain.AssetKindImage && tc.audio && !out.MissingAudioReadiness {
				t.Error("image reuse manufactured audio readiness")
			}
			acceptanceSafe(t, out)
		})
	}
	for _, name := range []string{"no-global-search", "missing-association", "duplicate-asset", "blank-uri", "metadata-only", "callback-error", "wrong-id", "wrong-target", "wrong-quality", "unknown-status", "failed-media"} {
		t.Run(name, func(t *testing.T) {
			r := acceptanceRequest(true)
			r.Project.Assets = []domain.Asset{{ID: "asset", Kind: domain.AssetKindVideo, URI: "PRIVATE_URI.fixture", Metadata: map[string]any{"passed": true}}}
			r.Candidates = []Candidate{{AssetID: "asset", Suitable: domain.DecisionYes}}
			wantCalls := 1
			switch name {
			case "no-global-search":
				r.Candidates = nil
				wantCalls = 0
			case "missing-association":
				r.Candidates[0].AssetID = "missing"
				wantCalls = 0
			case "duplicate-asset":
				r.Project.Assets = append(r.Project.Assets, r.Project.Assets[0])
				wantCalls = 0
			case "blank-uri", "metadata-only":
				r.Project.Assets[0].URI = ""
				wantCalls = 0
			}
			calls := 0
			v := acceptanceValidator(func(_ context.Context, got validation.Request) (domain.ValidationResult, error) {
				calls++
				out := acceptancePassed(got)
				out.Details["PRIVATE_METADATA"] = "PRIVATE_REASON"
				switch name {
				case "callback-error":
					return out, errors.New("PRIVATE_REASON")
				case "wrong-id":
					out.ID = "other"
				case "wrong-target":
					out.TargetID = "other"
				case "wrong-quality":
					out.Details["quality"] = "final"
				case "unknown-status":
					out.Status = "PRIVATE_STATUS"
				case "failed-media":
					out.Status, out.FailureClass = domain.ValidationFailed, "media"
					out.Details["issues"] = []validation.Issue{{Code: "resolution_mismatch", Severity: "error"}}
				}
				return out, nil
			})
			// Unknown conceptual evidence keeps the unassociated case unresolved, not AI-ready.
			a := acceptanceNew(t, v, acceptanceUnusedRouter(t), nil, nil)
			out, err := a.Analyze(context.Background(), r)
			if err != nil || calls != wantCalls || out.Strategy != OpNone || out.SourceAssetID != "" || out.InputsReady == Yes || out.ResourceStatus != "unknown" {
				t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
			}
			if name == "callback-error" && out.Assets[0].Validation != nil {
				t.Error("error-accompanying validation payload retained")
			}
			acceptanceSafe(t, out)
		})
	}
}

func TestAcceptanceStructuredDecisionEngine(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		deterministic, image, animation float64
		strategy                        Strategy
		verdict                         Fact
		kinds                           []string
		asset                           bool
	}{
		{"conceptual-yes", .95, 0, 0, Deterministic, Yes, []string{"deterministic_possible"}, false},
		{"conceptual-no", .1, 0, 0, GenerateVideo, No, []string{"deterministic_possible"}, false},
		{"conceptual-uncertain", .5, 0, 0, OpNone, Uncertain, []string{"deterministic_possible"}, false},
		{"image-yes", .1, .95, 0, StaticImage, No, []string{"deterministic_possible", "image_sufficient"}, true},
		{"animation-yes", .1, .1, .95, AnimateImage, No, []string{"deterministic_possible", "image_sufficient", "image_animation_suitable"}, true},
		{"image-uncertain-animation-no", .1, .5, .1, OpNone, No, []string{"deterministic_possible", "image_sufficient", "image_animation_suitable"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := acceptanceRequest(true)
			if tc.asset {
				r.Project.Assets = []domain.Asset{{ID: "image", Kind: domain.AssetKindImage, URI: "PRIVATE_URI", Metadata: map[string]any{"judgment": "yes"}}}
				r.Candidates = []Candidate{{AssetID: "image"}}
			}
			table := map[string]domain.DecisionResult{}
			for k, score := range map[string]float64{"deterministic_possible": tc.deterministic, "image_sufficient": tc.image, "image_animation_suitable": tc.animation} {
				table[k] = domain.DecisionResult{Decision: domain.DecisionNo, Score: score, ReasonCode: "PRIVATE_REASON"}
			}
			engine := acceptanceEngine(t, table)
			var kinds []string
			d := acceptanceDecider(func(ctx context.Context, got domain.DecisionRequest) (domain.DecisionResult, error) {
				kinds = append(kinds, got.Kind)
				want := map[string]any{"durationNs": float64(2 * time.Second), "primitiveCount": float64(1), "hasCandidate": got.Kind != "deterministic_possible", "width": float64(640), "height": float64(360), "fps": float64(24), "audioRequired": false}
				if got.ID != "analysis" || got.ProjectID != "project" || got.ShotID != "shot" || !reflect.DeepEqual(got.Input, want) {
					t.Errorf("unsafe/non-scalar descriptor: %+v", got)
				}
				acceptanceSafe(t, got)
				return engine.Decide(ctx, got)
			})
			v := acceptanceValidator(func(_ context.Context, got validation.Request) (domain.ValidationResult, error) {
				return acceptancePassed(got), nil
			})
			a := acceptanceNew(t, v, acceptanceRouter(func(context.Context, provider.RouteRequest) (provider.RouteResult, error) {
				return provider.RouteResult{EligibleIDs: []string{"local"}}, nil
			}), d, nil)
			out, err := a.Analyze(context.Background(), r)
			if err != nil || out.Strategy != tc.strategy || out.ConceptualPossible != tc.verdict || !reflect.DeepEqual(kinds, tc.kinds) {
				t.Fatalf("out=%+v kinds=%v err=%v", out, kinds, err)
			}
			for _, j := range out.Judgments {
				if j.Code != "classified" || j.Score < 0 || j.Score > 1 || math.IsNaN(j.Score) || math.IsInf(j.Score, 0) {
					t.Errorf("unsafe judgment=%+v", j)
				}
			}
			acceptanceSafe(t, out)
		})
	}
	for _, tc := range []struct {
		name   string
		result domain.DecisionResult
		err    error
	}{
		{"nan", domain.DecisionResult{Decision: domain.DecisionYes, Score: math.NaN()}, nil}, {"infinite", domain.DecisionResult{Decision: domain.DecisionYes, Score: math.Inf(1)}, nil},
		{"negative", domain.DecisionResult{Decision: domain.DecisionYes, Score: -.1}, nil}, {"above-one", domain.DecisionResult{Decision: domain.DecisionYes, Score: 1.1}, nil},
		{"unknown-verdict", domain.DecisionResult{Decision: "PRIVATE_VERDICT", Score: 1}, nil}, {"error-with-yes", domain.DecisionResult{Decision: domain.DecisionYes, Score: 1}, errors.New("PRIVATE_REASON")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err == nil {
				if _, err := decision.NewFixtureProvider("fixture", map[string]domain.DecisionResult{"deterministic_possible": tc.result}); !errors.Is(err, decision.ErrMalformedResult) {
					t.Fatalf("fixture accepted malformed judgment: %v", err)
				}
			}
			calls := 0
			a := acceptanceNew(t, acceptanceUnusedValidator(t), acceptanceUnusedRouter(t), acceptanceDecider(func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				calls++
				return tc.result, tc.err
			}), nil)
			out, err := a.Analyze(context.Background(), acceptanceRequest(true))
			if err != nil || calls != 1 || out.Strategy != OpNone || out.ConceptualPossible != Unknown || len(out.Judgments) != 1 || out.Judgments[0].Score != 0 {
				t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
			}
			acceptanceSafe(t, out)
		})
	}
	t.Run("real-engine-unsupported-kind", func(t *testing.T) {
		a := acceptanceNew(t, acceptanceUnusedValidator(t), acceptanceUnusedRouter(t), acceptanceEngine(t, map[string]domain.DecisionResult{"other": {Decision: domain.DecisionYes, Score: 1}}), nil)
		out, err := a.Analyze(context.Background(), acceptanceRequest(true))
		if err != nil || out.Strategy != OpNone || out.Judgments[0].Code != "decision_error" {
			t.Fatalf("out=%+v err=%v", out, err)
		}
	})
}

func TestAcceptanceRealRoutingAndResourceAdvice(t *testing.T) {
	t.Run("all-routing-gates", func(t *testing.T) {
		r := acceptanceRequest(true)
		memory, gpu, latency := 4.0, false, int64(100)
		r.Requirements.AudioRequired = true
		r.AudioRequiredTags = []string{"composes-audio"}
		r.Route = provider.RouteRequest{RequiredTags: []string{"caller-tag"}, PreferredIDs: []string{"unhealthy", "remote", "local"}, MaxCost: &provider.CostLimit{Amount: 1, Unit: "unit"}, MaxLatencyMs: &latency, Hardware: provider.HardwareFacts{AvailableMemoryGB: &memory, HasGPU: &gpu}}
		reg := provider.NewRegistry()
		for _, id := range []string{"local", "remote", "unhealthy", "missing-tag", "wrong-unit", "cost", "unknown-cost", "latency", "unknown-latency", "memory", "gpu", "unknown-hardware"} {
			ms := int64(100)
			caps := domain.ProviderCapabilities{ProviderID: id, Local: true, Tags: []string{"ai-capable", "realistic-human-motion", "caller-tag", "composes-audio"}, CostUnit: "unit", CostPerUnit: 1, EstimatedLatencyMs: &ms, Hardware: &domain.HardwareRequirements{MinMemoryGB: 4}}
			switch id {
			case "remote":
				caps.Local = false
			case "missing-tag":
				caps.Tags = caps.Tags[:3]
			case "wrong-unit":
				caps.CostUnit = "other"
			case "cost":
				caps.CostPerUnit = 1.01
			case "unknown-cost":
				caps.CostPerUnit, caps.CostUnit = 0, ""
			case "latency":
				ms = 101
			case "unknown-latency":
				caps.EstimatedLatencyMs = nil
			case "memory":
				caps.Hardware.MinMemoryGB = 4.01
			case "gpu":
				caps.Hardware.RequiresGPU = true
			case "unknown-hardware":
				caps.Hardware = nil
			}
			acceptanceRegister(t, reg, caps, id != "unhealthy")
		}
		router, err := provider.NewRouter(reg)
		if err != nil {
			t.Fatal(err)
		}
		a := acceptanceNew(t, acceptanceUnusedValidator(t), router, acceptanceDecider(acceptanceNo), nil)
		for _, locality := range []provider.Locality{"", provider.AnyLocality, provider.RemoteOnly} {
			r.Route.Locality = locality
			out, err := a.Analyze(context.Background(), r)
			want := []string{"local"}
			if locality == provider.AnyLocality {
				want = []string{"remote", "local"}
			}
			if locality == provider.RemoteOnly {
				want = []string{"remote"}
			}
			if err != nil || out.Routing == nil || !reflect.DeepEqual(out.Routing.EligibleIDs, want) || !reflect.DeepEqual(out.RequiredTags, []string{"ai-capable", "caller-tag", "composes-audio", "realistic-human-motion"}) || !out.MissingAudioReadiness || out.ResourceStatus != "unknown" {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if locality == "" {
				codes := map[string]provider.RejectionCode{"remote": provider.RejectLocality, "unhealthy": provider.RejectHealth, "missing-tag": provider.RejectTags, "wrong-unit": provider.RejectCostUnit, "cost": provider.RejectCostLimit, "unknown-cost": provider.RejectCostUnknown, "latency": provider.RejectLatencyLimit, "unknown-latency": provider.RejectLatencyUnknown, "memory": provider.RejectMemoryLimit, "gpu": provider.RejectGPUUnavailable, "unknown-hardware": provider.RejectHardwareUnknown}
				for _, rej := range out.Routing.Rejections {
					if !reflect.DeepEqual(rej.Codes, []provider.RejectionCode{codes[rej.ProviderID]}) {
						t.Errorf("gate %s codes=%v", rej.ProviderID, rej.Codes)
					}
					delete(codes, rej.ProviderID)
				}
				if len(codes) != 0 {
					t.Errorf("missing rejections=%v", codes)
				}
			}
		}
	})
	for _, name := range []string{"ready", "history", "held-slot", "permanent-memory", "permanent-cpu", "permanent-backend", "missing-profile", "any-unproven", "remote-unproven"} {
		t.Run(name, func(t *testing.T) {
			r := acceptanceBoundRequest()
			cfg := resource.ReferenceConfig(4, 2, resource.AcceleratorFacts{Known: true, Backend: domain.BackendNone}, 0, "hardware")
			cfg.MaxParallel, cfg.MaxHeavy = 1, 1
			wantStatus, wantRAM := "local_ready", int64(1250)
			switch name {
			case "permanent-memory":
				cfg.WorkerRAMBytes = 1249
				wantStatus = "local_ineligible"
			case "permanent-cpu":
				cfg.WorkerCPU = .5
				wantStatus = "local_ineligible"
			case "permanent-backend":
				p := &r.Bindings[0].Capabilities.Requirements.ExecutionProfiles[0]
				p.GPU, p.Backend = 1, domain.BackendCUDA
				wantStatus = "local_ineligible"
			case "missing-profile":
				r.Bindings[0].ProfileID = "missing"
				wantStatus = "unknown"
			case "any-unproven":
				r.Route.Locality = provider.AnyLocality
				wantStatus = "unknown"
			case "remote-unproven":
				r.Route.Locality = provider.RemoteOnly
				r.Bindings[0].Capabilities.Local = false
				wantStatus = "unknown"
			}
			m, err := resource.NewManager(cfg)
			if err != nil {
				t.Fatal(err)
			}
			h, err := resource.NewHistory(4)
			if err != nil {
				t.Fatal(err)
			}
			if name == "history" {
				b := r.Bindings[0]
				err = h.Record(resource.HistoryKey{ProviderID: "local", ModelID: "model", ModelRevision: "model-rev", ProfileID: "profile", ProfileRevision: "profile-rev", HardwareClass: "hardware", Workload: b.Workload}, resource.Measurement{FamilyRSS: &resource.MemoryPeak{Bytes: 2000, Source: resource.SourceNativeFamilyRSS, Completeness: resource.MeasurementComplete}})
				if err != nil {
					t.Fatal(err)
				}
				wantRAM = 2500
			}
			if name == "held-slot" { // The sole deliberate acquisition is a test-owned blocker, never intelligence work.
				lease, err := m.Acquire(context.Background(), resource.Request{RAMBytes: 1, Backend: domain.BackendNone})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := lease.Release(); err != nil {
						t.Error(err)
					}
				}()
				wantStatus = "wait"
			}
			planner, err := resource.NewPlanner(m, h, resource.DefaultPlannerConfig())
			if err != nil {
				t.Fatal(err)
			}
			reg := provider.NewRegistry()
			acceptanceRegister(t, reg, domain.ProviderCapabilities{ProviderID: "local", Local: name != "remote-unproven", Tags: []string{"deterministic", "renders-background", "renders-text"}, Hardware: &domain.HardwareRequirements{}}, true)
			router, err := provider.NewRouter(reg)
			if err != nil {
				t.Fatal(err)
			}
			before, historyBefore := m.Snapshot(), acceptanceJSON(t, h.Snapshot())
			calls := 0
			a := acceptanceNew(t, acceptanceUnusedValidator(t), router, nil, acceptancePlanner(func(ctx context.Context, req resource.PlanRequest) (resource.PlanResult, error) {
				calls++
				return planner.Plan(ctx, req)
			}))
			out, err := a.Analyze(context.Background(), r)
			if err != nil || out.ResourceStatus != wantStatus || out.Routing == nil || !reflect.DeepEqual(out.Routing.EligibleIDs, []string{"local"}) {
				t.Fatalf("out=%+v err=%v", out, err)
			}
			if wantStatus == "unknown" {
				if calls != 0 || out.Resource != nil {
					t.Error("unproven profile/locality admitted")
				}
			} else {
				if calls != 1 || out.Resource == nil || out.Resource.Plan.RAMBytes != wantRAM || out.Resource.Plan.Rationale != wantStatus || out.Resource.Plan.Provenance.HardwareClass != "" {
					t.Fatalf("resource evidence=%+v calls=%d", out.Resource, calls)
				}
				if name == "history" && (out.Resource.Plan.Provenance.ObservedPeakRAMBytes == nil || *out.Resource.Plan.Provenance.ObservedPeakRAMBytes != 2000 || out.Resource.Plan.Provenance.HistoryVersion != 1 || len(out.Resource.Plan.Provenance.HistoryHash) != 64) {
					t.Error("historical high-water provenance lost")
				}
			}
			if !reflect.DeepEqual(before, m.Snapshot()) || historyBefore != acceptanceJSON(t, h.Snapshot()) {
				t.Error("advisory analysis changed budget/history")
			}
		})
	}
}

func TestAcceptanceCancellationAndOwnership(t *testing.T) {
	t.Run("typed-nil-and-nil-context", func(t *testing.T) {
		var primitives *registry.PrimitiveRegistry
		var validator acceptanceValidator
		var router acceptanceRouter
		var decider acceptanceDecider
		var planner acceptancePlanner
		validV, validR := acceptanceUnusedValidator(t), acceptanceUnusedRouter(t)
		for _, args := range []struct {
			p Primitives
			v validation.Validator
			r Router
		}{{primitives, validV, validR}, {registry.NewPrimitiveRegistry(), validator, validR}, {registry.NewPrimitiveRegistry(), validV, router}} {
			if a, err := New(args.p, args.v, args.r, nil, nil); a != nil || !errors.Is(err, ErrInvalidDependency) {
				t.Errorf("typed nil accepted: %v", err)
			}
		}
		a := acceptanceNew(t, validV, validR, decider, planner)
		if out, err := a.Analyze(nil, acceptanceRequest(false)); !errors.Is(err, ErrInvalidRequest) || !reflect.DeepEqual(out, Result{}) {
			t.Errorf("nil context=%+v %v", out, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if out, err := a.Analyze(ctx, acceptanceRequest(false)); err != context.Canceled || !reflect.DeepEqual(out, Result{}) {
			t.Errorf("pre-canceled=%+v %v", out, err)
		}
	})
	for _, stage := range []string{"decision", "validation", "routing", "planning"} {
		t.Run("parent-cancel-"+stage, func(t *testing.T) {
			r := acceptanceBoundRequest()
			if stage == "decision" || stage == "validation" {
				r = acceptanceRequest(true)
			}
			if stage == "validation" {
				r.Project.Assets = []domain.Asset{{ID: "asset", Kind: domain.AssetKindVideo, URI: "PRIVATE_URI"}}
				r.Candidates = []Candidate{{AssetID: "asset", Suitable: domain.DecisionYes}}
			}
			type acceptanceContextKey struct{}
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), acceptanceContextKey{}, "token"))
			defer cancel()
			entered := make(chan struct{})
			calls := 0
			barrier := func(child context.Context) {
				calls++
				if child.Value(acceptanceContextKey{}) != "token" {
					t.Error("context ancestry lost")
				}
				close(entered)
				<-child.Done()
			}
			v := acceptanceValidator(func(c context.Context, req validation.Request) (domain.ValidationResult, error) {
				barrier(c)
				return acceptancePassed(req), nil
			})
			rt := acceptanceRouter(func(c context.Context, _ provider.RouteRequest) (provider.RouteResult, error) {
				if stage == "routing" {
					barrier(c)
				}
				return provider.RouteResult{EligibleIDs: []string{"local"}}, nil
			})
			d := acceptanceDecider(func(c context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
				if stage == "decision" {
					barrier(c)
					return domain.DecisionResult{Decision: domain.DecisionYes, Score: 1}, nil
				}
				return acceptanceNo(c, req)
			})
			p := acceptancePlanner(func(c context.Context, _ resource.PlanRequest) (resource.PlanResult, error) {
				barrier(c)
				return resource.PlanResult{Status: resource.StatusLocalReady}, nil
			})
			a := acceptanceNew(t, v, rt, d, p)
			type acceptanceReply struct {
				out Result
				err error
			}
			done := make(chan acceptanceReply, 1)
			go func() { out, err := a.Analyze(ctx, r); done <- acceptanceReply{out, err} }()
			select {
			case <-entered:
				cancel()
			case <-time.After(10 * time.Second):
				cancel()
				t.Error("callback barrier not reached")
			}
			select {
			case reply := <-done:
				if reply.err != context.Canceled || !reflect.DeepEqual(reply.out, Result{}) || calls != 1 {
					t.Errorf("late success after parent cancellation: %+v calls=%d", reply, calls)
				}
			case <-time.After(10 * time.Second):
				t.Error("Analyze did not join cooperative callback")
			}
		})
	}
	for _, stage := range []string{"decision", "validation", "routing", "planning"} {
		t.Run("child-late-success-"+stage, func(t *testing.T) {
			r := acceptanceBoundRequest()
			if stage == "decision" || stage == "validation" {
				r = acceptanceRequest(true)
			}
			if stage == "validation" {
				r.Project.Assets = []domain.Asset{{ID: "asset", Kind: domain.AssetKindVideo, URI: "PRIVATE_URI"}}
				r.Candidates = []Candidate{{AssetID: "asset", Suitable: domain.DecisionYes}}
			}
			calls := 0
			wait := func(c context.Context) {
				calls++
				<-c.Done()
				if c.Err() != context.DeadlineExceeded {
					t.Errorf("child error=%v", c.Err())
				}
			}
			v := acceptanceValidator(func(c context.Context, req validation.Request) (domain.ValidationResult, error) {
				wait(c)
				return acceptancePassed(req), nil
			})
			d := acceptanceDecider(func(c context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
				if stage == "decision" {
					wait(c)
					return domain.DecisionResult{Decision: domain.DecisionYes, Score: 1}, nil
				}
				return acceptanceNo(c, req)
			})
			rt := acceptanceRouter(func(c context.Context, _ provider.RouteRequest) (provider.RouteResult, error) {
				if stage == "routing" {
					wait(c)
				}
				return provider.RouteResult{EligibleIDs: []string{"local"}}, nil
			})
			p := acceptancePlanner(func(c context.Context, _ resource.PlanRequest) (resource.PlanResult, error) {
				wait(c)
				return resource.PlanResult{Status: resource.StatusLocalReady}, nil
			})
			a := acceptanceNew(t, v, rt, d, p)
			out, err := a.Analyze(context.Background(), r)
			if err != nil || calls != 1 || out.Resource != nil || out.ResourceStatus != "unknown" {
				t.Fatalf("out=%+v calls=%d err=%v", out, calls, err)
			}
			switch stage {
			case "decision":
				if out.Strategy != OpNone || out.Judgments[0].Code != "decision_error" {
					t.Error("late judgment accepted")
				}
			case "validation":
				if out.Strategy != OpNone || out.Assets[0].Validation != nil || out.Assets[0].Code != "validation_error" {
					t.Error("late validation accepted")
				}
			case "routing":
				if out.Routing != nil || out.Code != "routing_error" {
					t.Error("late route accepted")
				}
			case "planning":
				if out.Code != "resource_unknown" {
					t.Error("late plan accepted")
				}
			}
		})
	}
	t.Run("independent-inputs-and-return-evidence", func(t *testing.T) {
		r := acceptanceBoundRequest()
		memory, gpu, latency := 4.0, false, int64(100)
		r.Route = provider.RouteRequest{RequiredTags: []string{"caller"}, PreferredIDs: []string{"local"}, MaxCost: &provider.CostLimit{Amount: 1, Unit: "unit"}, MaxLatencyMs: &latency, Hardware: provider.HardwareFacts{AvailableMemoryGB: &memory, HasGPU: &gpu}}
		r.Bindings[0].Capabilities.Tags = append(r.Bindings[0].Capabilities.Tags, "caller")
		before := acceptanceJSON(t, r)
		cfg := resource.ReferenceConfig(4, 2, resource.AcceleratorFacts{Known: true, Backend: domain.BackendNone}, 0, "hardware")
		m, err := resource.NewManager(cfg)
		if err != nil {
			t.Fatal(err)
		}
		realPlanner, err := resource.NewPlanner(m, nil, resource.DefaultPlannerConfig())
		if err != nil {
			t.Fatal(err)
		}
		budgetBefore := m.Snapshot()
		caps := domain.PrimitiveCapabilities{Type: domain.PrimitiveTypeBackground, Deterministic: true, Tags: []string{"deterministic", "renders-background"}}
		primitives := acceptancePrimitives{domain.PrimitiveTypeBackground: caps, domain.PrimitiveTypeText: {Type: domain.PrimitiveTypeText, Deterministic: true, RequiredProperties: []string{"text"}, Tags: []string{"deterministic", "renders-text"}}}
		var callbackRoute provider.RouteResult
		var callbackPlan resource.PlanResult
		routeCalls, planCalls := 0, 0
		rt := acceptanceRouter(func(_ context.Context, got provider.RouteRequest) (provider.RouteResult, error) {
			routeCalls++
			if got.MaxCost.Amount != 1 || got.RequiredTags[0] != "caller" || got.PreferredIDs[0] != "local" || *got.MaxLatencyMs != 100 || *got.Hardware.AvailableMemoryGB != 4 || *got.Hardware.HasGPU {
				t.Error("route input mutated across calls")
			}
			// First call probes internal policy isolation. The second leaves the
			// tags intact so downstream ownership checks still run if that probe fails.
			if routeCalls == 1 {
				got.RequiredTags[0] = "mutated"
			}
			got.PreferredIDs[0], got.MaxCost.Amount, *got.MaxLatencyMs, *got.Hardware.AvailableMemoryGB, *got.Hardware.HasGPU = "mutated", 99, 999, 0, true
			callbackRoute = provider.RouteResult{EligibleIDs: []string{"local", "fallback"}, FallbackIDs: []string{"fallback"}, Rejections: []provider.Rejection{{ProviderID: "blocked", Codes: []provider.RejectionCode{provider.RejectHealth}}}}
			return callbackRoute, nil
		})
		p := acceptancePlanner(func(ctx context.Context, got resource.PlanRequest) (resource.PlanResult, error) {
			planCalls++
			if got.Capabilities.Tags[0] != "deterministic" || got.Capabilities.Requirements.ExecutionProfiles[0].Envelope.Precisions[0] != "fp16" {
				t.Error("binding input mutated across calls")
			}
			out, err := realPlanner.Plan(ctx, got)
			got.Capabilities.Tags[0] = "mutated"
			got.Capabilities.Requirements.ExecutionProfiles[0].Envelope.Precisions[0] = "mutated"
			out.Plan.Rationale = "PRIVATE_REASON"
			callbackPlan = out
			return out, err
		})
		a, err := New(primitives, acceptanceUnusedValidator(t), rt, nil, p)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			out, err := a.Analyze(context.Background(), r)
			if _, ok := r.Project.Plan.Shots[0].Primitives[1].Properties["fontSize"].(int); !ok {
				t.Error("request snapshot mutated the caller's original primitive property type")
			}
			if err != nil || out.Resource == nil || out.ResourceStatus != "local_ready" {
				t.Errorf("router input mutation changed trusted downstream admission: out=%+v err=%v", out, err)
				if before != acceptanceJSON(t, r) || !reflect.DeepEqual(budgetBefore, m.Snapshot()) {
					t.Error("callback mutation changed original request/budget")
				}
				continue
			}
			acceptanceSafe(t, out)
			callbackRoute.EligibleIDs[0], callbackRoute.FallbackIDs[0], callbackRoute.Rejections[0].Codes[0] = "changed", "changed", provider.RejectTags
			callbackPlan.Plan.Provenance.ProfileID = "changed"
			if out.Routing.EligibleIDs[0] != "local" || out.Routing.FallbackIDs[0] != "fallback" || out.Routing.Rejections[0].Codes[0] != provider.RejectHealth || out.Resource.Plan.Provenance.ProfileID != "profile" {
				t.Error("callback output aliases returned evidence")
			}
			out.RequiredTags[0], out.Routing.EligibleIDs[0], out.Resource.Plan.Provenance.ModelID = "changed", "changed", "changed"
			if before != acceptanceJSON(t, r) {
				t.Error("planner callback input mutation changed the original request binding")
			}
			if caps.Tags[0] != "deterministic" || !reflect.DeepEqual(budgetBefore, m.Snapshot()) {
				t.Error("evidence/input mutation changed original primitives/budget")
			}
		}
		if routeCalls != 2 || planCalls != 2 {
			t.Errorf("calls=%d/%d", routeCalls, planCalls)
		}
	})
	t.Run("validation-and-decision-isolation-error-payloads", func(t *testing.T) {
		r := acceptanceRequest(true)
		r.Project.Assets = []domain.Asset{{ID: "asset", Kind: domain.AssetKindVideo, URI: "PRIVATE_URI", Metadata: map[string]any{"nested": map[string]any{"private": "PRIVATE_METADATA"}}}}
		r.Candidates = []Candidate{{AssetID: "asset"}}
		before := acceptanceJSON(t, r)
		decisions, validations := 0, 0
		var external domain.ValidationResult
		d := acceptanceDecider(func(_ context.Context, got domain.DecisionRequest) (domain.DecisionResult, error) {
			decisions++
			if got.Input["primitiveCount"] != float64(1) || len(got.Input) != 7 {
				t.Error("decision input aliases other calls")
			}
			got.Input["primitiveCount"], got.Input["PRIVATE_METADATA"] = float64(999), "PRIVATE_REASON"
			if got.Kind == "deterministic_possible" {
				return domain.DecisionResult{Decision: domain.DecisionNo, Score: 0, ReasonCode: "PRIVATE_REASON"}, nil
			}
			return domain.DecisionResult{Decision: domain.DecisionYes, Score: 1, ReasonCode: "PRIVATE_REASON"}, context.Canceled
		})
		v := acceptanceValidator(func(_ context.Context, got validation.Request) (domain.ValidationResult, error) {
			validations++
			if got.Asset.Metadata != nil || got.Asset.URI != "PRIVATE_URI" {
				t.Error("metadata leaked or source mutated")
			}
			got.Asset.URI = "mutated"
			external = acceptancePassed(got)
			external.Details["extra"] = "PRIVATE_METADATA"
			return external, nil
		})
		a := acceptanceNew(t, v, acceptanceUnusedRouter(t), d, nil)
		for i := 0; i < 2; i++ {
			out, err := a.Analyze(context.Background(), r)
			if err != nil || out.Strategy != OpNone || len(out.Judgments) != 2 || out.Judgments[1].Verdict != Unknown || out.Judgments[1].Code != "decision_error" {
				t.Fatalf("error payload promoted: %+v %v", out, err)
			}
			acceptanceSafe(t, out)
			external.Details["quality"] = "changed"
			if out.Assets[0].Validation.Details["quality"] != "draft" {
				t.Error("validation evidence aliases callback output")
			}
			out.Assets[0].Validation.Details["quality"] = "changed"
			if before != acceptanceJSON(t, r) {
				t.Error("original project/primitives/asset changed")
			}
		}
		if decisions != 4 || validations != 2 {
			t.Errorf("unexpected retries/counts=%d/%d", decisions, validations)
		}
	})
}
