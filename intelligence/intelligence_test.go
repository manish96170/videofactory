package intelligence

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/director"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
	"github.com/manish96170/videofactory/provider"
	"github.com/manish96170/videofactory/registry"
	"github.com/manish96170/videofactory/resource"
	"github.com/manish96170/videofactory/validation"
)

type checkFunc func(context.Context, validation.Request) (domain.ValidationResult, error)

func (f checkFunc) Validate(ctx context.Context, req validation.Request) (domain.ValidationResult, error) {
	return f(ctx, req)
}

type routeFunc func(context.Context, provider.RouteRequest) (provider.RouteResult, error)

func (f routeFunc) Route(ctx context.Context, req provider.RouteRequest) (provider.RouteResult, error) {
	return f(ctx, req)
}

type decideFunc func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error)

func (f decideFunc) Decide(ctx context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
	return f(ctx, req)
}

type planFunc func(context.Context, resource.PlanRequest) (resource.PlanResult, error)

func (f planFunc) Plan(ctx context.Context, req resource.PlanRequest) (resource.PlanResult, error) {
	return f(ctx, req)
}

func plainRequest(t *testing.T) Request {
	t.Helper()
	p := project.New("fixture", time.Unix(1, 0))
	d := director.NewFixtureDirector(registry.NewFlowRegistry(), registry.NewFramingRegistry(), registry.NewPrimitiveRegistry())
	planned, err := d.Plan(context.Background(), director.PlanRequest{ProjectID: p.ID, Prompt: "A plain title."})
	if err != nil {
		t.Fatal(err)
	}
	p.Plan = &planned.Plan
	p.Plan.Shots[0].Duration = 2 * time.Second
	if err := project.ValidatePlan(p.ID, *p.Plan); err != nil {
		t.Fatal(err)
	}
	return Request{ID: "analysis", Project: p, ShotID: p.Plan.Shots[0].ID, Requirements: validation.Requirements{Kind: domain.AssetKindVideo, Quality: domain.QualityDraft, ExpectedDuration: 2 * time.Second, FPS: 24, Width: 640, Height: 360}}
}
func humanRequest(t *testing.T) Request {
	t.Helper()
	r := plainRequest(t)
	r.Project.Plan.Shots[0].Primitives = []domain.Primitive{{ID: "human", Type: domain.PrimitiveTypeHumanAction}}
	r.Project.Plan.Shots[0].Category = domain.ShotCategoryHumanAction
	r.Project.Plan.Shots[0].Deterministic = false
	return r
}
func mustAnalyzer(t *testing.T, v validation.Validator, r Router, d Decider, p Planner) *Analyzer {
	t.Helper()
	a, e := New(registry.NewPrimitiveRegistry(), v, r, d, p)
	if e != nil {
		t.Fatal(e)
	}
	return a
}
func unusedValidator(t *testing.T) validation.Validator {
	return checkFunc(func(context.Context, validation.Request) (domain.ValidationResult, error) {
		t.Error("unexpected validation")
		return domain.ValidationResult{}, nil
	})
}
func noDecision(ctx context.Context, r domain.DecisionRequest) (domain.DecisionResult, error) {
	return domain.DecisionResult{Decision: domain.DecisionNo, Score: 0}, nil
}
func passed(req validation.Request) domain.ValidationResult {
	return domain.ValidationResult{ID: req.ID, TargetID: req.TargetID, Status: domain.ValidationPassed, Details: map[string]any{"quality": string(req.Requirements.Quality), "issues": []validation.Issue{}}}
}

// Already-green integration smoke, not claimed as a red/green regression.
func TestFixtureDirectorCheapPath(t *testing.T) {
	r := plainRequest(t)
	calls := 0
	a := mustAnalyzer(t, unusedValidator(t), routeFunc(func(ctx context.Context, r provider.RouteRequest) (provider.RouteResult, error) {
		calls++
		if !reflect.DeepEqual(r.RequiredTags, []string{"deterministic", "renders-background", "renders-text"}) {
			t.Errorf("tags=%v", r.RequiredTags)
		}
		return provider.RouteResult{EligibleIDs: []string{"local"}}, nil
	}), nil, nil)
	out, e := a.Analyze(context.Background(), r)
	if e != nil || out.Strategy != Deterministic || out.ConceptualPossible != Yes || out.RenderedSupport != Yes || out.InputsReady != Yes || len(out.Judgments) != 0 || calls != 1 || out.ResourceStatus != "unknown" {
		t.Fatalf("out=%+v calls=%d error=%v", out, calls, e)
	}
}

func TestAudioIntentIsAnExplicitRoutingGate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		audio     []string
		wantCalls int
	}{
		{"missing", nil, 0}, {"explicit", []string{"fixture-audio-composition"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := humanRequest(t)
			r.Requirements.AudioRequired = true
			r.AudioRequiredTags = tc.audio
			memory, gpu, latency := 4.0, false, int64(500)
			r.Route = provider.RouteRequest{RequiredTags: []string{"caller-constraint"}, PreferredIDs: []string{"chosen"}, Locality: provider.LocalOnly, MaxCost: &provider.CostLimit{Amount: 0, Unit: "fixture/unit"}, MaxLatencyMs: &latency, Hardware: provider.HardwareFacts{AvailableMemoryGB: &memory, HasGPU: &gpu}}
			calls := 0
			a := mustAnalyzer(t, unusedValidator(t), routeFunc(func(ctx context.Context, got provider.RouteRequest) (provider.RouteResult, error) {
				calls++
				want := r.Route.Clone()
				want.RequiredTags = []string{"ai-capable", "caller-constraint", "fixture-audio-composition", "realistic-human-motion"}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("routing constraints/tags changed: got=%+v want=%+v", got, want)
				}
				return provider.RouteResult{EligibleIDs: []string{"chosen"}}, nil
			}), decideFunc(noDecision), nil)
			out, e := a.Analyze(context.Background(), r)
			if e != nil || calls != tc.wantCalls || out.Strategy != GenerateVideo || !out.MissingAudioReadiness {
				t.Fatalf("audio gate out=%+v calls=%d error=%v", out, calls, e)
			}
			if calls == 0 && (out.Routing != nil || out.Code != "audio_composition_required") {
				t.Fatalf("unproven audio routed: %+v", out)
			}
		})
	}
}

func TestValidationUnknownSeverityIsScrubbed(t *testing.T) {
	for _, severity := range []string{"PRIVATE", "warning"} {
		t.Run(severity, func(t *testing.T) {
			r := humanRequest(t)
			r.Requirements.AudioRequired = true
			r.Project.Assets = []domain.Asset{{ID: "video", Kind: domain.AssetKindVideo, URI: "fixture.mp4", Metadata: map[string]any{"private": "PRIVATE"}}}
			r.Candidates = []Candidate{{AssetID: "video", Suitable: domain.DecisionYes}}
			v := checkFunc(func(ctx context.Context, req validation.Request) (domain.ValidationResult, error) {
				if req.Asset.Metadata != nil || req.Asset.ID != "video" || req.TargetID != r.ShotID || req.Requirements != r.Requirements {
					t.Errorf("invalid validation input: %+v", req)
				}
				out := passed(req)
				out.Status = domain.ValidationWarning
				out.Details["issues"] = []validation.Issue{{Code: "uniform_frame_heuristic", Severity: severity}}
				out.Details["extra"] = "PRIVATE"
				return out, nil
			})
			a := mustAnalyzer(t, v, routeFunc(func(context.Context, provider.RouteRequest) (provider.RouteResult, error) {
				t.Error("reuse/unresolved should not route")
				return provider.RouteResult{}, nil
			}), nil, nil)
			out, e := a.Analyze(context.Background(), r)
			if e != nil {
				t.Fatal(e)
			}
			body, e := json.Marshal(out)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(body), "PRIVATE") {
				t.Errorf("raw validation evidence escaped: %s", body)
			}
			if severity == "PRIVATE" {
				if out.Strategy != OpNone || len(out.Assets) != 1 || out.Assets[0].Validation == nil || out.Assets[0].Validation.Details != nil {
					t.Errorf("invalid evidence retained: %+v", out)
				}
			} else if out.Strategy != ReuseVideo || out.MissingAudioReadiness || out.SourceAssetID != "video" {
				t.Errorf("verified audio-bearing warning reuse blocked: %+v", out)
			}
		})
	}
}

func boundRequest(t *testing.T) Request {
	t.Helper()
	r := plainRequest(t)
	w := domain.Workload{Precision: "fp16", Width: 640, Height: 360, Frames: 48, Quality: "preview", Offload: "none"}
	c := domain.ModelCapabilities{ProviderID: "local", ModelID: "fixture-model", Local: true, Tags: []string{"deterministic", "renders-background", "renders-text"}, Requirements: &domain.ModelRequirements{Version: 1, Revision: "model-rev", ExecutionProfiles: []domain.ExecutionProfile{{ID: "profile", Revision: "profile-rev", Trusted: true, MemoryKnown: true, LoadRAMBytes: 1000, ResidentRAMBytes: 700, WorkspaceRAMBytes: 300, CPU: 1, Backend: domain.BackendNone, Envelope: domain.WorkloadEnvelope{Precisions: []string{"fp16"}, Width: domain.IntRange{Min: 640, Max: 640}, Height: domain.IntRange{Min: 360, Max: 360}, Frames: domain.IntRange{Min: 48, Max: 48}, Qualities: []string{"preview"}, Offloads: []string{"none"}}}}}}
	r.Bindings = []resource.PlanRequest{{ID: r.ID, TargetID: r.ShotID, Capabilities: c, ProfileID: "profile", Workload: w}}
	return r
}

func TestResourceEvidenceRequiresMatchingBindingAndProvenance(t *testing.T) {
	cfg := resource.ReferenceConfig(4, 2, resource.AcceleratorFacts{Known: true, Backend: domain.BackendNone}, 0, "fixture-hardware")
	m, e := resource.NewManager(cfg)
	if e != nil {
		t.Fatal(e)
	}
	p, e := resource.NewPlanner(m, nil, resource.DefaultPlannerConfig())
	if e != nil {
		t.Fatal(e)
	}
	before := m.Snapshot()
	for _, tc := range []struct {
		name    string
		request func(*Request)
		result  func(*resource.PlanResult)
		valid   bool
	}{
		{name: "matching", valid: true},
		{name: "remote-binding-default-local", request: func(r *Request) { r.Bindings[0].Capabilities.Local = false }},
		{name: "any-locality-unproven", request: func(r *Request) { r.Route.Locality = provider.AnyLocality }},
		{name: "other-provider", result: func(p *resource.PlanResult) { p.Plan.Provenance.ProviderID = "PRIVATE" }},
		{name: "inconsistent-status", result: func(p *resource.PlanResult) { p.LocalStatus = resource.StatusWait }},
		{name: "request-plan-disagree", result: func(p *resource.PlanResult) { p.Request.CPU = 2 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := boundRequest(t)
			if tc.request != nil {
				tc.request(&r)
			}
			wrapped := planFunc(func(ctx context.Context, req resource.PlanRequest) (resource.PlanResult, error) {
				out, e := p.Plan(ctx, req)
				if e == nil && tc.result != nil {
					tc.result(&out)
				}
				out.Plan.Rationale = "PRIVATE"
				return out, e
			})
			a := mustAnalyzer(t, unusedValidator(t), routeFunc(func(context.Context, provider.RouteRequest) (provider.RouteResult, error) {
				return provider.RouteResult{EligibleIDs: []string{"local"}}, nil
			}), nil, wrapped)
			out, e := a.Analyze(context.Background(), r)
			if e != nil {
				t.Fatal(e)
			}
			if tc.valid {
				if out.Resource == nil || out.ResourceStatus != "local_ready" || out.Resource.Plan.RAMBytes != 1250 || out.Resource.Plan.Provenance.DeclaredPeakRAMBytes != 1000 || out.Resource.Plan.Provenance.Workload != r.Bindings[0].Workload {
					t.Errorf("matching evidence rejected: %+v", out)
				}
			} else if out.Resource != nil || out.ResourceStatus != "unknown" {
				t.Errorf("contradictory evidence accepted: %+v", out)
			}
			body, e := json.Marshal(out)
			if e != nil {
				t.Fatal(e)
			}
			if strings.Contains(string(body), "PRIVATE") {
				t.Errorf("raw resource evidence escaped: %s", body)
			}
		})
	}
	if !reflect.DeepEqual(before, m.Snapshot()) {
		t.Error("analysis changed resource allocations")
	}
}

func TestPlannerCannotMutateTrustedBindingBaseline(t *testing.T) {
	for _, tc := range []struct {
		name             string
		mutateBeforePlan bool
		wantEvidence     bool
	}{
		{name: "result based on mutated callback binding", mutateBeforePlan: true},
		{name: "callback mutation does not invalidate original evidence", wantEvidence: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := boundRequest(t)
			cfg := resource.ReferenceConfig(4, 2, resource.AcceleratorFacts{Known: true, Backend: domain.BackendNone}, 0, "fixture-hardware")
			manager, err := resource.NewManager(cfg)
			if err != nil {
				t.Fatal(err)
			}
			realPlanner, err := resource.NewPlanner(manager, nil, resource.DefaultPlannerConfig())
			if err != nil {
				t.Fatal(err)
			}
			planner := planFunc(func(ctx context.Context, callbackReq resource.PlanRequest) (resource.PlanResult, error) {
				var result resource.PlanResult
				if !tc.mutateBeforePlan {
					result, err = realPlanner.Plan(ctx, callbackReq)
					if err != nil {
						return result, err
					}
				}
				callbackReq.Capabilities.Requirements.Revision = "callback-mutated-revision"
				if tc.mutateBeforePlan {
					return realPlanner.Plan(ctx, callbackReq)
				}
				return result, nil
			})
			a := mustAnalyzer(t, unusedValidator(t), routeFunc(func(context.Context, provider.RouteRequest) (provider.RouteResult, error) {
				return provider.RouteResult{EligibleIDs: []string{"local"}}, nil
			}), nil, planner)
			out, err := a.Analyze(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			if got := out.Resource != nil; got != tc.wantEvidence {
				t.Fatalf("resource evidence accepted=%v, want %v; status=%q", got, tc.wantEvidence, out.ResourceStatus)
			}
			if r.Bindings[0].Capabilities.Requirements.Revision != "model-rev" {
				t.Fatal("planner callback mutation reached original request binding")
			}
		})
	}
}

func TestFrameCountUsesRoundTrippableDecimalFPS(t *testing.T) {
	for _, tc := range []struct {
		name     string
		duration time.Duration
		fps      float64
		want     int
		ok       bool
	}{
		{name: "ten seconds at 30.1 fps", duration: 10 * time.Second, fps: 30.1, want: 301, ok: true},
		{name: "fifty seconds at 23.98 fps", duration: 50 * time.Second, fps: 23.98, want: 1199, ok: true},
		{name: "fractional frame rounds up", duration: time.Second, fps: 23.976, want: 24, ok: true},
		{name: "platform integer overflow", duration: time.Second, fps: math.MaxFloat64},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := frameCount(validation.Requirements{ExpectedDuration: tc.duration, FPS: tc.fps})
			if got != tc.want || ok != tc.ok {
				t.Fatalf("frameCount()=(%d, %v), want (%d, %v)", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestMalformedInjectedRouteResultsCannotCreateEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result provider.RouteResult
	}{
		{name: "unknown rejection code", result: provider.RouteResult{EligibleIDs: []string{"local"}, Rejections: []provider.Rejection{{ProviderID: "blocked", Codes: []provider.RejectionCode{"private-code"}}}}},
		{name: "fallback disagrees with eligible order", result: provider.RouteResult{EligibleIDs: []string{"local", "fallback"}, FallbackIDs: []string{"other"}}},
		{name: "duplicate eligible provider", result: provider.RouteResult{EligibleIDs: []string{"local", "local"}, FallbackIDs: []string{"local"}}},
		{name: "eligible provider also rejected", result: provider.RouteResult{EligibleIDs: []string{"local"}, Rejections: []provider.Rejection{{ProviderID: "local", Codes: []provider.RejectionCode{provider.RejectHealth}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plannerCalls := 0
			a := mustAnalyzer(t, unusedValidator(t), routeFunc(func(context.Context, provider.RouteRequest) (provider.RouteResult, error) {
				return tc.result, nil
			}), nil, planFunc(func(context.Context, resource.PlanRequest) (resource.PlanResult, error) {
				plannerCalls++
				return resource.PlanResult{}, nil
			}))
			out, err := a.Analyze(context.Background(), boundRequest(t))
			if err != nil {
				t.Fatal(err)
			}
			if out.Routing != nil || out.Resource != nil || plannerCalls != 0 || out.Code != "routing_error" {
				t.Fatalf("malformed route result exposed evidence: out=%+v plannerCalls=%d", out, plannerCalls)
			}
		})
	}
	t.Run("positive eligibility with no-eligible error", func(t *testing.T) {
		a := mustAnalyzer(t, unusedValidator(t), routeFunc(func(context.Context, provider.RouteRequest) (provider.RouteResult, error) {
			return provider.RouteResult{EligibleIDs: []string{"local"}}, provider.ErrNoEligibleProviders
		}), nil, nil)
		out, err := a.Analyze(context.Background(), boundRequest(t))
		if err != nil || out.Routing != nil || out.Code != "routing_error" {
			t.Fatalf("contradictory no-eligible result exposed evidence: out=%+v err=%v", out, err)
		}
	})
}
