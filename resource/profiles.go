package resource

import (
	"errors"
	"slices"

	"github.com/manish96170/videofactory/domain"
)

// CloneProfile/CloneCapabilities isolate declarations on BOTH ingress/egress.
// Callers must not mutate input concurrently with cloning. No adapter is called.
func CloneProfile(p domain.ExecutionProfile) domain.ExecutionProfile {
	p.Envelope.Precisions = slices.Clone(p.Envelope.Precisions)
	p.Envelope.Qualities = slices.Clone(p.Envelope.Qualities)
	p.Envelope.Offloads = slices.Clone(p.Envelope.Offloads)
	return p
}

func CloneCapabilities(c domain.ModelCapabilities) domain.ModelCapabilities {
	c.Tags = slices.Clone(c.Tags)
	if c.Requirements != nil {
		r := *c.Requirements
		r.ExecutionProfiles = make([]domain.ExecutionProfile, len(c.Requirements.ExecutionProfiles))
		for i, p := range c.Requirements.ExecutionProfiles {
			r.ExecutionProfiles[i] = CloneProfile(p)
		}
		c.Requirements = &r
	}
	return c
}

func validLabels(labels []string) bool {
	if len(labels) == 0 {
		return false
	}
	seen := make(map[string]bool, len(labels))
	for _, s := range labels {
		if blank(s) || seen[s] {
			return false
		}
		seen[s] = true
	}
	return true
}
func validRange(r domain.IntRange) bool { return r.Min >= 0 && r.Max >= r.Min }

func ValidateWorkload(w domain.Workload) error {
	if blank(w.Precision) || blank(w.Quality) || blank(w.Offload) || w.Width < 0 || w.Height < 0 || w.Frames < 0 || w.ChunkSize < 0 {
		return ErrInvalidRequest
	}
	return nil
}

// ValidateProfile checks structure even for declarations that explicitly mark
// bounds unknown/untrusted; SupportsWorkload separately fails closed on those.
func ValidateProfile(p domain.ExecutionProfile) error {
	e := p.Envelope
	if blank(p.ID) || blank(p.Revision) || p.LoadRAMBytes < 0 || p.ResidentRAMBytes < 0 || p.WorkspaceRAMBytes < 0 ||
		!validLabels(e.Precisions) || !validLabels(e.Qualities) || !validLabels(e.Offloads) ||
		!validRange(e.Width) || !validRange(e.Height) || !validRange(e.Frames) || !validRange(e.ChunkSize) {
		return ErrInvalidProfile
	}
	if _, err := checkedAdd(p.ResidentRAMBytes, p.WorkspaceRAMBytes); err != nil {
		return errors.Join(ErrInvalidProfile, err)
	}
	if _, err := quantize(p.CPU, true); err != nil {
		return errors.Join(ErrInvalidProfile, err)
	}
	if _, err := quantize(p.GPU, true); err != nil {
		return errors.Join(ErrInvalidProfile, err)
	}
	if !knownBackend(p.Backend) || (p.GPU > 0 && p.Backend == domain.BackendNone) || (p.GPU == 0 && p.Backend != domain.BackendNone) {
		return ErrInvalidProfile
	}
	if p.MemoryKnown && p.LoadRAMBytes == 0 && p.ResidentRAMBytes == 0 && p.WorkspaceRAMBytes == 0 {
		return ErrInvalidProfile
	}
	return nil
}

// ValidateCapabilities admits optional legacy/unknown declarations for registry
// storage, not local loading. Requirements version must be 1 when present.
func ValidateCapabilities(c domain.ModelCapabilities) error {
	if blank(c.ProviderID) || blank(c.ModelID) || !finiteNonnegative(c.MinMemoryGB) {
		return ErrInvalidProfile
	}
	if _, err := rationalUnits(c.MinMemoryGB, GB, true); err != nil {
		return errors.Join(ErrInvalidProfile, err)
	}
	for _, tag := range c.Tags {
		if blank(tag) {
			return ErrInvalidProfile
		}
	}
	if c.Requirements == nil {
		return nil
	}
	if c.Requirements.Version != 1 || blank(c.Requirements.Revision) || len(c.Requirements.ExecutionProfiles) == 0 {
		return ErrInvalidProfile
	}
	seen := make(map[string]bool)
	for _, p := range c.Requirements.ExecutionProfiles {
		if seen[p.ID] {
			return ErrInvalidProfile
		}
		seen[p.ID] = true
		if err := ValidateProfile(p); err != nil {
			return err
		}
		if c.RequiresGPU && p.GPU == 0 {
			return ErrInvalidProfile
		}
	}
	return nil
}

func within(n int, r domain.IntRange) bool { return n >= r.Min && n <= r.Max }

func SupportsWorkload(p domain.ExecutionProfile, w domain.Workload) error {
	if err := ValidateProfile(p); err != nil {
		return err
	}
	if err := ValidateWorkload(w); err != nil {
		return err
	}
	e := p.Envelope
	if !p.Trusted || !p.MemoryKnown || !slices.Contains(e.Precisions, w.Precision) || !slices.Contains(e.Qualities, w.Quality) || !slices.Contains(e.Offloads, w.Offload) ||
		!within(w.Width, e.Width) || !within(w.Height, e.Height) || !within(w.Frames, e.Frames) || !within(w.ChunkSize, e.ChunkSize) {
		return ErrLocalIneligible
	}
	return nil
}

// Profile selects an EXACT profile ID, never a silent alternate. The returned
// value is independently owned and fully validated against frozen capabilities.
func Profile(c domain.ModelCapabilities, id string) (domain.ExecutionProfile, error) {
	if err := ValidateCapabilities(c); err != nil {
		return domain.ExecutionProfile{}, err
	}
	if c.Requirements != nil {
		for _, p := range c.Requirements.ExecutionProfiles {
			if p.ID == id {
				return CloneProfile(p), nil
			}
		}
	}
	return domain.ExecutionProfile{}, ErrLocalIneligible
}
