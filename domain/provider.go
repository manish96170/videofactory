package domain

// HardwareRequirements describes what a provider/model needs to run
// (PHASES.md section 3/9).
type HardwareRequirements struct {
	MinMemoryGB float64 `json:"minMemoryGb,omitempty"`
	RequiresGPU bool    `json:"requiresGpu,omitempty"`
}

// ProviderCapabilities declares what a Provider can do, so core code
// selects providers by capability instead of by name:
//
//	if provider.Capabilities().Supports("semantic-decision") { ... }
//
// (PHASES.md section 4.2).
type ProviderCapabilities struct {
	ProviderID string `json:"providerId"`
	// Tags is the set of capability tags the provider declares, e.g.
	// "semantic-decision", "realistic-human-motion".
	Tags        []string              `json:"tags"`
	Local       bool                  `json:"local"`
	CostPerUnit float64               `json:"costPerUnit,omitempty"`
	Hardware    *HardwareRequirements `json:"hardware,omitempty"`
}

// Supports reports whether the declared capabilities include tag.
func (c ProviderCapabilities) Supports(tag string) bool {
	for _, t := range c.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
