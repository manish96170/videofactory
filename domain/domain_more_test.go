package domain

import (
	"encoding/json"
	"testing"
	"time"
)

// jsonRoundTrip marshals v, unmarshals into a fresh value of the same
// type, and returns it. It fails the test immediately on any encoding
// error so individual tests can focus on asserting field values.
func jsonRoundTrip[T any](t *testing.T, v T) T {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded T
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return decoded
}

func TestAssetJSONRoundTrip(t *testing.T) {
	asset := Asset{
		ID:       ID("asset-1"),
		Kind:     AssetKindImage,
		URI:      "assets/shot-017/frame-00.png",
		Metadata: map[string]any{"width": 1920, "height": 1080},
	}
	decoded := jsonRoundTrip(t, asset)
	if decoded.Kind != AssetKindImage || decoded.URI != asset.URI {
		t.Fatalf("asset round trip mismatch: %+v", decoded)
	}
}

func TestFlowAndFramingJSONRoundTrip(t *testing.T) {
	flow := Flow{
		ID:                    ID("flow-1"),
		Name:                  "developer-explainer",
		Description:           "narrated code walkthrough",
		DefaultFramingID:      ID("framing-developer-circle"),
		RecommendedCategories: []ShotCategory{ShotCategoryAvatar, ShotCategoryCode},
	}
	decodedFlow := jsonRoundTrip(t, flow)
	if decodedFlow.ID != flow.ID || decodedFlow.Name != flow.Name || decodedFlow.Description != flow.Description {
		t.Fatalf("flow round trip mismatch: got %+v, want %+v", decodedFlow, flow)
	}
	if decodedFlow.DefaultFramingID != flow.DefaultFramingID {
		t.Fatalf("flow DefaultFramingID mismatch: got %q, want %q", decodedFlow.DefaultFramingID, flow.DefaultFramingID)
	}
	if len(decodedFlow.RecommendedCategories) != 2 || decodedFlow.RecommendedCategories[0] != ShotCategoryAvatar || decodedFlow.RecommendedCategories[1] != ShotCategoryCode {
		t.Fatalf("flow RecommendedCategories mismatch: got %+v, want %+v", decodedFlow.RecommendedCategories, flow.RecommendedCategories)
	}

	framing := Framing{
		ID:          ID("framing-1"),
		Name:        "developer-circle",
		Description: "circular webcam overlay",
		Slots: []FramingSlot{
			{Name: "main", Bounds: Bounds{X: 0, Y: 0, Width: 1, Height: 1}},
			{Name: "overlay", Bounds: Bounds{X: 0.75, Y: 0.75, Width: 0.2, Height: 0.2}},
		},
	}
	decodedFraming := jsonRoundTrip(t, framing)
	if decodedFraming.ID != framing.ID || decodedFraming.Name != framing.Name {
		t.Fatalf("framing round trip mismatch: got %+v, want %+v", decodedFraming, framing)
	}
	if len(decodedFraming.Slots) != 2 {
		t.Fatalf("framing Slots mismatch: got %+v, want 2 slots", decodedFraming.Slots)
	}
	for i, want := range framing.Slots {
		got := decodedFraming.Slots[i]
		if got.Name != want.Name || got.Bounds != want.Bounds {
			t.Fatalf("framing Slots[%d] mismatch: got %+v, want %+v", i, got, want)
		}
	}
}

func TestGenerationRequestAndResultJSONRoundTrip(t *testing.T) {
	req := GenerationRequest{
		ID:         ID("gen-req-1"),
		ShotID:     ID("shot-17"),
		ProviderID: "invokeai-local",
		Quality:    QualityDraft,
		Params:     map[string]any{"steps": 20},
	}
	decodedReq := jsonRoundTrip(t, req)
	if decodedReq.Quality != QualityDraft || decodedReq.ProviderID != "invokeai-local" {
		t.Fatalf("generation request round trip mismatch: %+v", decodedReq)
	}

	result := GenerationResult{
		ID:           ID("gen-res-1"),
		RequestID:    ID("gen-req-1"),
		AssetID:      ID("asset-1"),
		Seed:         "12345",
		DurationMs:   4200,
		PeakMemoryMB: 5120.5,
	}
	decodedResult := jsonRoundTrip(t, result)
	if decodedResult.ID != result.ID || decodedResult.RequestID != result.RequestID ||
		decodedResult.AssetID != result.AssetID || decodedResult.Seed != result.Seed ||
		decodedResult.DurationMs != result.DurationMs || decodedResult.PeakMemoryMB != result.PeakMemoryMB {
		t.Fatalf("generation result round trip mismatch: got %+v, want %+v", decodedResult, result)
	}
}

func TestPrimitiveCapabilitiesJSONRoundTripAndSupports(t *testing.T) {
	caps := PrimitiveCapabilities{
		Type:               PrimitiveTypeText,
		Deterministic:      true,
		SupportsBounds:     true,
		RequiredProperties: []string{"text"},
		Tags:               []string{"renders-text"},
	}
	decoded := jsonRoundTrip(t, caps)
	if decoded.Type != caps.Type || decoded.Deterministic != caps.Deterministic || decoded.SupportsBounds != caps.SupportsBounds {
		t.Fatalf("primitive capabilities round trip mismatch: got %+v, want %+v", decoded, caps)
	}
	if !decoded.Supports("renders-text") {
		t.Fatalf("expected decoded capabilities to support \"renders-text\"")
	}
	if decoded.Supports("renders-video") {
		t.Fatalf("expected decoded capabilities to not support \"renders-video\"")
	}
}

func TestValidationResultJSONRoundTrip(t *testing.T) {
	result := ValidationResult{
		ID:               ID("val-1"),
		TargetID:         ID("shot-17"),
		Status:           ValidationFailed,
		FailureClass:     "duration-mismatch",
		RetryRecommended: true,
		Details:          map[string]any{"expectedMs": 2000, "actualMs": 1800},
	}
	decoded := jsonRoundTrip(t, result)
	if decoded.Status != ValidationFailed || !decoded.RetryRecommended {
		t.Fatalf("validation result round trip mismatch: %+v", decoded)
	}
}

func TestResourcePlanJSONRoundTrip(t *testing.T) {
	plan := ResourcePlan{
		ID:           ID("plan-1"),
		TargetID:     ID("gen-req-1"),
		RAMBytes:     4 << 30,
		CPU:          2,
		PreferRemote: false,
		Rationale:    "fits within local 16GB budget",
	}
	decoded := jsonRoundTrip(t, plan)
	if decoded != plan {
		t.Fatalf("resource plan round trip mismatch: got %+v, want %+v", decoded, plan)
	}
}

func TestHardwareRequirementsAndModelCapabilitiesJSONRoundTrip(t *testing.T) {
	hw := HardwareRequirements{MinMemoryGB: 6, RequiresGPU: false}
	decodedHW := jsonRoundTrip(t, hw)
	if decodedHW != hw {
		t.Fatalf("hardware requirements round trip mismatch: got %+v, want %+v", decodedHW, hw)
	}

	model := ModelCapabilities{
		ModelID:     "qwen3-small",
		ProviderID:  "qwen-local",
		Tags:        []string{"semantic-decision"},
		Local:       true,
		MinMemoryGB: 2,
	}
	decodedModel := jsonRoundTrip(t, model)
	if !decodedModel.Supports("semantic-decision") || decodedModel.ModelID != "qwen3-small" {
		t.Fatalf("model capabilities round trip mismatch: %+v", decodedModel)
	}
}

func TestRenderManifestAndTimelineJSONRoundTrip(t *testing.T) {
	elem := VisualElement{
		ID:     ID("elem-1"),
		ShotID: ID("shot-17"),
		Type:   "subtitle",
		Start:  0,
		End:    2 * time.Second,
	}
	manifest := RenderManifest{
		ID:        ID("manifest-1"),
		ShotID:    ID("shot-17"),
		MediaPath: "renders/shot-017.mp4",
		Elements:  []VisualElement{elem},
	}
	decodedManifest := jsonRoundTrip(t, manifest)
	if len(decodedManifest.Elements) != 1 || decodedManifest.Elements[0].Type != "subtitle" {
		t.Fatalf("render manifest round trip mismatch: %+v", decodedManifest)
	}

	timeline := Timeline{
		ID:        ID("timeline-1"),
		ProjectID: ID("proj-1"),
		Entries: []TimelineEntry{
			{ShotID: ID("shot-17"), Start: 30 * time.Second, End: 32 * time.Second},
		},
		Elements: []VisualElement{elem},
	}
	decodedTimeline := jsonRoundTrip(t, timeline)
	if len(decodedTimeline.Entries) != 1 || decodedTimeline.Entries[0].Start != 30*time.Second {
		t.Fatalf("timeline round trip mismatch: %+v", decodedTimeline)
	}
}

func TestReviewFeedbackJSONRoundTrip(t *testing.T) {
	dur := 500 * time.Millisecond
	fb := ReviewFeedback{
		ID:        ID("fb-1"),
		ProjectID: ID("proj-1"),
		ShotID:    ID("shot-17"),
		ElementID: ID("elem-1"),
		Timestamp: 13*time.Second + 420*time.Millisecond,
		Duration:  &dur,
		Type:      FeedbackTypeChangeRequest,
		Note:      "move this subtitle 0.5 seconds earlier",
		CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
	decoded := jsonRoundTrip(t, fb)
	if decoded.Duration == nil || *decoded.Duration != dur {
		t.Fatalf("duration round trip mismatch: got %+v", decoded.Duration)
	}
	if decoded.Type != FeedbackTypeChangeRequest || decoded.Note != fb.Note {
		t.Fatalf("review feedback round trip mismatch: %+v", decoded)
	}

	// Duration is optional: confirm the zero-value (nil) case round trips
	// as absent, not as an explicit zero duration.
	fbNoDuration := fb
	fbNoDuration.Duration = nil
	decodedNoDuration := jsonRoundTrip(t, fbNoDuration)
	if decodedNoDuration.Duration != nil {
		t.Fatalf("expected absent duration to decode as nil, got %v", *decodedNoDuration.Duration)
	}
}

// TestShotRelativeAndProjectRelativeTimingCompose documents and verifies
// the timing convention from DECISIONS.md ADR-007: VisualElement.Start is
// shot-relative, TimelineEntry.Start is project-relative, and adding them
// yields the project-relative timestamp for the same element — even when
// the shot is not at the start of the project.
func TestShotRelativeAndProjectRelativeTimingCompose(t *testing.T) {
	entry := TimelineEntry{
		ShotID: ID("shot-17"),
		Start:  30 * time.Second, // shot-17 begins 30s into the project
		End:    32 * time.Second,
	}
	elem := VisualElement{
		ID:     ID("elem-1"),
		ShotID: ID("shot-17"),
		Type:   "subtitle",
		Start:  1200 * time.Millisecond, // 1.2s into shot-17 itself
		End:    1800 * time.Millisecond,
	}

	projectRelativeStart := entry.Start + elem.Start
	want := 31200 * time.Millisecond
	if projectRelativeStart != want {
		t.Fatalf("project-relative start = %v, want %v", projectRelativeStart, want)
	}
}
