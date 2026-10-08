package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProjectJSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	original := Project{
		ID:   ID("proj-1"),
		Name: "demo",
		Plan: &VideoPlan{
			ID:        ID("plan-1"),
			ProjectID: ID("proj-1"),
			FlowID:    ID("flow-developer-explainer"),
			FramingID: ID("framing-fullscreen"),
			Shots: []Shot{
				{
					ID:            ID("shot-1"),
					PlanID:        ID("plan-1"),
					Index:         0,
					Category:      ShotCategoryCode,
					Deterministic: true,
					Duration:      2 * time.Second,
					Primitives: []Primitive{
						{ID: ID("prim-1"), Type: PrimitiveTypeCodeEditor, Properties: map[string]any{"lang": "go"}},
					},
					DependsOn: []ID{"shot-0"},
				},
			},
			CreatedAt: now,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded Project
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.ID != original.ID || decoded.Name != original.Name {
		t.Fatalf("round trip mismatch: got %+v, want %+v", decoded, original)
	}
	if decoded.Plan == nil || len(decoded.Plan.Shots) != 1 {
		t.Fatalf("expected plan with 1 shot, got %+v", decoded.Plan)
	}
	shot := decoded.Plan.Shots[0]
	if shot.Category != ShotCategoryCode || !shot.Deterministic {
		t.Fatalf("shot round trip mismatch: %+v", shot)
	}
	if shot.Duration != 2*time.Second {
		t.Fatalf("duration round trip mismatch: got %v", shot.Duration)
	}
	if len(shot.DependsOn) != 1 || shot.DependsOn[0] != "shot-0" {
		t.Fatalf("dependsOn round trip mismatch: %+v", shot.DependsOn)
	}
}

func TestIDIsZero(t *testing.T) {
	var zero ID
	if !zero.IsZero() {
		t.Fatalf("expected empty ID to be zero")
	}
	if ID("x").IsZero() {
		t.Fatalf("expected non-empty ID to not be zero")
	}
}

func TestProviderCapabilitiesSupports(t *testing.T) {
	caps := ProviderCapabilities{
		ProviderID: "qwen-local",
		Tags:       []string{"semantic-decision", "realistic-human-motion"},
		Local:      true,
	}

	if !caps.Supports("semantic-decision") {
		t.Fatalf("expected caps to support semantic-decision")
	}
	if caps.Supports("cloud-only-feature") {
		t.Fatalf("expected caps to not support cloud-only-feature")
	}
}

func TestModelCapabilitiesSupports(t *testing.T) {
	model := ModelCapabilities{
		ModelID:    "qwen3-small",
		ProviderID: "qwen-local",
		Tags:       []string{"semantic-decision"},
		Local:      true,
	}

	if !model.Supports("semantic-decision") {
		t.Fatalf("expected model to support semantic-decision")
	}
	if model.Supports("video-generation") {
		t.Fatalf("expected model to not support video-generation")
	}
}

func TestVisualElementJSONRoundTrip(t *testing.T) {
	elem := VisualElement{
		ID:     ID("elem-1"),
		ShotID: ID("shot-17"),
		Type:   "subtitle",
		Start:  13*time.Second + 420*time.Millisecond,
		End:    15 * time.Second,
		Bounds: &Bounds{X: 0.1, Y: 0.8, Width: 0.5, Height: 0.1},
		EditableProperties: map[string]any{
			"text": "hello world",
		},
	}

	data, err := json.Marshal(elem)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded VisualElement
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Start != elem.Start || decoded.End != elem.End {
		t.Fatalf("timestamp round trip mismatch: got start=%v end=%v", decoded.Start, decoded.End)
	}
	if decoded.Bounds == nil || *decoded.Bounds != *elem.Bounds {
		t.Fatalf("bounds round trip mismatch: got %+v", decoded.Bounds)
	}
}

func TestChangeSetJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	cs := ChangeSet{
		ID:        ID("cs-1"),
		ProjectID: ID("proj-1"),
		Source:    "review-feedback",
		Changes: []Change{
			{TargetID: ID("elem-1"), Op: "shift-timestamp", Payload: map[string]any{"deltaMs": -500}},
		},
		Status:    ChangeSetProposed,
		CreatedAt: now,
	}

	data, err := json.Marshal(cs)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ChangeSet
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.Status != ChangeSetProposed || decoded.Source != "review-feedback" {
		t.Fatalf("changeset round trip mismatch: %+v", decoded)
	}
	if len(decoded.Changes) != 1 || decoded.Changes[0].Op != "shift-timestamp" {
		t.Fatalf("change round trip mismatch: %+v", decoded.Changes)
	}
	// map[string]any decodes JSON numbers as float64 (see package doc /
	// DECISIONS.md ADR-008); assert that explicitly rather than only
	// checking the key's presence.
	deltaMs, ok := decoded.Changes[0].Payload["deltaMs"].(float64)
	if !ok || deltaMs != -500 {
		t.Fatalf("payload round trip mismatch: got %#v (%T)", decoded.Changes[0].Payload["deltaMs"], decoded.Changes[0].Payload["deltaMs"])
	}
	if !decoded.CreatedAt.Equal(now) {
		t.Fatalf("createdAt round trip mismatch: got %v want %v", decoded.CreatedAt, now)
	}
}

func TestDecisionResultJSONRoundTrip(t *testing.T) {
	result := DecisionResult{
		Decision:   DecisionYes,
		Score:      0.94,
		ReasonCode: "REALISTIC_HUMAN_MOTION",
		Provider:   "jev",
	}

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded DecisionResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded != result {
		t.Fatalf("decision result round trip mismatch: got %+v, want %+v", decoded, result)
	}
}
