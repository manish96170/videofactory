// Package localprovider provides immutable local adapter declarations and
// cooperative, injected execution boundaries. It starts no processes and owns no
// model/resource leases. The caller must arrange admission, load, exclusive use,
// cleanup and unload through the existing model/resource managers. Discovery is
// declaration metadata, not health, hardware detection or permission to execute.
package localprovider

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/model"
	"github.com/manish96170/videofactory/provider"
	"github.com/manish96170/videofactory/resource"
	"github.com/manish96170/videofactory/worker"
)

var (
	ErrConfig      = errors.New("localprovider: invalid configuration")
	ErrRequest     = errors.New("localprovider: invalid request")
	ErrUnsupported = errors.New("localprovider: unsupported operation")
	ErrResult      = errors.New("localprovider: invalid result")
	ErrDependency  = errors.New("localprovider: dependency failed")
	ErrCleanup     = errors.New("localprovider: stop not proven")
)

const (
	GenerationTag = "generation"
	DecisionTag   = "semantic-decision"
	ASRTag        = "asr"
)

// Config is trusted deployment configuration. IDs/revisions/tags must be public,
// nonsecret handles. A descriptor selects exactly one revisioned model profile.
type Config struct {
	Model           domain.ModelCapabilities
	ProfileID       string
	AdapterRevision string
}

type Descriptor struct {
	caps     domain.ProviderCapabilities
	model    domain.ModelCapabilities
	profile  domain.ExecutionProfile
	revision string
}

// Private embedding promotes the read-only API without exposing a replaceable
// descriptor field on concrete adapters.
type descriptor = Descriptor

func NewDescriptor(c Config) (*Descriptor, error) {
	p, err := resource.Profile(c.Model, c.ProfileID)
	if err != nil || !c.Model.Local || !p.Trusted || !p.MemoryKnown || !handle(c.AdapterRevision) {
		return nil, ErrConfig
	}
	for _, s := range []string{c.Model.ProviderID, c.Model.ModelID, c.Model.Requirements.Revision, p.ID, p.Revision} {
		if !handle(s) {
			return nil, ErrConfig
		}
	}
	if len(c.Model.Tags) == 0 {
		return nil, ErrConfig
	}
	seen := map[string]bool{}
	for _, tag := range c.Model.Tags {
		if !handle(tag) || seen[tag] {
			return nil, ErrConfig
		}
		seen[tag] = true
	}
	peak := max(p.LoadRAMBytes, p.ResidentRAMBytes+p.WorkspaceRAMBytes)
	caps := domain.ProviderCapabilities{
		ProviderID: c.Model.ProviderID, Tags: append([]string(nil), c.Model.Tags...), Local: true,
		Hardware: &domain.HardwareRequirements{MinMemoryGB: math.Max(c.Model.MinMemoryGB, float64(peak)/float64(resource.GB)), RequiresGPU: p.GPU > 0},
	}
	return &Descriptor{caps: caps, model: resource.CloneCapabilities(c.Model), profile: p, revision: c.AdapterRevision}, nil
}

func (d *Descriptor) ID() string { return d.caps.ProviderID }

func (d *Descriptor) Capabilities(ctx context.Context) (domain.ProviderCapabilities, error) {
	if err := checkContext(ctx); err != nil {
		return domain.ProviderCapabilities{}, err
	}
	c := d.caps
	c.Tags = append([]string(nil), c.Tags...)
	h := *c.Hardware
	c.Hardware = &h
	return c, nil
}

func (d *Descriptor) ModelCapabilities() domain.ModelCapabilities {
	return resource.CloneCapabilities(d.model)
}

// LoadSpec is an advisory handoff to the model manager, never a loaded model or
// reservation. Metal (including unified-memory bounds) is declared by Profile;
// it is neither detected nor guaranteed to work on a particular Mac.
func (d *Descriptor) LoadSpec(w domain.Workload) (model.LoadSpec, error) {
	if resource.SupportsWorkload(d.profile, w) != nil {
		return model.LoadSpec{}, ErrUnsupported
	}
	return model.LoadSpec{Key: model.Key{ProviderID: d.ID(), ModelID: d.model.ModelID, Revision: d.model.Requirements.Revision}, Profile: resource.CloneProfile(d.profile), Workload: w}, nil
}

// Provenance contains configured identity only. It makes no claim about model
// determinism, measured compatibility, generated media validation or health.
type Provenance struct {
	ProviderID      string                    `json:"providerId"`
	AdapterRevision string                    `json:"adapterRevision"`
	ModelID         string                    `json:"modelId"`
	ModelRevision   string                    `json:"modelRevision"`
	ProfileID       string                    `json:"profileId"`
	ProfileRevision string                    `json:"profileRevision"`
	Backend         domain.AcceleratorBackend `json:"backend"`
	Source          string                    `json:"source"`
}

func (d *Descriptor) Provenance() Provenance {
	return Provenance{d.ID(), d.revision, d.model.ModelID, d.model.Requirements.Revision, d.profile.ID, d.profile.Revision, d.profile.Backend, "configured"}
}

func handle(s string) bool {
	if len(s) == 0 || len(s) > worker.MaxHeaderBytes {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r)) {
			return false
		}
	}
	return true
}

func nilLike(v any) bool {
	if v == nil {
		return true
	}
	r := reflect.ValueOf(v)
	switch r.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return r.IsNil()
	}
	return false
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return ErrRequest
	}
	return ctx.Err()
}

// Only fixed boundary sentinels cross this error boundary. Arbitrary dependency
// text (including paths, prompts and environment) is deliberately discarded.
func dependencyError(err error) error {
	for _, known := range []error{worker.ErrCleanup, worker.ErrProtocol, worker.ErrInvalidConfig, worker.ErrInvalidRequest, worker.ErrInputLimit, worker.ErrOutputLimit, worker.ErrLimitExceeded, worker.ErrMonitor, worker.ErrUnsupported, worker.ErrStart, worker.ErrWorker, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return errors.Join(ErrDependency, known)
		}
	}
	return ErrDependency
}

var _ provider.Provider = (*Descriptor)(nil)
