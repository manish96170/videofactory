package domain

// PrimitiveCapabilities declares what a PrimitiveType can do and what
// it requires, using the same capability-tag pattern as
// ProviderCapabilities/ModelCapabilities (PHASES.md section 4.2): code
// should check capabilities, not special-case a PrimitiveType by name
// wherever reasonably possible.
//
// Deterministic classifies the primitive per PHASES.md section 6's
// deterministic-vs-AI-capable split; it describes the primitive's
// conceptual nature, not whether any current renderer backend actually
// implements it yet (compare: the render package's own, narrower
// "which primitives can I render today" check in Phase 03).
type PrimitiveCapabilities struct {
	Type          PrimitiveType `json:"type"`
	Deterministic bool          `json:"deterministic"`
	// SupportsBounds reports whether this primitive type can be
	// positioned via Primitive.Properties["bounds"] (domain.Bounds).
	SupportsBounds bool `json:"supportsBounds"`
	// RequiredProperties names the Primitive.Properties keys a
	// renderer needs to render this primitive. Advisory metadata, not
	// enforced by this package.
	RequiredProperties []string `json:"requiredProperties,omitempty"`
	Tags               []string `json:"tags,omitempty"`
}

// Supports reports whether the declared capabilities include tag.
func (c PrimitiveCapabilities) Supports(tag string) bool {
	for _, t := range c.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
