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
	Tags  []string `json:"tags"`
	Local bool     `json:"local"`
	// CostPerUnit is a finite, nonnegative declaration, not a billing guarantee.
	// Zero with a CostUnit is explicitly free; zero without one is unknown.
	CostPerUnit float64 `json:"costPerUnit,omitempty"`
	// CostUnit is an exact currency/unit label, e.g. "USD/decision". Nonzero
	// cost requires a nonblank label. Routers never convert currencies or units.
	CostUnit string `json:"costUnit,omitempty"`
	// EstimatedLatencyMs is a nonnegative estimate in milliseconds. Nil is
	// unknown; a pointer to zero is a known zero estimate, not an execution SLA.
	EstimatedLatencyMs *int64 `json:"estimatedLatencyMs,omitempty"`
	// Hardware nil means unknown requirements. An explicit empty declaration
	// means no minimum. Local eligibility is not a resource reservation.
	Hardware *HardwareRequirements `json:"hardware,omitempty"`
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
