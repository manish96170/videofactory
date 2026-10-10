package domain

// ModelCapabilities declares what a specific model (as opposed to a whole
// Provider, which may offer several models) can do and what it costs to
// run, so routing never needs to special-case a model by name:
//
//	// bad
//	if model == "qwen3" { ... }
//
//	// good
//	if model.Supports("semantic-decision") { ... }
//
// (PHASES.md section 4.2).
type ModelCapabilities struct {
	ModelID     string   `json:"modelId"`
	ProviderID  string   `json:"providerId"`
	Tags        []string `json:"tags"`
	Local       bool     `json:"local"`
	MinMemoryGB float64  `json:"minMemoryGb,omitempty"` // Legacy decimal-GB floor, never a peak estimate.
	RequiresGPU bool     `json:"requiresGpu,omitempty"`
	// Requirements must come from trusted adapter/configuration declarations,
	// not generated plans. Nil requirements cannot admit local execution.
	Requirements *ModelRequirements `json:"requirements,omitempty"`
}

// Supports reports whether the model declares the given capability tag.
func (m ModelCapabilities) Supports(tag string) bool {
	for _, t := range m.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
