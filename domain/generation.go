package domain

// Quality distinguishes cheap draft generation from final-quality output
// (PHASES.md section 4.5, "preview first").
type Quality string

const (
	QualityDraft Quality = "draft"
	QualityFinal Quality = "final"
)

// GenerationRequest asks a provider to produce media for a shot/primitive.
//
// ProviderID is expected to be populated from the Provider Router's
// routing decision (Phase 07), not chosen ad hoc by arbitrary callers;
// this type does not itself perform routing/eligibility checks. Params
// holds JSON-compatible values only; see DECISIONS.md ADR-008 for the
// accepted precision/typing tradeoffs of map[string]any extension
// fields in Phase 00.
type GenerationRequest struct {
	ID         ID             `json:"id"`
	ShotID     ID             `json:"shotId"`
	ProviderID string         `json:"providerId"`
	Quality    Quality        `json:"quality"`
	Params     map[string]any `json:"params,omitempty"`
}

// GenerationResult records what a generation attempt produced, including
// the metadata needed for caching/incremental rebuild (section 4.6) and
// reproducibility (section 10 / Phase 10 cache sidecar metadata).
type GenerationResult struct {
	ID         ID     `json:"id"`
	RequestID  ID     `json:"requestId"`
	AssetID    ID     `json:"assetId,omitempty"`
	Seed       string `json:"seed,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
	// PeakMemoryMB is a measured peak in MB. Nil means no measurement is
	// available; a non-nil zero represents an explicitly measured zero.
	PeakMemoryMB *float64       `json:"peakMemoryMb,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}
