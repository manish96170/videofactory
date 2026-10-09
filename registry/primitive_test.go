package registry

import (
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func TestNewPrimitiveRegistryCoversEveryDomainPrimitiveType(t *testing.T) {
	r := NewPrimitiveRegistry()

	all := []domain.PrimitiveType{
		domain.PrimitiveTypeCodeEditor, domain.PrimitiveTypeTerminal, domain.PrimitiveTypeBrowser,
		domain.PrimitiveTypeChart, domain.PrimitiveTypeText, domain.PrimitiveTypeSubtitle,
		domain.PrimitiveTypeCursor, domain.PrimitiveTypeImage, domain.PrimitiveTypeTransition,
		domain.PrimitiveTypeDeveloperAvatar, domain.PrimitiveTypeHumanAction, domain.PrimitiveTypeEnvironment,
		domain.PrimitiveTypeCinematicCamera, domain.PrimitiveTypeAnimatedImage, domain.PrimitiveTypeBackground,
	}
	for _, pt := range all {
		if _, ok := r.Get(pt); !ok {
			t.Errorf("expected PrimitiveType %q to be registered", pt)
		}
	}

	if list := r.List(); len(list) != len(all) {
		t.Fatalf("List() returned %d entries, want %d", len(list), len(all))
	}
}

func TestPrimitiveRegistryDeterministicClassificationMatchesPhasesSection6(t *testing.T) {
	r := NewPrimitiveRegistry()

	deterministic := []domain.PrimitiveType{
		domain.PrimitiveTypeCodeEditor, domain.PrimitiveTypeTerminal, domain.PrimitiveTypeBrowser,
		domain.PrimitiveTypeChart, domain.PrimitiveTypeText, domain.PrimitiveTypeSubtitle,
		domain.PrimitiveTypeCursor, domain.PrimitiveTypeImage, domain.PrimitiveTypeTransition,
		domain.PrimitiveTypeBackground,
	}
	for _, pt := range deterministic {
		c, ok := r.Get(pt)
		if !ok || !c.Deterministic {
			t.Errorf("expected %q to be classified Deterministic, got %+v (ok=%v)", pt, c, ok)
		}
		if !c.Supports("deterministic") {
			t.Errorf("expected %q to have the \"deterministic\" tag", pt)
		}
	}

	aiCapable := []domain.PrimitiveType{
		domain.PrimitiveTypeDeveloperAvatar, domain.PrimitiveTypeHumanAction,
		domain.PrimitiveTypeEnvironment, domain.PrimitiveTypeCinematicCamera, domain.PrimitiveTypeAnimatedImage,
	}
	for _, pt := range aiCapable {
		c, ok := r.Get(pt)
		if !ok || c.Deterministic {
			t.Errorf("expected %q to be classified AI-capable (not Deterministic), got %+v (ok=%v)", pt, c, ok)
		}
		if !c.Supports("ai-capable") {
			t.Errorf("expected %q to have the \"ai-capable\" tag", pt)
		}
	}
}

func TestPrimitiveRegistryMatchesRenderPackageExpectations(t *testing.T) {
	// These four are what the Phase 03 render package actually
	// implements; SupportsBounds here must match render/filtergraph.go's
	// real behavior. RequiredProperties lists only properties with no
	// renderer-supplied default: Image/assetPath and Text/text have no
	// default (render errors if absent), while Background/color and
	// Transition/style do have defaults (render/filtergraph.go), so
	// they're intentionally NOT required here — see DECISIONS.md
	// ADR-017's amendment (an earlier version of this test asserted the
	// opposite, which a reviewer correctly flagged as inconsistent with
	// render's actual behavior).
	r := NewPrimitiveRegistry()

	requiredCases := []struct {
		pt             domain.PrimitiveType
		supportsBounds bool
		required       string
	}{
		{domain.PrimitiveTypeImage, true, "assetPath"},
		{domain.PrimitiveTypeText, true, "text"},
	}
	for _, tc := range requiredCases {
		c, ok := r.Get(tc.pt)
		if !ok {
			t.Fatalf("missing entry for %q", tc.pt)
		}
		if c.SupportsBounds != tc.supportsBounds {
			t.Errorf("%q: SupportsBounds = %v, want %v", tc.pt, c.SupportsBounds, tc.supportsBounds)
		}
		found := false
		for _, p := range c.RequiredProperties {
			if p == tc.required {
				found = true
			}
		}
		if !found {
			t.Errorf("%q: RequiredProperties = %v, want to include %q", tc.pt, c.RequiredProperties, tc.required)
		}
	}

	defaultedCases := []struct {
		pt             domain.PrimitiveType
		supportsBounds bool
	}{
		{domain.PrimitiveTypeBackground, false},
		{domain.PrimitiveTypeTransition, false},
	}
	for _, tc := range defaultedCases {
		c, ok := r.Get(tc.pt)
		if !ok {
			t.Fatalf("missing entry for %q", tc.pt)
		}
		if c.SupportsBounds != tc.supportsBounds {
			t.Errorf("%q: SupportsBounds = %v, want %v", tc.pt, c.SupportsBounds, tc.supportsBounds)
		}
		if len(c.RequiredProperties) != 0 {
			t.Errorf("%q: RequiredProperties = %v, want empty (render/filtergraph.go supplies a default)", tc.pt, c.RequiredProperties)
		}
	}
}

func TestPrimitiveRegistryRegisterRejectsEmptyTypeAndDuplicates(t *testing.T) {
	r := NewPrimitiveRegistry()

	if err := r.Register(domain.PrimitiveCapabilities{}); err == nil {
		t.Fatalf("expected error for empty PrimitiveType")
	}
	if err := r.Register(domain.PrimitiveCapabilities{Type: domain.PrimitiveTypeText}); err == nil {
		t.Fatalf("expected error for duplicate PrimitiveType")
	}
}

func TestPrimitiveRegistryRegisterCustomType(t *testing.T) {
	r := NewPrimitiveRegistry()
	custom := domain.PrimitiveCapabilities{
		Type:          domain.PrimitiveType("Waveform"),
		Deterministic: true,
		Tags:          []string{"deterministic", "renders-audio-waveform"},
	}
	if err := r.Register(custom); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get(domain.PrimitiveType("Waveform"))
	if !ok || !got.Deterministic {
		t.Fatalf("Get after Register = %+v, %v", got, ok)
	}
}

// TestPrimitiveRegistryIsolatesSliceFieldsFromCallerMutation guards
// against the same class of aliasing bug as the Flow/Framing registry
// tests, for RequiredProperties and Tags.
func TestPrimitiveRegistryIsolatesSliceFieldsFromCallerMutation(t *testing.T) {
	r := NewPrimitiveRegistry()

	originalTags := []string{"deterministic"}
	custom := domain.PrimitiveCapabilities{Type: domain.PrimitiveType("Waveform2"), Tags: originalTags}
	if err := r.Register(custom); err != nil {
		t.Fatalf("Register: %v", err)
	}
	originalTags[0] = "mutated" // mutate after registering
	got, _ := r.Get(domain.PrimitiveType("Waveform2"))
	if got.Tags[0] != "deterministic" {
		t.Fatalf("mutating the slice passed to Register affected the stored entry: got %v", got.Tags)
	}

	first, _ := r.Get(domain.PrimitiveType("Waveform2"))
	first.Tags[0] = "mutated"
	second, _ := r.Get(domain.PrimitiveType("Waveform2"))
	if second.Tags[0] != "deterministic" {
		t.Fatalf("mutating a Get() result affected a later Get() result: got %v", second.Tags)
	}
}
