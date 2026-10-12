// Package checkpoint persists bounded, deterministic progress snapshots. A
// checkpoint describes work to reconsider, never a live process to restore.
package checkpoint

import (
	"context"
	"errors"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
)

const SchemaVersion = 1

var (
	ErrInvalid     = errors.New("checkpoint: invalid input")
	ErrNotFound    = errors.New("checkpoint: not found")
	ErrCorrupt     = errors.New("checkpoint: corrupt data")
	ErrSchema      = errors.New("checkpoint: unsupported schema")
	ErrIdentity    = errors.New("checkpoint: identity mismatch")
	ErrProjectHash = errors.New("checkpoint: project content changed")
	ErrIO          = errors.New("checkpoint: I/O failure")
	ErrUnsafePath  = errors.New("checkpoint: unsafe path")
	ErrLimit       = errors.New("checkpoint: size limit exceeded")
	ErrUnsupported = errors.New("checkpoint: platform unsupported")
	ErrArtifact    = errors.New("checkpoint: artifact lookup failed")
)

// Identity binds progress to an authoritative project revision and requested
// quality. ProjectHash must be computed by the caller from current project
// content, not copied from an unverified checkpoint. No project content is stored.
// Handles must be nonsecret ASCII letters, digits, underscores or hyphens (1..128).
type Identity struct {
	ProjectID   domain.ID       `json:"projectId"`
	ProjectHash artifact.Digest `json:"projectHash"`
	Quality     domain.Quality  `json:"quality"`
}

type State string

const (
	Pending     State = "pending"
	Complete    State = "complete"
	Invalidated State = "invalidated"
)

// Shot stores only deterministic work identity and completion evidence.
// ArtifactDigest is Entry.Digest (the whole published completion record), not
// its media digest. Complete requires a nonzero digest. Other states may retain
// an old digest, but that evidence can never make them ready during resume.
// InvalidationGeneration is the latest invalidation affecting this shot, not
// an execution attempt, timestamp, or resource lease generation.
type Shot struct {
	ShotID                 domain.ID       `json:"shotId"`
	Key                    artifact.Key    `json:"key"`
	State                  State           `json:"state"`
	ArtifactDigest         artifact.Digest `json:"artifactDigest"`
	InvalidationGeneration uint64          `json:"invalidationGeneration"`
}

// Checkpoint preserves the caller's deterministic shot order. It has no cursor
// that could skip holes: resume recomputes progress by checking every shot.
// Generation is the latest invalidation generation for this snapshot; unaffected
// shots may have earlier generations. At most 10,000 unique shots are accepted.
// The caller owns project/shot membership, dependency order, key computation and
// generation advancement. Save does not merge concurrent or stale snapshots.
type Checkpoint struct {
	SchemaVersion int      `json:"schemaVersion"`
	Identity      Identity `json:"identity"`
	Generation    uint64   `json:"generation"`
	Shots         []Shot   `json:"shots"`
}

// Clone detaches mutable storage; every shot consists only of value fields.
func (c Checkpoint) Clone() Checkpoint {
	if c.Shots != nil {
		c.Shots = append([]Shot{}, c.Shots...)
	}
	return c
}

func safeHandle(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validIdentity(id Identity) bool {
	return safeHandle(string(id.ProjectID)) && id.ProjectHash != (artifact.Digest{}) &&
		(id.Quality == domain.QualityDraft || id.Quality == domain.QualityFinal)
}

func validKey(key artifact.Key) bool {
	if len(key) != 64 {
		return false
	}
	for _, c := range key {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func validate(c Checkpoint) error {
	if c.SchemaVersion != SchemaVersion {
		return ErrSchema
	}
	if !validIdentity(c.Identity) || len(c.Shots) > 10_000 {
		return ErrInvalid
	}
	seen := make(map[domain.ID]bool, len(c.Shots))
	for _, shot := range c.Shots {
		if !safeHandle(string(shot.ShotID)) || seen[shot.ShotID] || !validKey(shot.Key) || shot.InvalidationGeneration > c.Generation {
			return ErrInvalid
		}
		seen[shot.ShotID] = true
		switch shot.State {
		case Complete:
			if shot.ArtifactDigest == (artifact.Digest{}) {
				return ErrInvalid
			}
		case Pending:
		case Invalidated:
			if shot.InvalidationGeneration == 0 {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}

// ResumeState is readiness now, independently of persisted completion claims.
type ResumeState string

const (
	ResumeReady   ResumeState = "ready"
	ResumePending ResumeState = "pending"
)

type Reason string

const (
	ReasonVerified       Reason = "verified"
	ReasonIncomplete     Reason = "incomplete"
	ReasonInvalidated    Reason = "invalidated"
	ReasonUnavailable    Reason = "artifact_unavailable"
	ReasonDigestMismatch Reason = "artifact_digest_mismatch"
)

type ResumedShot struct {
	ShotID domain.ID   `json:"shotId"`
	State  ResumeState `json:"state"`
	Reason Reason      `json:"reason"`
}

// ResumeResult is detached read-only advice, in checkpoint shot order. Ready is
// a count, not a cursor. It is not persisted or used to bypass future lookups.
type ResumeResult struct {
	Checkpoint Checkpoint
	Shots      []ResumedShot
	Ready      int
}

// Resume validates the checkpoint against expected current project identity,
// then verifies each claimed completion through artifact.Store.Lookup. Missing,
// partial, corrupt and wrong-identity artifacts are intentionally indistinguishable
// safe misses in that API, so all receive ReasonUnavailable and remain pending.
// No files are repaired and no work is executed. A caller must revalidate evidence
// when consuming it; this is not a transaction across both stores.
func (s *Store) Resume(ctx context.Context, expected Identity, artifacts *artifact.Store) (ResumeResult, error) {
	if artifacts == nil {
		return ResumeResult{}, ErrInvalid
	}
	c, err := s.Load(ctx, expected)
	if err != nil {
		return ResumeResult{}, err
	}
	result := ResumeResult{Checkpoint: c, Shots: make([]ResumedShot, 0, len(c.Shots))}
	for _, shot := range c.Shots {
		if err := ctx.Err(); err != nil {
			return ResumeResult{}, err
		}
		r := ResumedShot{ShotID: shot.ShotID, State: ResumePending, Reason: ReasonIncomplete}
		switch shot.State {
		case Invalidated:
			r.Reason = ReasonInvalidated
		case Complete:
			id := artifact.Identity{ProjectID: c.Identity.ProjectID, ShotID: shot.ShotID, Quality: c.Identity.Quality, Key: shot.Key}
			e, hit, err := artifacts.Lookup(ctx, id)
			if err != nil {
				if ctx.Err() != nil {
					return ResumeResult{}, ctx.Err()
				}
				return ResumeResult{}, ErrArtifact
			}
			r.Reason = ReasonUnavailable
			if hit {
				r.Reason = ReasonDigestMismatch
				if e.Identity == id && e.Digest == shot.ArtifactDigest {
					r.State, r.Reason = ResumeReady, ReasonVerified
					result.Ready++
				}
			}
		}
		result.Shots = append(result.Shots, r)
	}
	if err := ctx.Err(); err != nil {
		return ResumeResult{}, err
	}
	return result, nil
}
