package resource

import "github.com/manish96170/videofactory/domain"

type Estimate struct {
	Request    Request                   `json:"request"`
	Key        HistoryKey                `json:"key"`
	Provenance domain.EstimateProvenance `json:"provenance"`
}

// EstimateRequest recomputes admission from exact frozen capabilities/profile,
// current trusted Config, and a coherent trusted HistorySnapshot. Never accepts a
// ResourcePlan as authority. percent is explicit (DefaultPlannerConfig uses 25).
// History only raises max(load,resident+workspace,ceil(legacyGB*GB)); margin is
// applied AFTER that max. No invented weights/precision formula.
func EstimateRequest(caps domain.ModelCapabilities, profileID string, workload domain.Workload, cfg Config, history HistorySnapshot, percent int64) (Estimate, error) {
	if err := cfg.Validate(); err != nil {
		return Estimate{}, err
	}
	if percent < 0 {
		return Estimate{}, ErrInvalidConfig
	}
	p, err := Profile(caps, profileID)
	if err != nil {
		return Estimate{}, err
	}
	if !caps.Local {
		return Estimate{}, ErrLocalIneligible
	}
	if err := SupportsWorkload(p, workload); err != nil {
		return Estimate{}, err
	}
	run, err := checkedAdd(p.ResidentRAMBytes, p.WorkspaceRAMBytes)
	if err != nil {
		return Estimate{}, err
	}
	declared := max(p.LoadRAMBytes, run)
	floor, err := rationalUnits(caps.MinMemoryGB, GB, true)
	if err != nil {
		return Estimate{}, err
	}
	key := HistoryKey{ProviderID: caps.ProviderID, ModelID: caps.ModelID, ModelRevision: caps.Requirements.Revision,
		ProfileID: p.ID, ProfileRevision: p.Revision, HardwareClass: cfg.HardwareClass, Workload: workload}
	prov := domain.EstimateProvenance{ProviderID: key.ProviderID, ModelID: key.ModelID, ModelRevision: key.ModelRevision,
		ProfileID: key.ProfileID, ProfileRevision: key.ProfileRevision, HardwareClass: key.HardwareClass, Workload: workload,
		DeclaredPeakRAMBytes: declared, LegacyFloorRAMBytes: floor, SafetyMarginPercent: percent, HistoryVersion: history.Version, HistoryHash: history.Hash}
	peak := max(declared, floor)
	if entry, ok := history.Lookup(key); ok {
		if err := entry.Measurement.Validate(); err != nil {
			return Estimate{}, err
		}
		prov.ObservedPeakRAMBytes = entry.Measurement.ObservedPeak()
		if prov.ObservedPeakRAMBytes != nil {
			peak = max(peak, *prov.ObservedPeakRAMBytes)
		}
	}
	peak, err = ApplySafetyMargin(peak, percent)
	if err != nil {
		return Estimate{}, err
	}
	req := Request{RAMBytes: peak, CPU: p.CPU, GPU: p.GPU, Backend: p.Backend, Heavy: p.Heavy, Exclusive: p.Exclusive}
	// Populate the advisory estimate even on a permanent budget/backend failure.
	req.Heavy = req.Heavy || req.GPU > 0 || req.RAMBytes >= cfg.HeavyRAMThreshold
	est := Estimate{Request: req, Key: key, Provenance: prov}
	_, err = ValidateRequest(cfg, req)
	return est, err
}
