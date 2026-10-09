package registry

import (
	"math"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func TestNewFramingRegistryHasBuiltins(t *testing.T) {
	r := NewFramingRegistry()

	wantIDs := []domain.ID{"fullscreen", "split-screen", "picture-in-picture", "developer-circle", "screen-focused"}
	for _, id := range wantIDs {
		framing, ok := r.Get(id)
		if !ok {
			t.Fatalf("expected built-in framing %q to be registered", id)
		}
		if len(framing.Slots) == 0 {
			t.Fatalf("framing %q has no slots", id)
		}
	}

	if list := r.List(); len(list) != len(wantIDs) {
		t.Fatalf("List() returned %d framings, want %d", len(list), len(wantIDs))
	}
}

func TestFramingRegistrySlotShapesMatchDescription(t *testing.T) {
	r := NewFramingRegistry()

	fullscreen, _ := r.Get("fullscreen")
	if len(fullscreen.Slots) != 1 || fullscreen.Slots[0].Name != "main" {
		t.Fatalf("fullscreen slots = %+v, want exactly one \"main\" slot", fullscreen.Slots)
	}

	split, _ := r.Get("split-screen")
	if len(split.Slots) != 2 {
		t.Fatalf("split-screen slots = %+v, want 2", split.Slots)
	}

	pip, _ := r.Get("picture-in-picture")
	if len(pip.Slots) != 2 {
		t.Fatalf("picture-in-picture slots = %+v, want 2 (main + overlay)", pip.Slots)
	}

	devCircle, _ := r.Get("developer-circle")
	if len(devCircle.Slots) != 2 {
		t.Fatalf("developer-circle slots = %+v, want 2 (main + overlay)", devCircle.Slots)
	}

	screenFocused, _ := r.Get("screen-focused")
	if len(screenFocused.Slots) != 1 {
		t.Fatalf("screen-focused slots = %+v, want exactly one slot", screenFocused.Slots)
	}
}

func TestFramingRegistryRegisterRejectsInvalidFramings(t *testing.T) {
	r := NewFramingRegistry()

	cases := []domain.Framing{
		{}, // empty ID
		{ID: "no-slots"},
		{ID: "empty-slot-name", Slots: []domain.FramingSlot{{Name: "", Bounds: domain.Bounds{Width: 1, Height: 1}}}},
		{ID: "dup-slot-name", Slots: []domain.FramingSlot{
			{Name: "main", Bounds: domain.Bounds{Width: 1, Height: 1}},
			{Name: "main", Bounds: domain.Bounds{Width: 0.5, Height: 0.5}},
		}},
		{ID: "bad-bounds", Slots: []domain.FramingSlot{{Name: "main", Bounds: domain.Bounds{Width: 0, Height: 1}}}},
		{ID: "fullscreen", Slots: []domain.FramingSlot{{Name: "main", Bounds: domain.Bounds{Width: 1, Height: 1}}}}, // duplicate of a built-in
	}
	for _, c := range cases {
		if err := r.Register(c); err == nil {
			t.Errorf("Register(%+v) = nil, want an error", c)
		}
	}
}

func TestFramingRegistryRegisterCustomFraming(t *testing.T) {
	r := NewFramingRegistry()
	custom := domain.Framing{
		ID:   "quad-split",
		Name: "quad-split",
		Slots: []domain.FramingSlot{
			{Name: "top-left", Bounds: domain.Bounds{X: 0, Y: 0, Width: 0.5, Height: 0.5}},
			{Name: "top-right", Bounds: domain.Bounds{X: 0.5, Y: 0, Width: 0.5, Height: 0.5}},
			{Name: "bottom-left", Bounds: domain.Bounds{X: 0, Y: 0.5, Width: 0.5, Height: 0.5}},
			{Name: "bottom-right", Bounds: domain.Bounds{X: 0.5, Y: 0.5, Width: 0.5, Height: 0.5}},
		},
	}
	if err := r.Register(custom); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("quad-split")
	if !ok || len(got.Slots) != 4 {
		t.Fatalf("Get after Register = %+v, %v", got, ok)
	}
}

func TestFramingRegistryRejectsNonFiniteBounds(t *testing.T) {
	r := NewFramingRegistry()

	nan := domain.Framing{ID: "nan-framing", Slots: []domain.FramingSlot{
		{Name: "main", Bounds: domain.Bounds{Width: math.NaN(), Height: 1}},
	}}
	if err := r.Register(nan); err == nil {
		t.Fatalf("expected error for a NaN bounds field")
	}

	inf := domain.Framing{ID: "inf-framing", Slots: []domain.FramingSlot{
		{Name: "main", Bounds: domain.Bounds{Width: math.Inf(1), Height: 1}},
	}}
	if err := r.Register(inf); err == nil {
		t.Fatalf("expected error for an infinite bounds field")
	}
}

// TestFramingRegistryIsolatesSliceFieldsFromCallerMutation guards
// against the same class of aliasing bug as the FlowRegistry test
// above, for Framing.Slots.
func TestFramingRegistryIsolatesSliceFieldsFromCallerMutation(t *testing.T) {
	r := NewFramingRegistry()

	originalSlots := []domain.FramingSlot{{Name: "main", Bounds: domain.Bounds{Width: 1, Height: 1}}}
	if err := r.Register(domain.Framing{ID: "custom-framing", Slots: originalSlots}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	originalSlots[0].Name = "mutated" // mutate after registering
	got, _ := r.Get("custom-framing")
	if got.Slots[0].Name != "main" {
		t.Fatalf("mutating the slice passed to Register affected the stored entry: got %+v", got.Slots)
	}

	first, _ := r.Get("custom-framing")
	first.Slots[0].Name = "mutated"
	second, _ := r.Get("custom-framing")
	if second.Slots[0].Name != "main" {
		t.Fatalf("mutating a Get() result affected a later Get() result: got %+v", second.Slots)
	}
}
