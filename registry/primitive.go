package registry

import (
	"fmt"
	"sort"
	"sync"

	"github.com/manish96170/videofactory/domain"
)

// PrimitiveRegistry looks up domain.PrimitiveCapabilities by
// PrimitiveType. The zero value is not usable; construct one with
// NewPrimitiveRegistry.
type PrimitiveRegistry struct {
	mu      sync.RWMutex
	entries map[domain.PrimitiveType]domain.PrimitiveCapabilities
}

// NewPrimitiveRegistry returns a PrimitiveRegistry seeded with
// capability declarations for every domain.PrimitiveType constant,
// classified per PHASES.md section 6's deterministic-vs-AI-capable
// split. Declaring a capability here does not imply the render package
// (or any other renderer) implements that primitive yet — Phase 03's
// FFmpegRenderer only renders Background/Image/Text/Transition today.
// RequiredProperties lists only properties with *no* renderer-supplied
// default (so render/filtergraph.go errors if they're absent); a
// property render defaults when missing (e.g. Background's "color",
// Transition's "style") is intentionally not listed as required. The
// rest of SupportsBounds/RequiredProperties/Tags are forward-looking
// metadata for later phases.
func NewPrimitiveRegistry() *PrimitiveRegistry {
	r := &PrimitiveRegistry{entries: make(map[domain.PrimitiveType]domain.PrimitiveCapabilities)}
	for _, c := range builtinPrimitiveCapabilities() {
		if err := r.Register(c); err != nil {
			panic(fmt.Sprintf("registry: invalid built-in primitive capabilities %q: %v", c.Type, err))
		}
	}
	return r
}

func builtinPrimitiveCapabilities() []domain.PrimitiveCapabilities {
	return []domain.PrimitiveCapabilities{
		// Deterministic, implemented by render (Phase 03).
		{
			Type:           domain.PrimitiveTypeBackground,
			Deterministic:  true,
			SupportsBounds: false, // always fills the whole frame
			// "color" has a renderer-supplied default (#000000,
			// render/filtergraph.go) and so is not actually required;
			// no RequiredProperties entry for it (found during Phase 04
			// review — see DECISIONS.md ADR-017's amendment).
			Tags: []string{"deterministic", "renders-background"},
		},
		{
			Type:               domain.PrimitiveTypeImage,
			Deterministic:      true,
			SupportsBounds:     true,
			RequiredProperties: []string{"assetPath"}, // no default; render/filtergraph.go errors if absent
			Tags:               []string{"deterministic", "renders-image"},
		},
		{
			Type:               domain.PrimitiveTypeText,
			Deterministic:      true,
			SupportsBounds:     true,
			RequiredProperties: []string{"text"}, // no default; render/filtergraph.go errors if absent
			Tags:               []string{"deterministic", "renders-text"},
		},
		{
			Type:           domain.PrimitiveTypeTransition,
			Deterministic:  true,
			SupportsBounds: false, // a whole-shot effect, not a positioned element
			// "style" has a renderer-supplied default ("fade-in",
			// render/filtergraph.go) and so is not actually required.
			Tags: []string{"deterministic", "renders-transition"},
		},

		// Deterministic per PHASES.md section 6, not yet implemented by
		// any renderer.
		{
			Type:               domain.PrimitiveTypeCodeEditor,
			Deterministic:      true,
			SupportsBounds:     true,
			RequiredProperties: nil,
			Tags:               []string{"deterministic", "renders-code"},
		},
		{
			Type:           domain.PrimitiveTypeTerminal,
			Deterministic:  true,
			SupportsBounds: true,
			Tags:           []string{"deterministic", "renders-terminal"},
		},
		{
			Type:           domain.PrimitiveTypeBrowser,
			Deterministic:  true,
			SupportsBounds: true,
			Tags:           []string{"deterministic", "renders-browser"},
		},
		{
			Type:           domain.PrimitiveTypeChart,
			Deterministic:  true,
			SupportsBounds: true,
			Tags:           []string{"deterministic", "renders-chart"},
		},
		{
			Type:               domain.PrimitiveTypeSubtitle,
			Deterministic:      true,
			SupportsBounds:     true,
			RequiredProperties: []string{"text"},
			Tags:               []string{"deterministic", "renders-subtitle"},
		},
		{
			Type:           domain.PrimitiveTypeCursor,
			Deterministic:  true,
			SupportsBounds: true,
			Tags:           []string{"deterministic", "renders-cursor"},
		},

		// AI-capable per PHASES.md section 6 — require a generative
		// provider (Phase 07/14/15), not implemented by the
		// deterministic renderer.
		{
			Type:           domain.PrimitiveTypeDeveloperAvatar,
			Deterministic:  false,
			SupportsBounds: true,
			Tags:           []string{"ai-capable", "realistic-human-motion"},
		},
		{
			Type:           domain.PrimitiveTypeHumanAction,
			Deterministic:  false,
			SupportsBounds: true,
			Tags:           []string{"ai-capable", "realistic-human-motion"},
		},
		{
			Type:           domain.PrimitiveTypeEnvironment,
			Deterministic:  false,
			SupportsBounds: true,
			Tags:           []string{"ai-capable", "realistic-environment"},
		},
		{
			Type:           domain.PrimitiveTypeCinematicCamera,
			Deterministic:  false,
			SupportsBounds: true,
			Tags:           []string{"ai-capable", "cinematic-camera-movement"},
		},
		{
			Type:               domain.PrimitiveTypeAnimatedImage,
			Deterministic:      false,
			SupportsBounds:     true,
			RequiredProperties: []string{"assetPath"},
			Tags:               []string{"ai-capable", "image-animation"},
		},
	}
}

// Register adds c to the registry. It returns an error if c.Type is
// empty or already registered. c's slice fields (RequiredProperties,
// Tags) are defensively copied, so later mutation of the caller's
// original value cannot alter the registered entry.
func (r *PrimitiveRegistry) Register(c domain.PrimitiveCapabilities) error {
	if c.Type == "" {
		return fmt.Errorf("registry: primitive type must not be empty")
	}
	stored := clonePrimitiveCapabilities(c)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[c.Type]; exists {
		return fmt.Errorf("registry: primitive type %q is already registered", c.Type)
	}
	r.entries[c.Type] = stored
	return nil
}

// Get returns the capabilities registered for t, and whether they were
// found. The returned value is a defensive copy: mutating its
// RequiredProperties/Tags slices cannot affect the registry's stored
// entry.
func (r *PrimitiveRegistry) Get(t domain.PrimitiveType) (domain.PrimitiveCapabilities, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.entries[t]
	if !ok {
		return domain.PrimitiveCapabilities{}, false
	}
	return clonePrimitiveCapabilities(c), true
}

// List returns every registered primitive's capabilities, ordered by
// Type for determinism. Each returned value is a defensive copy (see
// Get).
func (r *PrimitiveRegistry) List() []domain.PrimitiveCapabilities {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.PrimitiveCapabilities, 0, len(r.entries))
	for _, c := range r.entries {
		out = append(out, clonePrimitiveCapabilities(c))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// clonePrimitiveCapabilities returns a copy of c whose
// RequiredProperties/Tags slices do not alias c's, so the registry's
// internal state and anything a caller holds can never affect each
// other through a shared slice (found during Phase 04 review — see
// DECISIONS.md ADR-017's amendment).
func clonePrimitiveCapabilities(c domain.PrimitiveCapabilities) domain.PrimitiveCapabilities {
	c.RequiredProperties = append([]string(nil), c.RequiredProperties...)
	c.Tags = append([]string(nil), c.Tags...)
	return c
}
