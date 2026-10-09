// Package registry implements Phase 04's Flow/Framing/Primitive
// registries, so video composition is reusable instead of hard-coded
// (PHASES.md "PHASE 04 — FLOWS / FRAMING / PRIMITIVES").
//
// Each registry is a small, mutex-protected lookup table seeded with
// built-in entries, open to runtime registration of more. None of this
// package's own logic special-cases a specific Flow/Framing/Primitive
// ID or Type by name — see compose.go's AssignSlots, which is the
// concrete proof that "framing is not embedded into business logic."
package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/manish96170/videofactory/domain"
)

// FlowRegistry looks up domain.Flow entries by ID. The zero value is
// not usable; construct one with NewFlowRegistry.
type FlowRegistry struct {
	mu      sync.RWMutex
	entries map[domain.ID]domain.Flow
}

// NewFlowRegistry returns a FlowRegistry seeded with the built-in flows
// named in PHASES.md section 1/7: "developer-explainer" and
// "code-tutorial".
func NewFlowRegistry() *FlowRegistry {
	r := &FlowRegistry{entries: make(map[domain.ID]domain.Flow)}
	for _, f := range builtinFlows() {
		if err := r.Register(f); err != nil {
			// Only reachable if a built-in definition below is itself
			// invalid (e.g. a duplicate ID) — a programming error in
			// this package, not a runtime condition callers can hit.
			panic(fmt.Sprintf("registry: invalid built-in flow %q: %v", f.ID, err))
		}
	}
	return r
}

func builtinFlows() []domain.Flow {
	return []domain.Flow{
		{
			ID:               "developer-explainer",
			Name:             "developer-explainer",
			Description:      "A developer narrates while explaining code, with a small avatar/webcam presence on screen.",
			DefaultFramingID: "developer-circle",
			RecommendedCategories: []domain.ShotCategory{
				domain.ShotCategoryAvatar,
				domain.ShotCategoryCode,
				domain.ShotCategoryTerminal,
				domain.ShotCategoryUI,
			},
		},
		{
			ID:               "code-tutorial",
			Name:             "code-tutorial",
			Description:      "A screen-focused walkthrough of code/terminal/UI, minimizing on-screen distraction from the content.",
			DefaultFramingID: "screen-focused",
			RecommendedCategories: []domain.ShotCategory{
				domain.ShotCategoryCode,
				domain.ShotCategoryTerminal,
				domain.ShotCategoryUI,
			},
		},
	}
}

// Register adds flow to the registry. It returns an error if flow.ID
// is empty or already registered. flow's slice field
// (RecommendedCategories) is defensively copied, so later mutation of
// the caller's original value cannot alter the registered entry.
func (r *FlowRegistry) Register(flow domain.Flow) error {
	if flow.ID.IsZero() {
		return fmt.Errorf("registry: flow ID must not be empty")
	}
	stored := cloneFlow(flow)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[flow.ID]; exists {
		return fmt.Errorf("registry: flow %q is already registered", flow.ID)
	}
	r.entries[flow.ID] = stored
	return nil
}

// Get returns the flow registered under id, and whether it was found.
// The returned value is a defensive copy: mutating its
// RecommendedCategories slice cannot affect the registry's stored
// entry.
func (r *FlowRegistry) Get(id domain.ID) (domain.Flow, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.entries[id]
	if !ok {
		return domain.Flow{}, false
	}
	return cloneFlow(f), true
}

// List returns every registered flow, ordered by ID for determinism.
// Each returned value is a defensive copy (see Get).
func (r *FlowRegistry) List() []domain.Flow {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Flow, 0, len(r.entries))
	for _, f := range r.entries {
		out = append(out, cloneFlow(f))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// cloneFlow returns a copy of f whose RecommendedCategories slice does
// not alias f's, so the registry's internal state and anything a
// caller holds can never affect each other through this shared slice
// (PHASES.md section 19; found during Phase 04 review — see
// DECISIONS.md ADR-017's amendment).
func cloneFlow(f domain.Flow) domain.Flow {
	f.RecommendedCategories = append([]domain.ShotCategory(nil), f.RecommendedCategories...)
	return f
}
