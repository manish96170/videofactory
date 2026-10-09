package render

import (
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestBuildFilterGraphBackgroundOnly(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: 2 * time.Second,
		Primitives: []domain.Primitive{
			{ID: "p1", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#112233"}},
		},
	}
	plan, err := buildFilterGraph(shot, DefaultSettings())
	if err != nil {
		t.Fatalf("buildFilterGraph: %v", err)
	}
	if !strings.Contains(plan.filterComplex, "0x112233") {
		t.Fatalf("expected background color in filter graph: %s", plan.filterComplex)
	}
	if len(plan.inputs) != 0 {
		t.Fatalf("expected no extra inputs for a background-only shot, got %d", len(plan.inputs))
	}
	if plan.outputLabel != "base" {
		t.Fatalf("expected output label to be the base layer, got %q", plan.outputLabel)
	}
}

func TestBuildFilterGraphDefaultsToBlackBackground(t *testing.T) {
	shot := domain.Shot{ID: "shot-1", Duration: time.Second}
	plan, err := buildFilterGraph(shot, DefaultSettings())
	if err != nil {
		t.Fatalf("buildFilterGraph: %v", err)
	}
	if !strings.Contains(plan.filterComplex, "0x000000") {
		t.Fatalf("expected default black background: %s", plan.filterComplex)
	}
}

func TestBuildFilterGraphRejectsDuplicateBackground(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "p1", Type: domain.PrimitiveTypeBackground},
			{ID: "p2", Type: domain.PrimitiveTypeBackground},
		},
	}
	if _, err := buildFilterGraph(shot, DefaultSettings()); err == nil {
		t.Fatalf("expected error for duplicate Background primitives")
	}
}

func TestBuildFilterGraphRejectsDuplicateTransition(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "p1", Type: domain.PrimitiveTypeTransition, Properties: map[string]any{"style": "fade-in"}},
			{ID: "p2", Type: domain.PrimitiveTypeTransition, Properties: map[string]any{"style": "fade-out"}},
		},
	}
	if _, err := buildFilterGraph(shot, DefaultSettings()); err == nil {
		t.Fatalf("expected error for duplicate Transition primitives")
	}
}

func TestBuildFilterGraphRejectsUnsupportedPrimitiveType(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "p1", Type: domain.PrimitiveTypeCodeEditor},
		},
	}
	if _, err := buildFilterGraph(shot, DefaultSettings()); err == nil {
		t.Fatalf("expected error for an unsupported PrimitiveType")
	}
}

func TestBuildFilterGraphTextRequiresText(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "p1", Type: domain.PrimitiveTypeText}, // missing required "text" property
		},
	}
	if _, err := buildFilterGraph(shot, DefaultSettings()); err == nil {
		t.Fatalf("expected error for a Text primitive missing \"text\"")
	}
}

func TestBuildFilterGraphTextRequiresFontPath(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "p1", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "hi"}},
		},
	}
	settings := DefaultSettings()
	settings.FontPath = ""
	if _, err := buildFilterGraph(shot, settings); err == nil {
		t.Fatalf("expected error when no FontPath is configured and a Text primitive is present")
	}
}

func TestBuildFilterGraphImageRequiresExistingAsset(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "p1", Type: domain.PrimitiveTypeImage, Properties: map[string]any{"assetPath": "/no/such/file.png"}},
		},
	}
	if _, err := buildFilterGraph(shot, DefaultSettings()); err == nil {
		t.Fatalf("expected error for a missing image asset")
	}
}

func TestBuildFilterGraphRejectsUnsupportedTransitionStyle(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "p1", Type: domain.PrimitiveTypeTransition, Properties: map[string]any{"style": "spin"}},
		},
	}
	if _, err := buildFilterGraph(shot, DefaultSettings()); err == nil {
		t.Fatalf("expected error for an unsupported transition style")
	}
}

func TestBuildFilterGraphLayersInPrimitiveOrder(t *testing.T) {
	fixture := writeFixturePNG(t)
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: 2 * time.Second,
		Primitives: []domain.Primitive{
			{ID: "bg", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#000000"}},
			{ID: "img", Type: domain.PrimitiveTypeImage, Properties: map[string]any{"assetPath": fixture}},
			{ID: "txt", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "hello"}},
			{ID: "fade", Type: domain.PrimitiveTypeTransition, Properties: map[string]any{"style": "fade-in"}},
		},
	}
	settings := DefaultSettings()
	settings.FontPath = requireFontPath(t)

	plan, err := buildFilterGraph(shot, settings)
	if err != nil {
		t.Fatalf("buildFilterGraph: %v", err)
	}

	fc := plan.filterComplex
	bgIdx := strings.Index(fc, "color=c=")
	overlayIdx := strings.Index(fc, "overlay=")
	drawtextIdx := strings.Index(fc, "drawtext=")
	fadeIdx := strings.Index(fc, "fade=t=in")

	if bgIdx < 0 || overlayIdx < 0 || drawtextIdx < 0 || fadeIdx < 0 {
		t.Fatalf("expected all four stages present in filter graph: %s", fc)
	}
	if !(bgIdx < overlayIdx && overlayIdx < drawtextIdx && drawtextIdx < fadeIdx) {
		t.Fatalf("expected background < overlay < drawtext < fade ordering, got indices %d,%d,%d,%d: %s", bgIdx, overlayIdx, drawtextIdx, fadeIdx, fc)
	}
	if len(plan.inputs) != 1 {
		t.Fatalf("expected exactly 1 extra input (the image), got %d", len(plan.inputs))
	}
}
