package audio

import (
	"fmt"
	"math"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manish96170/videofactory/domain"
)

const (
	MaxWordsPerResult = 100000
	MaxCuesPerResult  = 10000
)

var supportedCodecs = map[string]struct{}{
	"aac": {}, "flac": {}, "mp3": {}, "opus": {}, "pcm_s16le": {}, "wav": {},
}

// ValidateAudioAsset accepts only normalized, serializable audio metadata.
func ValidateAudioAsset(asset domain.AudioAsset) error {
	if !validID(asset.ID) || !validID(asset.ShotID) || strings.TrimSpace(asset.URI) == "" ||
		!utf8.ValidString(asset.URI) || asset.Duration <= 0 || asset.SampleRateHz <= 0 || asset.Channels <= 0 ||
		!validAudioMetadata(asset.Metadata, 0, make(map[uintptr]struct{})) {
		return ErrInvalid
	}
	if _, ok := supportedCodecs[asset.Codec]; !ok {
		return ErrInvalid
	}
	return nil
}

func validateInterval(start, end, duration time.Duration) error {
	if start < 0 || end <= start || end > duration {
		return ErrInvalid
	}
	return nil
}

// ValidateWordTimings checks chronological, non-overlapping word timings bound
// to asset. The empty result is valid for silence, but blank words are never.
func ValidateWordTimings(asset domain.AudioAsset, words []domain.WordTiming) error {
	if err := ValidateAudioAsset(asset); err != nil || len(words) > MaxWordsPerResult {
		return ErrInvalid
	}
	var previous time.Duration
	for i, word := range words {
		if !validID(word.AudioAssetID) || word.AudioAssetID != asset.ID || strings.TrimSpace(word.Word) == "" || !utf8.ValidString(word.Word) || validateInterval(word.Start, word.End, asset.Duration) != nil || (i > 0 && word.Start < previous) {
			return ErrInvalid
		}
		previous = word.End
	}
	return nil
}

// ValidateSubtitleCues checks chronological, non-overlapping subtitle timing
// and ensures every cue is linked to the audio asset that bounds its interval.
func ValidateSubtitleCues(asset domain.AudioAsset, cues []domain.SubtitleCue) error {
	if err := ValidateAudioAsset(asset); err != nil || len(cues) > MaxCuesPerResult {
		return ErrInvalid
	}
	seen := make(map[domain.ID]struct{}, len(cues))
	var previous time.Duration
	for i, cue := range cues {
		if !validID(cue.ID) || !validID(cue.AudioAssetID) || cue.AudioAssetID != asset.ID || strings.TrimSpace(cue.Text) == "" || !utf8.ValidString(cue.Text) || validateInterval(cue.Start, cue.End, asset.Duration) != nil || (i > 0 && cue.Start < previous) {
			return ErrInvalid
		}
		if _, duplicate := seen[cue.ID]; duplicate {
			return ErrInvalid
		}
		seen[cue.ID] = struct{}{}
		previous = cue.End
	}
	return nil
}

// ValidateTemporalTarget verifies an existing shot-relative temporal mapping.
func ValidateTemporalTarget(target domain.TemporalTarget, shotDuration time.Duration) error {
	if !validID(target.ShotID) || (!target.ElementID.IsZero() && !validID(target.ElementID)) || shotDuration <= 0 || !utf8.ValidString(target.EventID) || validateInterval(target.Start, target.End, shotDuration) != nil ||
		(target.ElementID.IsZero() == (strings.TrimSpace(target.EventID) == "")) {
		return ErrInvalid
	}
	return nil
}

func validateVoiceRequest(req VoiceRequest) error {
	if !validID(req.ID) || !validID(req.ShotID) || strings.TrimSpace(req.Script) == "" || strings.TrimSpace(req.Voice) == "" || !utf8.ValidString(req.Script) || !utf8.ValidString(req.Voice) {
		return ErrInvalid
	}
	return nil
}

func validAudioMetadata(metadata map[string]any, depth int, active map[uintptr]struct{}) bool {
	if metadata == nil {
		return true
	}
	if depth > domain.MaxAudioMetadataDepth || !enterMetadataContainer(metadata, active) {
		return false
	}
	defer leaveMetadataContainer(metadata, active)
	for key, value := range metadata {
		if !utf8.ValidString(key) || !validAudioMetadataValue(value, depth+1, active) {
			return false
		}
	}
	return true
}

func validAudioMetadataValue(value any, depth int, active map[uintptr]struct{}) bool {
	switch value := value.(type) {
	case nil, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	case string:
		return utf8.ValidString(value)
	case float32:
		return !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
	case float64:
		return !math.IsNaN(value) && !math.IsInf(value, 0)
	case map[string]any:
		return validAudioMetadata(value, depth, active)
	case []any:
		if depth > domain.MaxAudioMetadataDepth || !enterMetadataContainer(value, active) {
			return false
		}
		defer leaveMetadataContainer(value, active)
		for _, child := range value {
			if !validAudioMetadataValue(child, depth+1, active) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validID(id domain.ID) bool { return !id.IsZero() && utf8.ValidString(string(id)) }

func enterMetadataContainer(value any, active map[uintptr]struct{}) bool {
	pointer := reflect.ValueOf(value).Pointer()
	if pointer == 0 {
		return true
	}
	if _, exists := active[pointer]; exists {
		return false
	}
	active[pointer] = struct{}{}
	return true
}

func leaveMetadataContainer(value any, active map[uintptr]struct{}) {
	if pointer := reflect.ValueOf(value).Pointer(); pointer != 0 {
		delete(active, pointer)
	}
}

// ValidateVoiceResult validates request/response linkage and all timing facts.
func ValidateVoiceResult(req VoiceRequest, result VoiceResult) error {
	if validateVoiceRequest(req) != nil || result.Asset.ShotID != req.ShotID || ValidateWordTimings(result.Asset, result.Words) != nil || ValidateSubtitleCues(result.Asset, result.Cues) != nil {
		return ErrInvalid
	}
	return nil
}

// ValidateTranscriptionResult validates ASR output against its requested asset.
func ValidateTranscriptionResult(req TranscriptionRequest, result TranscriptionResult) error {
	if !validID(req.ID) || ValidateWordTimings(req.Asset, result.Words) != nil || ValidateSubtitleCues(req.Asset, result.Cues) != nil {
		return ErrInvalid
	}
	return nil
}

// SyncAdvice is deterministic advice only; no media is changed by validation.
type SyncAdvice string

const (
	SyncAligned    SyncAdvice = "aligned"
	SyncAudioShort SyncAdvice = "audio_short"
	SyncAudioLong  SyncAdvice = "audio_long"
)

type SyncResult struct {
	Advice SyncAdvice    `json:"advice"`
	Delta  time.Duration `json:"delta"`
}

// ValidateSync compares audio with an explicit expected video duration.
func ValidateSync(asset domain.AudioAsset, expectedVideoDuration, tolerance time.Duration) (SyncResult, error) {
	if ValidateAudioAsset(asset) != nil || expectedVideoDuration <= 0 || tolerance < 0 {
		return SyncResult{}, ErrInvalid
	}
	delta := asset.Duration - expectedVideoDuration
	if delta > tolerance {
		return SyncResult{Advice: SyncAudioLong, Delta: delta}, fmt.Errorf("%w: %s", ErrSyncMismatch, SyncAudioLong)
	}
	if delta < -tolerance {
		return SyncResult{Advice: SyncAudioShort, Delta: delta}, fmt.Errorf("%w: %s", ErrSyncMismatch, SyncAudioShort)
	}
	return SyncResult{Advice: SyncAligned, Delta: delta}, nil
}
