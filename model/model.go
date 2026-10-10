// Package model owns explicit local model lifecycles. Full peak reservations
// survive use-handle Release and context cancellation; only a verified stop and
// successful resource-lease Release refund them. There is no eviction, expiry,
// reference sharing, worker allocation, inference, or ambient model state.
package model

import (
	"context"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/resource"
)

// Key is a stable, exact, nonsecret source handle. Never use paths, credentials,
// prompts, or host identifiers in keys (or capability/workload labels).
type Key struct {
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
	Revision   string `json:"revision"`
}

func (k Key) Validate() error {
	for _, s := range []string{k.ProviderID, k.ModelID, k.Revision} {
		if !utf8.ValidString(s) || strings.TrimSpace(s) == "" {
			return ErrInvalidKey
		}
	}
	return nil
}

// LoadSpec is an owned snapshot of the EXACT admitted profile and workload,
// including backend/precision and model revision. The loader must not modify or
// asynchronously mutate/retain it after return. Copies never alias the manager.
type LoadSpec struct {
	Key      Key                     `json:"key"`
	Profile  domain.ExecutionProfile `json:"profile"`
	Workload domain.Workload         `json:"workload"`
}

func cloneSpec(s LoadSpec) LoadSpec { s.Profile = resource.CloneProfile(s.Profile); return s }
func cloneEstimate(e resource.Estimate) resource.Estimate {
	if p := e.Provenance.ObservedPeakRAMBytes; p != nil {
		v := *p
		e.Provenance.ObservedPeakRAMBytes = &v
	}
	return e
}

// Loader is a cooperative callback boundary, not a worker dependency. Identity
// must be stable, fast, concurrency-safe, and equal the registered Key. Load
// must not acquire another resource lease: the full reservation is already held.
// Unload's bool is proof that ALL resources started by Load (including partial
// loads/descendants) stopped. nil error alone is never proof. Even with an error,
// true is usable proof when both identity checks match. Callbacks must cooperate
// with context; no detached goroutine forcibly times out uncooperative callbacks.
// Caller owns loaders; do not drive their lifecycle outside this manager.
type Loader interface {
	Identity() Key
	Load(context.Context, LoadSpec) error
	Unload(context.Context) (stopped bool, err error)
}

// Budget supplies authoritative current configuration and atomic allocations.
// Injected methods must be concurrency-safe; Release is bounded bookkeeping and
// must support retry after error without losing its allocation token.
type Budget interface {
	resource.Manager
	Config() resource.Config
}

// History is trusted parent-owned evidence, never worker/LLM claims. Methods
// must be bounded, concurrency-safe, and return owned snapshots.
type History interface {
	Snapshot() resource.HistorySnapshot
	Record(resource.HistoryKey, resource.Measurement) error
}

type Config struct {
	LoadTimeout         time.Duration
	UnloadTimeout       time.Duration // also the separate failed-load cleanup bound
	SafetyMarginPercent int64
	Now                 func() time.Time // concurrency-safe parent clock; called outside locks
}

func DefaultConfig() Config {
	return Config{LoadTimeout: 30 * time.Second, UnloadTimeout: 5 * time.Second, SafetyMarginPercent: 25, Now: time.Now}
}

func (c Config) Validate() error {
	if c.LoadTimeout <= 0 || c.UnloadTimeout <= 0 || c.SafetyMarginPercent < 0 || c.Now == nil {
		return ErrInvalidConfig
	}
	return nil
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

func contextError(ctx context.Context) error {
	if ctx == nil {
		return failure(CodeInvalidRequest, ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return failure(CodeCanceled, err)
	}
	return nil
}

type State string

const (
	StateUnloaded    State = "unloaded"
	StateReserving   State = "reserving"
	StateLoading     State = "loading"
	StateInUse       State = "in_use"
	StateIdle        State = "loaded_idle"
	StateUnloading   State = "unloading"
	StateQuarantined State = "quarantined"
)

// Snapshot contains only owned serializable metadata, not loaders, contexts,
// resource/use tokens, raw error text, private paths, or worker I/O.
type Snapshot struct {
	Models []ModelSnapshot `json:"models"`
}
type ModelSnapshot struct {
	Key             Key                      `json:"key"`
	Capabilities    domain.ModelCapabilities `json:"capabilities"`
	State           State                    `json:"state"`
	Spec            *LoadSpec                `json:"spec,omitempty"`
	Estimate        *resource.Estimate       `json:"estimate,omitempty"`
	Reservation     *resource.Request        `json:"reservation,omitempty"`
	StoppedVerified bool                     `json:"stoppedVerified"`
	Observations    resource.Measurement     `json:"observations"`
	HistoryFailures uint64                   `json:"historyFailures"`
	LastCode        Code                     `json:"lastCode,omitempty"`
}
