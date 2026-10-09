package registry

import (
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func TestNewFlowRegistryHasBuiltins(t *testing.T) {
	r := NewFlowRegistry()

	for _, id := range []domain.ID{"developer-explainer", "code-tutorial"} {
		flow, ok := r.Get(id)
		if !ok {
			t.Fatalf("expected built-in flow %q to be registered", id)
		}
		if flow.Name == "" || flow.Description == "" {
			t.Fatalf("flow %q missing Name/Description: %+v", id, flow)
		}
		if flow.DefaultFramingID.IsZero() {
			t.Fatalf("flow %q missing DefaultFramingID", id)
		}
	}
}

func TestFlowRegistryGetMissing(t *testing.T) {
	r := NewFlowRegistry()
	if _, ok := r.Get("no-such-flow"); ok {
		t.Fatalf("expected Get for an unregistered flow to return ok=false")
	}
}

func TestFlowRegistryRegisterRejectsEmptyIDAndDuplicates(t *testing.T) {
	r := NewFlowRegistry()

	if err := r.Register(domain.Flow{}); err == nil {
		t.Fatalf("expected error for empty flow ID")
	}
	if err := r.Register(domain.Flow{ID: "developer-explainer", Name: "dup"}); err == nil {
		t.Fatalf("expected error for duplicate flow ID")
	}
}

func TestFlowRegistryRegisterCustomFlow(t *testing.T) {
	r := NewFlowRegistry()
	custom := domain.Flow{ID: "cinematic-product-demo", Name: "cinematic-product-demo"}
	if err := r.Register(custom); err != nil {
		t.Fatalf("Register: %v", err)
	}
	got, ok := r.Get("cinematic-product-demo")
	if !ok || got.Name != "cinematic-product-demo" {
		t.Fatalf("Get after Register = %+v, %v", got, ok)
	}
}

// TestFlowRegistryIsolatesSliceFieldsFromCallerMutation guards against
// a real issue a reviewer found: Register/Get/List previously returned
// values that shared backing arrays with caller-owned slices, so
// mutating one could silently corrupt the registry (or vice versa)
// despite the mutex, since the mutex only protects the map itself, not
// aliased slice contents.
func TestFlowRegistryIsolatesSliceFieldsFromCallerMutation(t *testing.T) {
	r := NewFlowRegistry()

	// Mutating the slice passed to Register must not affect the stored
	// entry.
	original := []domain.ShotCategory{domain.ShotCategoryCode}
	if err := r.Register(domain.Flow{ID: "custom-flow", RecommendedCategories: original}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	original[0] = domain.ShotCategoryTerminal // mutate after registering
	got, _ := r.Get("custom-flow")
	if got.RecommendedCategories[0] != domain.ShotCategoryCode {
		t.Fatalf("mutating the slice passed to Register affected the stored entry: got %v", got.RecommendedCategories)
	}

	// Mutating a slice returned by Get must not affect what a later
	// Get/List call returns.
	first, _ := r.Get("custom-flow")
	first.RecommendedCategories[0] = domain.ShotCategoryTerminal
	second, _ := r.Get("custom-flow")
	if second.RecommendedCategories[0] != domain.ShotCategoryCode {
		t.Fatalf("mutating a Get() result affected a later Get() result: got %v", second.RecommendedCategories)
	}

	// Same for List().
	listed := r.List()
	for i := range listed {
		if listed[i].ID == "custom-flow" {
			listed[i].RecommendedCategories[0] = domain.ShotCategoryTerminal
		}
	}
	third, _ := r.Get("custom-flow")
	if third.RecommendedCategories[0] != domain.ShotCategoryCode {
		t.Fatalf("mutating a List() result affected a later Get() result: got %v", third.RecommendedCategories)
	}
}

func TestFlowRegistryListIsSortedAndComplete(t *testing.T) {
	r := NewFlowRegistry()
	list := r.List()
	if len(list) != 2 {
		t.Fatalf("expected 2 built-in flows, got %d", len(list))
	}
	for i := 1; i < len(list); i++ {
		if list[i-1].ID >= list[i].ID {
			t.Fatalf("expected List() sorted by ID, got %+v", list)
		}
	}
}
