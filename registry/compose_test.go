package registry

import (
	"testing"

	"github.com/manish96170/videofactory/domain"
)

// TestAssignSlotsWorksGenericallyAcrossAllBuiltinFramings is the direct
// proof that AssignSlots contains no framing-specific logic: the same
// loop, calling the same function, handles every built-in framing
// (including multi-slot ones) without any per-framing branching in the
// test itself, let alone in AssignSlots. It uses Image primitives
// (which actually support positioning per the primitive registry —
// unlike Background, which render/filtergraph.go ignores bounds for)
// and asserts every slot's exact bounds, not just that a "bounds" key
// is present (a reviewer correctly noted the original version of this
// test could pass even if slot N's bounds were assigned to the wrong
// primitive).
func TestAssignSlotsWorksGenericallyAcrossAllBuiltinFramings(t *testing.T) {
	registry := NewFramingRegistry()

	for _, framing := range registry.List() {
		content := make(map[string]domain.Primitive, len(framing.Slots))
		for _, slot := range framing.Slots {
			content[slot.Name] = domain.Primitive{
				ID:         domain.ID("prim-" + slot.Name),
				Type:       domain.PrimitiveTypeImage,
				Properties: map[string]any{"assetPath": "/fixtures/" + slot.Name + ".png"},
			}
		}

		assigned, err := AssignSlots(framing, content)
		if err != nil {
			t.Errorf("AssignSlots(%q): %v", framing.ID, err)
			continue
		}
		if len(assigned) != len(framing.Slots) {
			t.Errorf("AssignSlots(%q) returned %d primitives, want %d", framing.ID, len(assigned), len(framing.Slots))
			continue
		}
		// assigned is in framing.Slots order (see AssignSlots's doc
		// comment), so index-aligned comparison is valid here.
		for i, slot := range framing.Slots {
			p := assigned[i]
			if p.ID != domain.ID("prim-"+slot.Name) {
				t.Errorf("AssignSlots(%q): position %d has ID %q, want %q (wrong slot/primitive pairing)", framing.ID, i, p.ID, "prim-"+slot.Name)
				continue
			}
			bounds, ok := p.Properties["bounds"].(domain.Bounds)
			if !ok {
				t.Errorf("AssignSlots(%q): primitive %q has no domain.Bounds \"bounds\" property", framing.ID, p.ID)
				continue
			}
			if bounds != slot.Bounds {
				t.Errorf("AssignSlots(%q): primitive %q bounds = %+v, want exactly slot %q's bounds %+v", framing.ID, p.ID, bounds, slot.Name, slot.Bounds)
			}
			if p.Properties["assetPath"] != "/fixtures/"+slot.Name+".png" {
				t.Errorf("AssignSlots(%q): primitive %q lost its original assetPath property: %+v", framing.ID, p.ID, p.Properties)
			}
		}
	}
}

// TestAssignSlotsWorksForARuntimeRegisteredFramingWithNoCodeChanges
// registers a brand-new framing (not one of the five built-ins) and
// runs it through the exact same AssignSlots function, with no
// modification to AssignSlots or this package. This is the concrete
// demonstration of PHASES.md Phase 04's "ensure framing is not
// embedded into business logic": a new framing just works.
func TestAssignSlotsWorksForARuntimeRegisteredFramingWithNoCodeChanges(t *testing.T) {
	framings := NewFramingRegistry()
	custom := domain.Framing{
		ID:   "triple-split",
		Name: "triple-split",
		Slots: []domain.FramingSlot{
			{Name: "left", Bounds: domain.Bounds{X: 0, Y: 0, Width: 1.0 / 3, Height: 1}},
			{Name: "center", Bounds: domain.Bounds{X: 1.0 / 3, Y: 0, Width: 1.0 / 3, Height: 1}},
			{Name: "right", Bounds: domain.Bounds{X: 2.0 / 3, Y: 0, Width: 1.0 / 3, Height: 1}},
		},
	}
	if err := framings.Register(custom); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := framings.Get("triple-split")
	if !ok {
		t.Fatalf("Get: not found after Register")
	}

	content := map[string]domain.Primitive{
		"left":   {ID: "p1", Type: domain.PrimitiveTypeImage, Properties: map[string]any{"assetPath": "/a.png"}},
		"center": {ID: "p2", Type: domain.PrimitiveTypeImage, Properties: map[string]any{"assetPath": "/b.png"}},
		"right":  {ID: "p3", Type: domain.PrimitiveTypeImage, Properties: map[string]any{"assetPath": "/c.png"}},
	}
	assigned, err := AssignSlots(got, content)
	if err != nil {
		t.Fatalf("AssignSlots: %v", err)
	}
	if len(assigned) != 3 {
		t.Fatalf("expected 3 assigned primitives, got %d", len(assigned))
	}
	// Order follows the framing's slot order (left, center, right).
	if assigned[0].ID != "p1" || assigned[1].ID != "p2" || assigned[2].ID != "p3" {
		t.Fatalf("expected slot-order assignment, got %+v", assigned)
	}
	// Original properties (assetPath) must survive alongside the new
	// bounds, and every slot's bounds — not just the first — must
	// exactly match that specific slot's own bounds (a reviewer
	// correctly noted the original version of this test only checked
	// index 0, so a bug that mixed up slot 1/2's bounds would have
	// gone undetected).
	wantAssetPath := []string{"/a.png", "/b.png", "/c.png"}
	for i, p := range assigned {
		if p.Properties["assetPath"] != wantAssetPath[i] {
			t.Fatalf("assigned[%d]: expected original assetPath %q to be preserved, got %+v", i, wantAssetPath[i], p.Properties)
		}
		bounds, ok := p.Properties["bounds"].(domain.Bounds)
		if !ok || bounds != got.Slots[i].Bounds {
			t.Fatalf("assigned[%d]: bounds = %+v, want exactly slot %q's bounds %+v", i, p.Properties["bounds"], got.Slots[i].Name, got.Slots[i].Bounds)
		}
	}
}

func TestAssignSlotsLeavesUnfilledSlotsOut(t *testing.T) {
	framings := NewFramingRegistry()
	pip, _ := framings.Get("picture-in-picture")

	// Only "main" supplied; "overlay" is optional and left unfilled.
	content := map[string]domain.Primitive{
		"main": {ID: "p1", Type: domain.PrimitiveTypeBackground},
	}
	assigned, err := AssignSlots(pip, content)
	if err != nil {
		t.Fatalf("AssignSlots: %v", err)
	}
	if len(assigned) != 1 || assigned[0].ID != "p1" {
		t.Fatalf("expected exactly the \"main\" primitive, got %+v", assigned)
	}
}

func TestAssignSlotsRejectsUnknownSlotName(t *testing.T) {
	framings := NewFramingRegistry()
	fullscreen, _ := framings.Get("fullscreen")

	content := map[string]domain.Primitive{
		"typo-of-main": {ID: "p1", Type: domain.PrimitiveTypeBackground},
	}
	if _, err := AssignSlots(fullscreen, content); err == nil {
		t.Fatalf("expected error for an unknown slot name")
	}
}

func TestAssignSlotsDoesNotMutateInputPrimitiveProperties(t *testing.T) {
	framings := NewFramingRegistry()
	fullscreen, _ := framings.Get("fullscreen")

	original := map[string]any{"color": "#000000"}
	prim := domain.Primitive{ID: "p1", Type: domain.PrimitiveTypeBackground, Properties: original}
	content := map[string]domain.Primitive{"main": prim}

	if _, err := AssignSlots(fullscreen, content); err != nil {
		t.Fatalf("AssignSlots: %v", err)
	}
	if _, hasBounds := original["bounds"]; hasBounds {
		t.Fatalf("expected AssignSlots to not mutate the caller's original Properties map, but \"bounds\" leaked into it: %+v", original)
	}
}

func TestSlotNames(t *testing.T) {
	framings := NewFramingRegistry()
	pip, _ := framings.Get("picture-in-picture")
	names := SlotNames(pip)
	if len(names) != 2 || names[0] != "main" || names[1] != "overlay" {
		t.Fatalf("SlotNames(picture-in-picture) = %v, want [main overlay]", names)
	}
}
