package director

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/idgen"
	"github.com/manish96170/videofactory/project"
	"github.com/manish96170/videofactory/registry"
)

func newTestDirector(opts ...Option) *FixtureDirector {
	return NewFixtureDirector(registry.NewFlowRegistry(), registry.NewFramingRegistry(), registry.NewPrimitiveRegistry(), opts...)
}

func TestPlanRejectsEmptyProjectID(t *testing.T) {
	d := newTestDirector()
	_, err := d.Plan(context.Background(), PlanRequest{Prompt: "Hello world."})
	if err == nil {
		t.Fatalf("expected error for empty ProjectID")
	}
}

func TestPlanRejectsEmptyPrompt(t *testing.T) {
	d := newTestDirector()
	_, err := d.Plan(context.Background(), PlanRequest{ProjectID: domain.ID(idgen.New())})
	if err == nil {
		t.Fatalf("expected error for empty Prompt")
	}
}

func TestPlanRespectsCancellation(t *testing.T) {
	d := newTestDirector()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := d.Plan(ctx, PlanRequest{ProjectID: domain.ID(idgen.New()), Prompt: "Hello."})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Plan error = %v, want context.Canceled", err)
	}
}

func TestPlanRejectsUnknownFlowOverride(t *testing.T) {
	d := newTestDirector()
	_, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "Hello.",
		FlowID:    "no-such-flow",
	})
	if err == nil {
		t.Fatalf("expected error for an unknown flow override")
	}
}

func TestPlanRejectsUnknownFramingOverride(t *testing.T) {
	d := newTestDirector()
	_, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "Hello.",
		FramingID: "no-such-framing",
	})
	if err == nil {
		t.Fatalf("expected error for an unknown framing override")
	}
}

func TestPlanProducesDeterministicShotsForPlainPrompt(t *testing.T) {
	d := newTestDirector()
	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "First we open the editor. Then we write a function. Finally we run it in the terminal.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(res.Plan.Shots) != 3 {
		t.Fatalf("expected 3 shots, got %d", len(res.Plan.Shots))
	}
	for i, shot := range res.Plan.Shots {
		if !shot.Deterministic {
			t.Errorf("shot %d: expected Deterministic=true for a non-avatar prompt", i)
		}
		if shot.Index != i {
			t.Errorf("shot %d: Index = %d, want %d", i, shot.Index, i)
		}
		if len(shot.Primitives) != 2 {
			t.Errorf("shot %d: expected 2 primitives (Background+Text), got %d", i, len(shot.Primitives))
			continue
		}
		if shot.Primitives[0].Type != domain.PrimitiveTypeBackground || shot.Primitives[1].Type != domain.PrimitiveTypeText {
			t.Errorf("shot %d: primitive types = %v, want [Background Text]", i, []domain.PrimitiveType{shot.Primitives[0].Type, shot.Primitives[1].Type})
		}
	}

	if len(res.AssetRequirements) != 0 {
		t.Fatalf("expected no AssetRequirements for an all-deterministic plan, got %+v", res.AssetRequirements)
	}

	// Default flow for a non-avatar prompt is code-tutorial, whose
	// DefaultFramingID is screen-focused (registry package, Phase 04).
	if res.Plan.FlowID != "code-tutorial" {
		t.Fatalf("FlowID = %q, want %q", res.Plan.FlowID, "code-tutorial")
	}
	if res.Plan.FramingID != "screen-focused" {
		t.Fatalf("FramingID = %q, want %q", res.Plan.FramingID, "screen-focused")
	}
}

func TestPlanProducesAvatarShotForAvatarKeywords(t *testing.T) {
	d := newTestDirector()
	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "Let me introduce myself as your presenter.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if len(res.Plan.Shots) != 1 {
		t.Fatalf("expected 1 shot, got %d", len(res.Plan.Shots))
	}
	shot := res.Plan.Shots[0]
	if shot.Deterministic {
		t.Fatalf("expected Deterministic=false for an avatar shot")
	}
	if shot.Category != domain.ShotCategoryAvatar {
		t.Fatalf("Category = %q, want AVATAR", shot.Category)
	}
	if len(shot.Primitives) != 1 || shot.Primitives[0].Type != domain.PrimitiveTypeDeveloperAvatar {
		t.Fatalf("expected a single DeveloperAvatar primitive, got %+v", shot.Primitives)
	}

	if len(res.AssetRequirements) != 1 {
		t.Fatalf("expected 1 AssetRequirement for the avatar shot, got %+v", res.AssetRequirements)
	}
	if res.AssetRequirements[0].ShotID != shot.ID {
		t.Fatalf("AssetRequirement.ShotID = %q, want %q", res.AssetRequirements[0].ShotID, shot.ID)
	}

	// Default flow for an avatar-mentioning prompt is developer-explainer.
	if res.Plan.FlowID != "developer-explainer" {
		t.Fatalf("FlowID = %q, want %q", res.Plan.FlowID, "developer-explainer")
	}
	if res.Plan.FramingID != "developer-circle" {
		t.Fatalf("FramingID = %q, want %q", res.Plan.FramingID, "developer-circle")
	}
}

func TestPlanAggregatesRequiredCapabilities(t *testing.T) {
	d := newTestDirector()
	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "First a plain sentence. Then another code sentence. Finally show my avatar.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	want := []string{"ai-capable", "deterministic", "realistic-human-motion", "renders-background", "renders-text"}
	if !reflect.DeepEqual(res.RequiredCapabilities, want) {
		t.Errorf("RequiredCapabilities = %v, want exact sorted, deduplicated %v", res.RequiredCapabilities, want)
	}
}

func TestPlanRespectsFlowAndFramingOverrides(t *testing.T) {
	d := newTestDirector()
	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "Let me show my avatar.", // would normally select developer-explainer
		FlowID:    "code-tutorial",
		FramingID: "split-screen",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.Plan.FlowID != "code-tutorial" {
		t.Fatalf("FlowID = %q, want override %q", res.Plan.FlowID, "code-tutorial")
	}
	if res.Plan.FramingID != "split-screen" {
		t.Fatalf("FramingID = %q, want override %q", res.Plan.FramingID, "split-screen")
	}
}

func TestPlanProducesAStructurallyValidPlan(t *testing.T) {
	d := newTestDirector()
	projectID := domain.ID(idgen.New())
	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: projectID,
		Prompt:    "First we open the editor. Then let me show my avatar speaking.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// Validate it with project.ValidatePlan directly too, as a
	// standalone double-check (Plan already does this internally
	// before returning, so this also guards against that internal
	// check silently becoming a no-op).
	if err := project.ValidatePlan(projectID, res.Plan); err != nil {
		t.Fatalf("project.ValidatePlan: %v", err)
	}
}

func TestPlanMetadataRecordsPromptAndDirector(t *testing.T) {
	d := newTestDirector()
	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "Hello there.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if res.Metadata.Prompt != "Hello there." {
		t.Fatalf("Metadata.Prompt = %q", res.Metadata.Prompt)
	}
	if res.Metadata.DirectorID != "fixture" || res.Metadata.DirectorVersion != FixtureDirectorVersion {
		t.Fatalf("Metadata.DirectorID/DirectorVersion = %q/%q", res.Metadata.DirectorID, res.Metadata.DirectorVersion)
	}
	if res.Metadata.CreatedAt.IsZero() {
		t.Fatalf("Metadata.CreatedAt is zero")
	}
}

func TestPlanIsStructurallyDeterministicAcrossRepeatedCalls(t *testing.T) {
	// Compare the entire result, not just shot summaries. Only freshly
	// generated IDs (including references to them) and timestamps vary.
	d := newTestDirector()
	for _, prompt := range []string{
		"First we open the editor. Then we write a function.",
		"First show code. Then show my avatar. Finally open the terminal.",
	} {
		t.Run(prompt, func(t *testing.T) {
			req := PlanRequest{ProjectID: domain.ID(idgen.New()), Prompt: prompt}
			first, err := d.Plan(context.Background(), req)
			if err != nil {
				t.Fatalf("first Plan: %v", err)
			}
			second, err := d.Plan(context.Background(), req)
			if err != nil {
				t.Fatalf("second Plan: %v", err)
			}
			a, b := normalizePlanResult(first), normalizePlanResult(second)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("normalized PlanResults differ:\nfirst: %+v\nsecond: %+v", a, b)
			}
		})
	}
}

// normalizePlanResult copies the result and canonicalizes only generated
// IDs and timestamps. References are mapped, not blanked, so broken plan,
// dependency, or asset linkage remains visible. All content, properties,
// requirements, metadata, and slice order remain part of the comparison.
func normalizePlanResult(result PlanResult) PlanResult {
	ids := map[domain.ID]domain.ID{}
	remember := func(id, canonical domain.ID) {
		if !id.IsZero() {
			ids[id] = canonical
		}
	}
	remember(result.Plan.ID, "plan")
	for i, shot := range result.Plan.Shots {
		remember(shot.ID, domain.ID(fmt.Sprintf("shot-%d", i)))
		for j, prim := range shot.Primitives {
			remember(prim.ID, domain.ID(fmt.Sprintf("shot-%d-primitive-%d", i, j)))
		}
	}
	canonical := func(id domain.ID) domain.ID {
		if mapped, ok := ids[id]; ok {
			return mapped
		}
		return id // Keep missing/unknown references detectable.
	}
	result.Plan.ID = canonical(result.Plan.ID)
	result.Plan.CreatedAt = time.Time{}
	result.Metadata.CreatedAt = time.Time{}
	result.Plan.Shots = slices.Clone(result.Plan.Shots)
	for i := range result.Plan.Shots {
		shot := &result.Plan.Shots[i]
		shot.ID = canonical(shot.ID)
		shot.PlanID = canonical(shot.PlanID)
		shot.DependsOn = slices.Clone(shot.DependsOn)
		for j, dep := range shot.DependsOn {
			shot.DependsOn[j] = canonical(dep)
		}
		shot.Primitives = slices.Clone(shot.Primitives)
		for j := range shot.Primitives {
			prim := &shot.Primitives[j]
			prim.ID = canonical(prim.ID)
			prim.Properties = maps.Clone(prim.Properties)
		}
	}
	result.AssetRequirements = slices.Clone(result.AssetRequirements)
	for i := range result.AssetRequirements {
		result.AssetRequirements[i].ShotID = canonical(result.AssetRequirements[i].ShotID)
	}
	result.RequiredCapabilities = slices.Clone(result.RequiredCapabilities)
	return result
}

func TestNormalizePlanResultRetainsSubstantiveDifferences(t *testing.T) {
	res, err := newTestDirector().Plan(context.Background(), PlanRequest{
		ProjectID: "project",
		Prompt:    "Show code. Then more code. Finally show my avatar.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(res.Plan.Shots) != 3 || len(res.Plan.Shots[0].Primitives) != 2 || len(res.Plan.Shots[2].Primitives) != 1 || len(res.AssetRequirements) != 1 {
		t.Fatalf("unexpected mixed-plan fixture: %+v", res)
	}
	res.Plan.Shots[1].DependsOn = []domain.ID{res.Plan.Shots[0].ID}
	before := roundTripJSON(t, res)
	baseline := normalizePlanResult(res)
	for _, tc := range []struct {
		name   string
		change func(*PlanResult)
	}{
		{name: "text", change: func(r *PlanResult) { r.Plan.Shots[0].Primitives[1].Properties["text"] = "Other code." }},
		{name: "background", change: func(r *PlanResult) { r.Plan.Shots[0].Primitives[0].Properties["color"] = "#FFFFFF" }},
		{name: "avatar script", change: func(r *PlanResult) { r.Plan.Shots[2].Primitives[0].Properties["script"] = "Other narration." }},
		{name: "primitive type", change: func(r *PlanResult) { r.Plan.Shots[0].Primitives[1].Type = domain.PrimitiveTypeSubtitle }},
		{name: "primitive order", change: func(r *PlanResult) { slices.Reverse(r.Plan.Shots[0].Primitives) }},
		{name: "asset kind", change: func(r *PlanResult) { r.AssetRequirements[0].Kind = "other-asset" }},
		{name: "asset description", change: func(r *PlanResult) { r.AssetRequirements[0].Description = "Other requirement." }},
		{name: "asset linkage", change: func(r *PlanResult) { r.AssetRequirements[0].ShotID = r.Plan.Shots[0].ID }},
		{name: "missing asset", change: func(r *PlanResult) { r.AssetRequirements = nil }},
		{name: "capability content", change: func(r *PlanResult) { r.RequiredCapabilities[0] = "other-capability" }},
		{name: "capability order", change: func(r *PlanResult) { slices.Reverse(r.RequiredCapabilities) }},
		{name: "duplicate capability", change: func(r *PlanResult) {
			r.RequiredCapabilities = append(r.RequiredCapabilities, r.RequiredCapabilities[0])
		}},
		{name: "dependency linkage", change: func(r *PlanResult) { r.Plan.Shots[1].DependsOn[0] = r.Plan.Shots[2].ID }},
		{name: "plan linkage", change: func(r *PlanResult) { r.Plan.Shots[0].PlanID = "unknown-plan" }},
		{name: "project linkage", change: func(r *PlanResult) { r.Plan.ProjectID = "other-project" }},
		{name: "metadata prompt", change: func(r *PlanResult) { r.Metadata.Prompt = "Other prompt." }},
		{name: "metadata version", change: func(r *PlanResult) { r.Metadata.DirectorVersion = "other-version" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changed := normalizePlanResult(res)
			tc.change(&changed)
			if reflect.DeepEqual(baseline, normalizePlanResult(changed)) {
				t.Fatalf("normalization hid a substantive %s difference", tc.name)
			}
		})
	}
	if after := roundTripJSON(t, res); !reflect.DeepEqual(before, after) {
		t.Fatal("normalization or sensitivity checks mutated the original result")
	}
}

func roundTripJSON[T any](t *testing.T, value T) T {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var decoded T
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return decoded
}

// --- PromptInterpreter integration ---

type fakeInterpreter struct {
	result PromptInterpretation
	err    error
}

func (f fakeInterpreter) Interpret(ctx context.Context, prompt string) (PromptInterpretation, error) {
	return f.result, f.err
}

var _ PromptInterpreter = fakeInterpreter{}

type interpreterFunc func(context.Context, string) (PromptInterpretation, error)

func (f interpreterFunc) Interpret(ctx context.Context, prompt string) (PromptInterpretation, error) {
	return f(ctx, prompt)
}

func TestPlanObservesCancellationAfterInterpret(t *testing.T) {
	for _, tc := range []struct {
		name   string
		result PromptInterpretation
	}{
		{name: "valid hints", result: PromptInterpretation{Shots: []ShotHint{{Text: "Hello."}}}},
		{name: "fallback"},
		{name: "before flow lookup", result: PromptInterpretation{SuggestedFlowID: "unknown"}},
		{name: "before hint validation", result: PromptInterpretation{Shots: []ShotHint{{Text: " "}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := newTestDirector(WithPromptInterpreter(interpreterFunc(func(context.Context, string) (PromptInterpretation, error) {
				cancel()
				return tc.result, nil // An adapter may return success even after cancellation.
			})))
			res, err := d.Plan(ctx, PlanRequest{ProjectID: "project", Prompt: "Hello."})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Plan error = %v, want context.Canceled", err)
			}
			if !reflect.DeepEqual(res, PlanResult{}) {
				t.Fatalf("canceled Plan returned a partial result: %+v", res)
			}
		})
	}
}

func TestPlanObservesCancellationBeforeShotConstruction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := newTestDirector(WithPromptInterpreter(fakeInterpreter{result: PromptInterpretation{
		Shots: []ShotHint{{Text: " "}},
	}}))
	d.now = func() time.Time {
		cancel()
		return time.Now()
	}
	_, err := d.Plan(ctx, PlanRequest{ProjectID: "project", Prompt: "Hello."})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Plan error = %v, want context.Canceled before hint validation/construction", err)
	}
}

func TestPlanRejectsBlankInterpreterHints(t *testing.T) {
	for _, tc := range []struct {
		name  string
		hints []ShotHint
		index int
	}{
		{name: "empty normal", hints: []ShotHint{{Category: domain.ShotCategoryCode}}},
		{name: "whitespace normal", hints: []ShotHint{{Text: " \t\n\u2003", Category: domain.ShotCategoryUI}}},
		{name: "empty avatar", hints: []ShotHint{{Category: domain.ShotCategoryAvatar}}},
		{name: "whitespace avatar", hints: []ShotHint{{Text: " \t\n", Category: domain.ShotCategoryAvatar}}},
		{name: "mixed valid invalid", hints: []ShotHint{{Text: "Valid code."}, {Text: " \n"}}, index: 1},
		{name: "mixed invalid valid", hints: []ShotHint{{}, {Text: "Valid code."}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := append([]ShotHint(nil), tc.hints...)
			d := newTestDirector(WithPromptInterpreter(fakeInterpreter{result: PromptInterpretation{Shots: tc.hints}}))
			res, err := d.Plan(context.Background(), PlanRequest{ProjectID: "project", Prompt: "Fallback must not hide invalid hints."})
			if err == nil {
				t.Fatalf("Plan accepted blank hint at index %d", tc.index)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("shot hint %d", tc.index)) || !strings.Contains(err.Error(), "Text") {
				t.Errorf("Plan error = %v, want indexed hint Text error", err)
			}
			if !reflect.DeepEqual(res, PlanResult{}) {
				t.Errorf("invalid hints returned a partial/fallback result: %+v", res)
			}
			if !reflect.DeepEqual(tc.hints, original) {
				t.Errorf("Plan mutated interpreter hints: got %+v, want %+v", tc.hints, original)
			}
		})
	}
}

func TestPlanNormalizesHintTextWithoutMutatingInterpreterHints(t *testing.T) {
	hints := []ShotHint{
		{Text: " \t Show code. \n", Category: domain.ShotCategoryCode},
		{Text: " \u2003 Show my avatar. \t", Category: domain.ShotCategoryCode},
	}
	original := append([]ShotHint(nil), hints...)
	d := newTestDirector(WithPromptInterpreter(fakeInterpreter{result: PromptInterpretation{Shots: hints}}))
	for call := 0; call < 2; call++ {
		res, err := d.Plan(context.Background(), PlanRequest{ProjectID: "project", Prompt: "Raw prompt."})
		if err != nil {
			t.Fatalf("Plan: %v", err)
		}
		if len(res.Plan.Shots) != 2 {
			t.Fatalf("shot count = %d, want 2", len(res.Plan.Shots))
		}
		for i, shot := range res.Plan.Shots {
			key := "text"
			if i == 1 {
				key = "script"
			}
			got := shot.Primitives[len(shot.Primitives)-1].Properties[key]
			if want := strings.TrimSpace(original[i].Text); got != want {
				t.Errorf("shot %d %s = %q, want %q", i, key, got, want)
			}
		}
		if !reflect.DeepEqual(hints, original) {
			t.Fatalf("Plan mutated interpreter-owned hints: got %+v, want %+v", hints, original)
		}
	}
}

func TestPlanAvatarCategoryAndPrimitiveIntentAgree(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hint     ShotHint
		category domain.ShotCategory
		avatar   bool
	}{
		{name: "code hint with avatar text", hint: ShotHint{Text: "Show my avatar.", Category: domain.ShotCategoryCode}, category: domain.ShotCategoryAvatar, avatar: true},
		{name: "avatar hint with code text", hint: ShotHint{Text: "Show code.", Category: domain.ShotCategoryAvatar}, category: domain.ShotCategoryAvatar, avatar: true},
		{name: "inferred avatar", hint: ShotHint{Text: "Show my avatar."}, category: domain.ShotCategoryAvatar, avatar: true},
		{name: "non avatar hint preserved", hint: ShotHint{Text: "Show code.", Category: domain.ShotCategoryTerminal}, category: domain.ShotCategoryTerminal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hints := []ShotHint{tc.hint}
			d := newTestDirector(WithPromptInterpreter(fakeInterpreter{result: PromptInterpretation{Shots: hints}}))
			res, err := d.Plan(context.Background(), PlanRequest{ProjectID: "project", Prompt: "Raw prompt."})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if len(res.Plan.Shots) != 1 {
				t.Fatalf("shot count = %d, want 1", len(res.Plan.Shots))
			}
			shot := res.Plan.Shots[0]
			if shot.Category != tc.category || shot.Deterministic == tc.avatar {
				t.Errorf("category/deterministic = %s/%v, want %s/%v", shot.Category, shot.Deterministic, tc.category, !tc.avatar)
			}
			if tc.avatar {
				if len(shot.Primitives) != 1 || shot.Primitives[0].Type != domain.PrimitiveTypeDeveloperAvatar {
					t.Errorf("avatar primitives = %+v, want a single DeveloperAvatar", shot.Primitives)
				}
				if len(res.AssetRequirements) != 1 || res.AssetRequirements[0].ShotID != shot.ID || res.AssetRequirements[0].Kind != "generated-video" {
					t.Errorf("avatar asset requirements = %+v", res.AssetRequirements)
				}
				if !reflect.DeepEqual(res.RequiredCapabilities, []string{"ai-capable", "realistic-human-motion"}) {
					t.Errorf("avatar capabilities = %v", res.RequiredCapabilities)
				}
			} else if len(shot.Primitives) != 2 || shot.Primitives[0].Type != domain.PrimitiveTypeBackground || shot.Primitives[1].Type != domain.PrimitiveTypeText {
				t.Errorf("deterministic primitives = %+v, want Background+Text", shot.Primitives)
			}
			if hints[0] != tc.hint {
				t.Errorf("Plan mutated hint: got %+v, want %+v", hints[0], tc.hint)
			}
		})
	}
}

func TestPlanFallbackDoesNotMutateInterpreterHintBackingArray(t *testing.T) {
	backing := []ShotHint{{Text: "Owned by interpreter."}, {Text: "Also owned by interpreter."}}
	original := append([]ShotHint(nil), backing...)
	d := newTestDirector(WithPromptInterpreter(fakeInterpreter{result: PromptInterpretation{Shots: backing[:0]}}))
	res, err := d.Plan(context.Background(), PlanRequest{ProjectID: "project", Prompt: "First sentence. Second sentence."})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(res.Plan.Shots) != 2 {
		t.Errorf("fallback shot count = %d, want 2", len(res.Plan.Shots))
	}
	if !reflect.DeepEqual(backing, original) {
		t.Errorf("fallback mutated interpreter backing array: got %+v, want %+v", backing, original)
	}
}

func TestPlanUsesInterpreterHintsWhenProvided(t *testing.T) {
	interp := fakeInterpreter{result: PromptInterpretation{
		SuggestedFlowID: "developer-explainer",
		Shots: []ShotHint{
			{Text: "Interpreter-provided shot one.", Category: domain.ShotCategoryCode},
			{Text: "Interpreter-provided shot two.", Category: domain.ShotCategoryTerminal},
		},
	}}
	d := newTestDirector(WithPromptInterpreter(interp))

	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "This raw prompt text would normally split differently.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	if res.Plan.FlowID != "developer-explainer" {
		t.Fatalf("expected the interpreter's suggested flow, got %q", res.Plan.FlowID)
	}
	if len(res.Plan.Shots) != 2 {
		t.Fatalf("expected 2 shots from the interpreter's hints, got %d", len(res.Plan.Shots))
	}
	if res.Plan.Shots[0].Category != domain.ShotCategoryCode || res.Plan.Shots[1].Category != domain.ShotCategoryTerminal {
		t.Fatalf("expected the interpreter's hinted categories to be used, got %+v", res.Plan.Shots)
	}
}

func TestPlanPropagatesInterpreterError(t *testing.T) {
	for _, cause := range []error{errors.New("interpreter unavailable"), context.Canceled, context.DeadlineExceeded} {
		d := newTestDirector(WithPromptInterpreter(fakeInterpreter{err: cause}))
		_, err := d.Plan(context.Background(), PlanRequest{ProjectID: domain.ID(idgen.New()), Prompt: "Hello."})
		if !errors.Is(err, cause) {
			t.Errorf("Plan error = %v, want wrapped %v", err, cause)
		}
	}
}

func TestPlanFallsBackToHeuristicsWhenInterpreterHasNoShots(t *testing.T) {
	// An interpreter that returns a flow suggestion but no shot hints:
	// FixtureDirector must still fall back to sentence-splitting.
	interp := fakeInterpreter{result: PromptInterpretation{SuggestedFlowID: "code-tutorial"}}
	d := newTestDirector(WithPromptInterpreter(interp))

	res, err := d.Plan(context.Background(), PlanRequest{
		ProjectID: domain.ID(idgen.New()),
		Prompt:    "First sentence. Second sentence.",
	})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(res.Plan.Shots) != 2 {
		t.Fatalf("expected fallback sentence-splitting to produce 2 shots, got %d", len(res.Plan.Shots))
	}
}

func TestPlanUsesRuntimeRegisteredFlowAndFraming(t *testing.T) {
	for _, tc := range []struct {
		name            string
		defaultFraming  domain.ID
		overrideFraming domain.ID
		wantFraming     domain.ID
		wantError       string
	}{
		{name: "custom default", defaultFraming: "custom-framing", wantFraming: "custom-framing"},
		{name: "empty default falls back", wantFraming: "fullscreen"},
		{name: "dangling default", defaultFraming: "missing-framing", wantError: `unknown framing "missing-framing"`},
		{name: "override replaces dangling default", defaultFraming: "missing-framing", overrideFraming: "custom-framing", wantFraming: "custom-framing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDirector()
			if err := d.framings.Register(domain.Framing{
				ID: "custom-framing", Name: "Custom framing",
				Slots: []domain.FramingSlot{{Name: "main", Bounds: domain.Bounds{Width: 1, Height: 1}}},
			}); err != nil {
				t.Fatalf("Register framing: %v", err)
			}
			if err := d.flows.Register(domain.Flow{ID: "custom-flow", Name: "Custom flow", DefaultFramingID: tc.defaultFraming}); err != nil {
				t.Fatalf("Register flow: %v", err)
			}
			req := PlanRequest{ProjectID: "project", Prompt: "Show code.", FlowID: "custom-flow", FramingID: tc.overrideFraming}
			res, err := d.Plan(context.Background(), req)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("Plan error = %v, want %q", err, tc.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if res.Plan.FlowID != req.FlowID || res.Plan.FramingID != tc.wantFraming {
				t.Errorf("flow/framing = %s/%s, want %s/%s", res.Plan.FlowID, res.Plan.FramingID, req.FlowID, tc.wantFraming)
			}
			if err := project.ValidatePlan(req.ProjectID, res.Plan); err != nil {
				t.Fatalf("ValidatePlan: %v", err)
			}
		})
	}
}

func TestPlanRequestFlowOverridesInterpreterSuggestion(t *testing.T) {
	for _, suggested := range []domain.ID{"developer-explainer", "unknown-flow"} {
		t.Run(string(suggested), func(t *testing.T) {
			d := newTestDirector(WithPromptInterpreter(fakeInterpreter{result: PromptInterpretation{SuggestedFlowID: suggested}}))
			res, err := d.Plan(context.Background(), PlanRequest{ProjectID: "project", Prompt: "Show my avatar.", FlowID: "code-tutorial"})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			if res.Plan.FlowID != "code-tutorial" || res.Plan.FramingID != "screen-focused" {
				t.Fatalf("flow/framing = %s/%s, want request flow and its default framing", res.Plan.FlowID, res.Plan.FramingID)
			}
		})
	}
}

func TestPlanRequestAndResultJSONRoundTrip(t *testing.T) {
	for _, prompt := range []string{"Show code.", "Show code. Then show my avatar."} {
		t.Run(prompt, func(t *testing.T) {
			req := PlanRequest{ProjectID: "project", Prompt: prompt, FlowID: "code-tutorial", FramingID: "split-screen"}
			decodedReq := roundTripJSON(t, req)
			if !reflect.DeepEqual(decodedReq, req) {
				t.Fatalf("request roundtrip = %+v, want %+v", decodedReq, req)
			}
			d := newTestDirector()
			// JSON intentionally drops time.Time's monotonic component.
			// Use an exact UTC instant so the complete typed result compares.
			d.now = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
			res, err := d.Plan(context.Background(), decodedReq)
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			decodedRes := roundTripJSON(t, res)
			if !reflect.DeepEqual(decodedRes, res) {
				t.Fatalf("result roundtrip differs:\ngot: %+v\nwant: %+v", decodedRes, res)
			}
			if err := project.ValidatePlan(req.ProjectID, decodedRes.Plan); err != nil {
				t.Fatalf("ValidatePlan after roundtrip: %v", err)
			}
		})
	}
}

func TestPlanConcurrentCallsWithStatelessInterpreter(t *testing.T) {
	// Every call receives freshly allocated hints; no mutable adapter state
	// or interpreter-owned slice is shared between concurrent callers.
	d := newTestDirector(WithPromptInterpreter(interpreterFunc(func(ctx context.Context, _ string) (PromptInterpretation, error) {
		if err := ctx.Err(); err != nil {
			return PromptInterpretation{}, err
		}
		return PromptInterpretation{
			SuggestedFlowID: "developer-explainer",
			Shots: []ShotHint{
				{Text: "Show code.", Category: domain.ShotCategoryCode},
				{Text: "Show my avatar.", Category: domain.ShotCategoryAvatar},
			},
		}, nil
	})))
	req := PlanRequest{ProjectID: "project", Prompt: "Shared prompt."}
	baseline, err := d.Plan(context.Background(), req)
	if err != nil {
		t.Fatalf("baseline Plan: %v", err)
	}
	want := normalizePlanResult(baseline)
	type outcome struct {
		result PlanResult
		err    error
	}
	const callers = 24
	results := make(chan outcome, callers)
	start := make(chan struct{})
	for i := 0; i < callers; i++ {
		go func() {
			<-start
			res, err := d.Plan(context.Background(), req)
			results <- outcome{result: res, err: err}
		}()
	}
	close(start)
	seenIDs := map[domain.ID]bool{}
	for i := 0; i < callers; i++ {
		got := <-results
		if got.err != nil {
			t.Errorf("concurrent Plan %d: %v", i, got.err)
			continue
		}
		if normalized := normalizePlanResult(got.result); !reflect.DeepEqual(normalized, want) {
			t.Errorf("concurrent Plan %d differs:\ngot: %+v\nwant: %+v", i, normalized, want)
		}
		if err := project.ValidatePlan(req.ProjectID, got.result.Plan); err != nil {
			t.Errorf("concurrent Plan %d validation: %v", i, err)
		}
		ids := []domain.ID{got.result.Plan.ID}
		for _, shot := range got.result.Plan.Shots {
			ids = append(ids, shot.ID)
			for _, prim := range shot.Primitives {
				ids = append(ids, prim.ID)
			}
		}
		for _, id := range ids {
			if id.IsZero() || seenIDs[id] {
				t.Errorf("concurrent Plan %d returned empty/duplicate generated ID %q", i, id)
			}
			seenIDs[id] = true
		}
	}
}
