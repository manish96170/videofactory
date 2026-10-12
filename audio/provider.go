package audio

import (
	"context"
	"reflect"
	"strings"
)

// FixtureVoiceProvider is deterministic test infrastructure. It returns a
// copied predeclared response keyed by script and invokes neither a speech
// engine nor a process/network service.
type FixtureVoiceProvider struct {
	id        string
	responses map[string]VoiceResult
}

func NewFixtureVoiceProvider(id string, responses map[string]VoiceResult) (*FixtureVoiceProvider, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalid
	}
	copy := make(map[string]VoiceResult, len(responses))
	for script, result := range responses {
		if strings.TrimSpace(script) == "" || ValidateWordTimings(result.Asset, result.Words) != nil || ValidateSubtitleCues(result.Asset, result.Cues) != nil {
			return nil, ErrInvalid
		}
		copy[script] = result.Clone()
	}
	return &FixtureVoiceProvider{id: id, responses: copy}, nil
}

func (p *FixtureVoiceProvider) ID() string { return p.id }

func (p *FixtureVoiceProvider) Synthesize(ctx context.Context, req VoiceRequest) (VoiceResult, error) {
	if ctx == nil || ctx.Err() != nil {
		if ctx == nil {
			return VoiceResult{}, ErrInvalid
		}
		return VoiceResult{}, ctx.Err()
	}
	if validateVoiceRequest(req) != nil {
		return VoiceResult{}, ErrInvalid
	}
	result, ok := p.responses[req.Script]
	if !ok {
		return VoiceResult{}, ErrUnsupported
	}
	result = result.Clone()
	if err := ctx.Err(); err != nil {
		return VoiceResult{}, err
	}
	if ValidateVoiceResult(req, result) != nil {
		return VoiceResult{}, ErrDependency
	}
	return result.Clone(), nil
}

// LocalASRWorker is the non-executing boundary to a trusted local worker. Its
// implementation, not an arbitrary caller request, owns executable, argv,
// working directory, and environment configuration.
type LocalASRWorker interface {
	Transcribe(context.Context, TranscriptionRequest) (TranscriptionResult, error)
}

// LocalASRProvider delegates to an injected worker boundary. It starts no
// executable and does not retain caller-owned asset metadata or result slices.
type LocalASRProvider struct {
	id     string
	worker LocalASRWorker
}

func NewLocalASRProvider(id string, worker LocalASRWorker) (*LocalASRProvider, error) {
	if strings.TrimSpace(id) == "" || nilWorker(worker) {
		return nil, ErrInvalid
	}
	return &LocalASRProvider{id: id, worker: worker}, nil
}

func nilWorker(worker LocalASRWorker) bool {
	if worker == nil {
		return true
	}
	v := reflect.ValueOf(worker)
	return (v.Kind() == reflect.Chan || v.Kind() == reflect.Func || v.Kind() == reflect.Interface || v.Kind() == reflect.Map || v.Kind() == reflect.Ptr || v.Kind() == reflect.Slice) && v.IsNil()
}

func (p *LocalASRProvider) ID() string { return p.id }

func (p *LocalASRProvider) Transcribe(ctx context.Context, req TranscriptionRequest) (TranscriptionResult, error) {
	if ctx == nil || ctx.Err() != nil {
		if ctx == nil {
			return TranscriptionResult{}, ErrInvalid
		}
		return TranscriptionResult{}, ctx.Err()
	}
	if !validID(req.ID) || ValidateAudioAsset(req.Asset) != nil {
		return TranscriptionResult{}, ErrInvalid
	}
	result, err := p.worker.Transcribe(ctx, req.Clone())
	if ctx.Err() != nil {
		return TranscriptionResult{}, ctx.Err()
	}
	if err != nil {
		return TranscriptionResult{}, ErrDependency
	}
	if ValidateTranscriptionResult(req, result) != nil {
		return TranscriptionResult{}, ErrDependency
	}
	result = result.Clone()
	if err := ctx.Err(); err != nil {
		return TranscriptionResult{}, err
	}
	return result, nil
}

// AppleVoiceProvider is an explicit platform integration seam. Phase 11 does
// not invoke AVFoundation, say, or any other native Apple speech service.
type AppleVoiceProvider struct{ id string }

func NewAppleVoiceProvider(id string) (*AppleVoiceProvider, error) {
	if strings.TrimSpace(id) == "" {
		return nil, ErrInvalid
	}
	return &AppleVoiceProvider{id: id}, nil
}

func (p *AppleVoiceProvider) ID() string { return p.id }

func (p *AppleVoiceProvider) Synthesize(ctx context.Context, req VoiceRequest) (VoiceResult, error) {
	if ctx == nil {
		return VoiceResult{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return VoiceResult{}, err
	}
	return VoiceResult{}, ErrUnsupported
}

var _ VoiceProvider = (*FixtureVoiceProvider)(nil)
var _ VoiceProvider = (*AppleVoiceProvider)(nil)
var _ ASRProvider = (*LocalASRProvider)(nil)
