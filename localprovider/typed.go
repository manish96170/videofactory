package localprovider

import (
	"context"
	"errors"
	"math"

	"github.com/manish96170/videofactory/audio"
	"github.com/manish96170/videofactory/decision"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/worker"
)

// DecisionWorker is a typed injected boundary, not a new worker wire operation.
// It is already bound to trusted runtime/model configuration by the caller.
// Implementations must cooperate with cancellation, be safe for concurrent use,
// and neither retain nor asynchronously mutate input/results after return.
type DecisionWorker interface {
	Decide(context.Context, domain.DecisionRequest) (domain.DecisionResult, error)
}

type Decision struct {
	*descriptor
	worker DecisionWorker
	kinds  map[string]bool
}

func NewDecision(d *Descriptor, kinds []string, w DecisionWorker) (*Decision, error) {
	if d == nil || nilLike(w) || !d.caps.Supports(DecisionTag) || len(kinds) == 0 {
		return nil, ErrConfig
	}
	set := make(map[string]bool, len(kinds))
	for _, kind := range kinds {
		if !handle(kind) || set[kind] {
			return nil, ErrConfig
		}
		set[kind] = true
	}
	return &Decision{d, w, set}, nil
}

func (d *Decision) Decide(ctx context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
	if err := checkContext(ctx); err != nil {
		return domain.DecisionResult{}, err
	}
	if !handle(string(req.ID)) || !handle(req.Kind) || (req.ProjectID != "" && !handle(string(req.ProjectID))) || (req.ShotID != "" && !handle(string(req.ShotID))) {
		return domain.DecisionResult{}, errors.Join(ErrRequest, decision.ErrInvalidRequest)
	}
	if !d.kinds[req.Kind] {
		return domain.DecisionResult{}, errors.Join(ErrUnsupported, decision.ErrUnsupportedKind)
	}
	input, err := copyInput(req.Input)
	if err != nil {
		return domain.DecisionResult{}, errors.Join(err, decision.ErrInvalidRequest)
	}
	req.Input = input
	if err := ctx.Err(); err != nil {
		return domain.DecisionResult{}, err
	}
	result, err := d.worker.Decide(ctx, req)
	if ctx.Err() != nil {
		return domain.DecisionResult{}, ctx.Err()
	}
	if err != nil {
		// Permanent classifications override any wrapped transient marker.
		for _, known := range []error{decision.ErrInvalidRequest, decision.ErrInvalidProvider, decision.ErrIdentityMismatch, decision.ErrMalformedResult, decision.ErrUnsupportedKind, worker.ErrCleanup, worker.ErrProtocol, worker.ErrInvalidConfig, worker.ErrInvalidRequest, worker.ErrInputLimit, worker.ErrOutputLimit, worker.ErrLimitExceeded, worker.ErrMonitor, worker.ErrUnsupported, worker.ErrStart} {
			if errors.Is(err, known) {
				return domain.DecisionResult{}, errors.Join(ErrDependency, known)
			}
		}
		if errors.Is(err, decision.ErrTransient) {
			return domain.DecisionResult{}, errors.Join(ErrDependency, decision.ErrTransient)
		}
		return domain.DecisionResult{}, dependencyError(err)
	}
	if math.IsNaN(result.Score) || math.IsInf(result.Score, 0) || result.Score < 0 || result.Score > 1 || (result.Decision != domain.DecisionYes && result.Decision != domain.DecisionNo && result.Decision != domain.DecisionUncertain) || (result.Provider != "" && result.Provider != d.ID()) || (result.ReasonCode != "" && !handle(result.ReasonCode)) {
		return domain.DecisionResult{}, errors.Join(ErrResult, decision.ErrMalformedResult)
	}
	result.Provider = d.ID()
	return result, nil // Threshold classification remains decision.Engine's job.
}

// ASR uses the same cooperative ownership contract as DecisionWorker. Its
// injected worker owns trusted runtime binding; asset URIs are opaque data here.
type ASR struct {
	*descriptor
	worker audio.LocalASRWorker
}

func NewASR(d *Descriptor, w audio.LocalASRWorker) (*ASR, error) {
	if d == nil || nilLike(w) || !d.caps.Supports(ASRTag) {
		return nil, ErrConfig
	}
	return &ASR{d, w}, nil
}

func (a *ASR) Transcribe(ctx context.Context, req audio.TranscriptionRequest) (audio.TranscriptionResult, error) {
	if err := checkContext(ctx); err != nil {
		return audio.TranscriptionResult{}, err
	}
	if !handle(string(req.ID)) || audio.ValidateAudioAsset(req.Asset) != nil {
		return audio.TranscriptionResult{}, errors.Join(ErrRequest, audio.ErrInvalid)
	}
	req = req.Clone()
	if err := ctx.Err(); err != nil {
		return audio.TranscriptionResult{}, err
	}
	result, err := a.worker.Transcribe(ctx, req.Clone())
	if ctx.Err() != nil {
		return audio.TranscriptionResult{}, ctx.Err()
	}
	if err != nil {
		return audio.TranscriptionResult{}, errors.Join(dependencyError(err), audio.ErrDependency)
	}
	if audio.ValidateTranscriptionResult(req, result) != nil {
		return audio.TranscriptionResult{}, errors.Join(ErrResult, audio.ErrDependency)
	}
	result = result.Clone()
	if err := ctx.Err(); err != nil {
		return audio.TranscriptionResult{}, err
	}
	return result, nil
}

var _ decision.Provider = (*Decision)(nil)
var _ audio.ASRProvider = (*ASR)(nil)
