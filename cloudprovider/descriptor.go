package cloudprovider

import (
	"context"
	"math"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/provider"
)

// Config is trusted declaration metadata, not spending authorization or health.
// A known exact cost unit and latency estimate are mandatory (zero is allowed).
type Config struct {
	Capabilities    domain.ProviderCapabilities
	AdapterRevision string
	Policy          Policy
}

type Descriptor struct {
	caps     domain.ProviderCapabilities
	revision string
	policy   Policy
}

func NewDescriptor(c Config) (*Descriptor, error) {
	v, p := c.Capabilities, c.Policy
	if !handle(v.ProviderID) || !handle(c.AdapterRevision) || v.Local || v.Hardware != nil ||
		len(v.Tags) == 0 || len(v.Tags) > 64 || !v.Supports("generation") ||
		!label(v.CostUnit) || math.IsNaN(v.CostPerUnit) || math.IsInf(v.CostPerUnit, 0) || v.CostPerUnit < 0 ||
		v.EstimatedLatencyMs == nil || *v.EstimatedLatencyMs < 0 ||
		p.MaxAttempts < 1 || p.MaxAttempts > 8 || p.MaxPolls < 1 || p.MaxPolls > 10000 ||
		p.MinInterval <= 0 || p.RetryDelay <= 0 || p.MaxDelay < p.MinInterval || p.MaxDelay < p.RetryDelay || p.MaxDelay > 24*time.Hour ||
		p.AttemptTimeout <= 0 || p.AttemptTimeout > time.Hour || p.CleanupTimeout <= 0 || p.CleanupTimeout > time.Minute ||
		p.MaxDownloadBytes <= 0 || p.MaxDownloadBytes > 1<<40 {
		return nil, ErrConfig
	}
	seen := map[string]bool{}
	for _, tag := range v.Tags {
		if !handle(tag) || seen[tag] {
			return nil, ErrConfig
		}
		seen[tag] = true
	}
	return &Descriptor{cloneCaps(v), c.AdapterRevision, p}, nil
}

func (d *Descriptor) ID() string { return d.caps.ProviderID }
func (d *Descriptor) Capabilities(ctx context.Context) (domain.ProviderCapabilities, error) {
	if err := checkContext(ctx); err != nil {
		return domain.ProviderCapabilities{}, err
	}
	return cloneCaps(d.caps), nil
}
func (d *Descriptor) Provenance() Provenance { return Provenance{d.ID(), d.revision, "configured"} }
func cloneCaps(c domain.ProviderCapabilities) domain.ProviderCapabilities {
	c.Tags = append([]string(nil), c.Tags...)
	v := *c.EstimatedLatencyMs
	c.EstimatedLatencyMs = &v
	return c
}

type descriptor = Descriptor

// Adapter is concurrency-safe provided its injected dependencies are. The rate
// gate is shared across all its jobs. Do not copy an Adapter after use.
type Adapter struct {
	*descriptor
	source    CredentialSource
	transport Transport
	mu        sync.Mutex
	last      time.Time
	next      time.Time
}

func NewAdapter(d *Descriptor, source CredentialSource, transport Transport) (*Adapter, error) {
	if d == nil || !handle(d.ID()) || nilLike(source) || nilLike(transport) {
		return nil, ErrConfig
	}
	return &Adapter{descriptor: d, source: source, transport: transport}, nil
}

// now is trusted caller-supplied scheduling time. It must advance monotonically
// across this adapter. No wall-clock reads or waiting are hidden in the gate.
func (a *Adapter) reserve(now time.Time) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.IsZero() || now.Before(a.last) {
		return ErrRequest
	}
	if now.Before(a.next) {
		return &WaitError{NotBefore: a.next}
	}
	a.last, a.next = now, now.Add(a.policy.MinInterval)
	return nil
}

func (a *Adapter) deferUntil(t time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if t.After(a.next) {
		a.next = t
	}
}

func handle(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("-_.:", r)) {
			return false
		}
	}
	return true
}

func label(s string) bool {
	if len(s) == 0 || len(s) > 128 || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r < 33 || r > 126 {
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

var _ provider.Provider = (*Descriptor)(nil)
var _ provider.Provider = (*Adapter)(nil)
