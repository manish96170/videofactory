package changeset

import (
	"context"
	"errors"
	"time"

	"github.com/manish96170/videofactory/domain"
)

const (
	OpSetProjectName = "set-project-name" // target project; payload {name:string}
	OpSetText        = "set-text"         // target Text primitive; payload {shotId:string,text:string}
	OpSetColor       = "set-color"        // target Text/Background primitive; payload {shotId:string,color:string}
	OpSetFontSize    = "set-font-size"    // target Text primitive; payload {shotId:string,fontSize:number}
	MaxChanges       = 128
	MaxTextBytes     = 16 * 1024
	MaxProjectBytes  = 8 * 1024 * 1024
	MaxAuditRecords  = 256
)

var (
	ErrInvalid     = errors.New("changeset: invalid input")
	ErrUnsupported = errors.New("changeset: unsupported mutation")
	ErrStale       = errors.New("changeset: stale proposal")
	ErrState       = errors.New("changeset: proposal is not pending")
	ErrProvider    = errors.New("changeset: intent provider failed")
)

// Proposal is untrusted input even when ChangeSet.Status says approved/applied.
// Validate accepts only proposed status. A decoded proposal must be revalidated.
type Proposal struct {
	BaseHash  string           `json:"baseHash"`
	ChangeSet domain.ChangeSet `json:"changeSet"`
}

// Target uses the ReviewFeedback coordinate convention: shot-relative when a
// shot is selected, otherwise project-relative. Nil Timestamp means no position.
type Target struct {
	ShotID    domain.ID      `json:"shotId,omitempty"`
	ElementID domain.ID      `json:"elementId,omitempty"`
	Timestamp *time.Duration `json:"timestamp,omitempty"`
	Duration  *time.Duration `json:"duration,omitempty"`
}

// Request represents either chat (Text/Target), or Feedback, never both. ID and
// CreatedAt are supplied by the caller, so identical requests remain deterministic.
type Request struct {
	ID        domain.ID              `json:"id"`
	ProjectID domain.ID              `json:"projectId"`
	CreatedAt time.Time              `json:"createdAt"`
	Text      string                 `json:"text,omitempty"`
	Target    Target                 `json:"target"`
	Feedback  *domain.ReviewFeedback `json:"feedback,omitempty"`
}

type IntentRequest struct {
	Request  Request        `json:"request"`
	Project  domain.Project `json:"project"`
	BaseHash string         `json:"baseHash"`
}

// Intent cannot supply lifecycle status, project identity, or a base hash. The
// engine binds these from the authoritative input after the callback returns.
type Intent struct {
	Changes []domain.Change `json:"changes"`
}

type IntentProvider interface {
	Propose(context.Context, IntentRequest) (Intent, error)
}

type Mode string

const (
	Review    Mode = "review"
	Automatic Mode = "automatic"
)

type Config struct {
	Mode Mode
	// Timeout bounds the cooperative provider call; default 5 seconds.
	Timeout time.Duration
}

// Impact is advice only. ResourceEstimateKnown is always false in this tranche.
type Impact struct {
	ChangedShots          int  `json:"changedShots"`
	InvalidatedShots      int  `json:"invalidatedShots"`
	ResourceEstimateKnown bool `json:"resourceEstimateKnown"`
}

type Preview struct {
	Proposal
	ResultHash       string      `json:"resultHash"`
	ChangedRoots     []domain.ID `json:"changedRoots"`
	InvalidatedShots []domain.ID `json:"invalidatedShots"`
	Impact           Impact      `json:"impact"`
}

type Result struct {
	Project domain.Project `json:"project"`
	Preview Preview        `json:"preview"`
}

// Outcome always contains the validated handle; Applied is present only in
// automatic mode. Review mode requires an explicit Apply call with current state.
type Outcome struct {
	Validated *Validated
	Applied   *Result
}

// AuditRecord contains bounded core codes and validated nonsecret handles only.
// IDs supplied to this package must be nonsecret. Notes/payloads/provider errors
// are never included. Sequence is local to one handle, with no ambient clock.
type AuditRecord struct {
	Sequence    uint64    `json:"sequence"`
	ProjectID   domain.ID `json:"projectId"`
	ChangeSetID domain.ID `json:"changeSetId"`
	Code        string    `json:"code"`
}
