// Package cloudprovider supplies cooperative, injected cloud job boundaries.
// It performs no networking itself. Callers own routing, spending authorization,
// scheduling and dependency lifecycles. No operation sleeps or starts a goroutine.
package cloudprovider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/manish96170/videofactory/domain"
)

var (
	ErrConfig      = errors.New("cloudprovider: invalid configuration")
	ErrRequest     = errors.New("cloudprovider: invalid request")
	ErrResult      = errors.New("cloudprovider: invalid response")
	ErrState       = errors.New("cloudprovider: invalid job state")
	ErrBusy        = errors.New("cloudprovider: job busy")
	ErrNotReady    = errors.New("cloudprovider: attempt not yet due")
	ErrExhausted   = errors.New("cloudprovider: attempt limit reached")
	ErrUnsupported = errors.New("cloudprovider: operation unsupported")
	ErrAuth        = errors.New("cloudprovider: credentials rejected")
	ErrPermanent   = errors.New("cloudprovider: permanent transport failure")
	ErrTransient   = errors.New("cloudprovider: transient transport failure")
	ErrRateLimited = errors.New("cloudprovider: transport rate limited")
	ErrDependency  = errors.New("cloudprovider: dependency failed")
	ErrIntegrity   = errors.New("cloudprovider: download integrity mismatch")
	ErrSink        = errors.New("cloudprovider: staging failed")
	ErrCleanup     = errors.New("cloudprovider: staging cleanup unconfirmed")
)

// Credential is an opaque runtime value: only the injected transport should call
// Value. It cannot be serialized or formatted as secret text. Sources and
// transports must not persist, log, or retain credentials after the call.
type Credential struct{ value any }

func NewCredential(value any) Credential        { return Credential{value: value} }
func (c Credential) Value() any                 { return c.value }
func (Credential) Format(s fmt.State, _ rune)   { _, _ = io.WriteString(s, "[credential]") }
func (Credential) MarshalJSON() ([]byte, error) { return nil, ErrAuth }

type CredentialSource interface {
	Acquire(context.Context) (Credential, error)
}

// SubmitRequest has an immutable, versioned payload and an idempotency key. A
// transport MUST deduplicate identical submissions by RequestID (including after
// timeout) and reject reuse of that key with different content. Handles throughout
// this API are public logical IDs, never URLs, bearer tokens or provider secrets.
type SubmitRequest struct {
	Version   int             `json:"version"`
	RequestID domain.ID       `json:"requestId"`
	Payload   json.RawMessage `json:"payload"`
}

type JobRef struct {
	ProviderID string    `json:"providerId"`
	RequestID  domain.ID `json:"requestId"`
	JobID      string    `json:"jobId"`
}

type Status string

const (
	New       Status = "new"
	Queued    Status = "queued"
	Running   Status = "running"
	Succeeded Status = "succeeded"
	Failed    Status = "failed"
	Canceled  Status = "canceled"
)

type Artifact struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Reply contains only protocol facts; arbitrary provider messages and metadata
// are intentionally absent. Failed is a terminal remote job status, not a retry.
// Cancel may return Queued/Running when cancellation is not yet acknowledged.
type Reply struct {
	Ref       JobRef        `json:"ref"`
	Status    Status        `json:"status"`
	PollAfter time.Duration `json:"pollAfter"`
	Artifact  *Artifact     `json:"artifact,omitempty"`
}

// Dependencies must cooperate with context, be safe across concurrent jobs, and
// not retain/mutate input or output buffers asynchronously. Download readers must
// honor the supplied context on Read and Close. Error-accompanying replies are
// discarded; any accompanying stream is closed. Transport is a trusted runtime
// seam, not a sandbox. This package includes no default HTTP implementation.
type Transport interface {
	Submit(context.Context, Credential, SubmitRequest) (Reply, error)
	Poll(context.Context, Credential, JobRef) (Reply, error)
	Download(context.Context, Credential, JobRef, Artifact) (io.ReadCloser, error)
}

// Cancellation is optional. Context cancellation only stops a local attempt; it
// never implies that the remote job was canceled. Poll confirms remote status.
type Canceler interface {
	Cancel(context.Context, Credential, JobRef) (Reply, error)
}

// TransportError communicates a closed failure classification and Retry-After.
// Kind must be one of ErrAuth/ErrPermanent/ErrRequest/ErrUnsupported/ErrTransient/
// ErrRateLimited. Unknown kinds fail permanently. No raw cause is exposed.
type TransportError struct {
	Kind       error
	RetryAfter time.Duration
}

func (e *TransportError) Error() string { return "cloudprovider: transport failure" }
func (e *TransportError) Unwrap() error {
	if e == nil {
		return nil
	}
	switch e.Kind {
	case ErrAuth, ErrPermanent, ErrRequest, ErrUnsupported, ErrTransient, ErrRateLimited:
		return e.Kind
	default:
		return ErrPermanent
	}
}

type WaitError struct{ NotBefore time.Time }

func (*WaitError) Error() string     { return ErrNotReady.Error() }
func (*WaitError) Is(err error) bool { return err == ErrNotReady }

// Policy has no defaults: every bound is explicit. MaxAttempts bounds submit,
// cancel and download attempts, and consecutive failed poll attempts. MaxPolls
// additionally bounds ALL poll calls, including successful pending responses.
// Retry-After above MaxDelay stops retries rather than retrying too early.
type Policy struct {
	MaxAttempts      int
	MaxPolls         int
	MinInterval      time.Duration
	RetryDelay       time.Duration
	MaxDelay         time.Duration
	AttemptTimeout   time.Duration
	CleanupTimeout   time.Duration
	MaxDownloadBytes int64
}

// Target contains verified identity and expected integrity facts, never a path.
// The sink alone maps this logical target to caller-controlled private staging.
type Target struct {
	RequestID domain.ID `json:"requestId"`
	ShotID    domain.ID `json:"shotId"`
	Artifact  Artifact  `json:"artifact"`
}

type Sink interface {
	Begin(context.Context, Target) (Staging, error)
}

// Staging writes must remain invisible until Commit, which atomically accepts
// only verified bytes. Abort must be idempotent and discard staging, including
// after a failed/late Commit. This is not cache publication or media validation.
// A nonnil stage returned with an error is still aborted. All methods cooperate
// with their context (Write uses the Begin context); no filesystem is owned here.
type Staging interface {
	io.Writer
	Commit(context.Context) error
	Abort(context.Context) error
}

type Provenance struct {
	ProviderID      string `json:"providerId"`
	AdapterRevision string `json:"adapterRevision"`
	Source          string `json:"source"`
}

type DownloadResult struct {
	Target     Target         `json:"target"`
	Quality    domain.Quality `json:"quality"`
	Provenance Provenance     `json:"provenance"`
}
