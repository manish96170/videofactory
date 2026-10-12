// Package audio defines provider-neutral contracts and deterministic codecs for
// audio timing. It never invokes a speech framework, process, or network.
package audio

import (
	"context"
	"errors"

	"github.com/manish96170/videofactory/domain"
)

var (
	ErrInvalid      = errors.New("audio: invalid input")
	ErrUnsupported  = errors.New("audio: unsupported")
	ErrDependency   = errors.New("audio: dependency failed")
	ErrSyncMismatch = errors.New("audio: duration does not match expected video duration")
)

// VoiceRequest is a provider-neutral request for speech synthesis. Script and
// Voice are data, never an executable command or provider-specific argv.
type VoiceRequest struct {
	ID     domain.ID `json:"id"`
	ShotID domain.ID `json:"shotId"`
	Script string    `json:"script"`
	Voice  string    `json:"voice"`
}

// VoiceResult contains only serializable audio and timing facts.
type VoiceResult struct {
	Asset domain.AudioAsset    `json:"asset"`
	Words []domain.WordTiming  `json:"words"`
	Cues  []domain.SubtitleCue `json:"cues,omitempty"`
}

// Clone returns an independent copy suitable for crossing a provider boundary.
func (r VoiceResult) Clone() VoiceResult {
	r.Asset = r.Asset.Clone()
	r.Words = append([]domain.WordTiming(nil), r.Words...)
	r.Cues = append([]domain.SubtitleCue(nil), r.Cues...)
	return r
}

// VoiceProvider synthesizes speech without coupling callers to a vendor.
type VoiceProvider interface {
	ID() string
	Synthesize(context.Context, VoiceRequest) (VoiceResult, error)
}

// TranscriptionRequest asks an ASR implementation to transcribe an existing
// validated audio asset.
type TranscriptionRequest struct {
	ID    domain.ID         `json:"id"`
	Asset domain.AudioAsset `json:"asset"`
}

func (r TranscriptionRequest) Clone() TranscriptionRequest {
	r.Asset = r.Asset.Clone()
	return r
}

// TranscriptionResult is a provider-neutral ASR result.
type TranscriptionResult struct {
	Words []domain.WordTiming  `json:"words"`
	Cues  []domain.SubtitleCue `json:"cues,omitempty"`
}

func (r TranscriptionResult) Clone() TranscriptionResult {
	r.Words = append([]domain.WordTiming(nil), r.Words...)
	r.Cues = append([]domain.SubtitleCue(nil), r.Cues...)
	return r
}

// ASRProvider transcribes existing audio. Implementations must honor ctx.
type ASRProvider interface {
	ID() string
	Transcribe(context.Context, TranscriptionRequest) (TranscriptionResult, error)
}
