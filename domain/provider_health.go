package domain

// ProviderHealth is explicit registry metadata, not inferred from discovery or
// execution. Only HEALTHY is routable; registration starts at UNKNOWN.
type ProviderHealth string

const (
	ProviderHealthUnknown   ProviderHealth = "UNKNOWN"
	ProviderHealthHealthy   ProviderHealth = "HEALTHY"
	ProviderHealthUnhealthy ProviderHealth = "UNHEALTHY"
	ProviderHealthDisabled  ProviderHealth = "DISABLED"
)
