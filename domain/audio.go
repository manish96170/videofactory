package domain

import (
	"math"
	"reflect"
	"time"
)

// MaxAudioMetadataDepth bounds nested metadata containers. Audio metadata is
// intentionally a small JSON tree, not an arbitrary Go object graph.
const MaxAudioMetadataDepth = 32

// AudioAsset is serializable metadata for audio placed on a shot timeline.
// All timing owned by an AudioAsset is relative to the start of ShotID and
// uses the end-exclusive interval convention already used by VisualElement.
type AudioAsset struct {
	ID           ID             `json:"id"`
	ShotID       ID             `json:"shotId"`
	URI          string         `json:"uri"`
	Codec        string         `json:"codec"`
	Duration     time.Duration  `json:"duration"`
	SampleRateHz int            `json:"sampleRateHz"`
	Channels     int            `json:"channels"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

// WordTiming identifies one spoken word in an audio asset. Start and End are
// relative to that audio asset's shot and form [Start, End).
type WordTiming struct {
	AudioAssetID ID            `json:"audioAssetId"`
	Word         string        `json:"word"`
	Start        time.Duration `json:"start"`
	End          time.Duration `json:"end"`
}

// SubtitleCue is a timed textual representation of one portion of an audio
// asset. Start and End are relative to the asset's shot and form [Start, End).
type SubtitleCue struct {
	ID           ID            `json:"id"`
	AudioAssetID ID            `json:"audioAssetId"`
	Text         string        `json:"text"`
	Start        time.Duration `json:"start"`
	End          time.Duration `json:"end"`
}

// TemporalTarget maps a shot-relative interval to one renderer-known element
// or a named temporal event. It deliberately uses the existing ShotID and
// shot-relative timestamp convention rather than introducing another clock.
type TemporalTarget struct {
	ShotID    ID            `json:"shotId"`
	ElementID ID            `json:"elementId,omitempty"`
	EventID   string        `json:"eventId,omitempty"`
	Start     time.Duration `json:"start"`
	End       time.Duration `json:"end"`
}

// Clone returns an independent copy of an AudioAsset whose Metadata has already
// passed audio.ValidateAudioAsset. Invalid metadata is not cloned. Callers that
// admit external AudioAssets must validate before calling Clone.
func (a AudioAsset) Clone() AudioAsset {
	metadata, ok := cloneAudioMetadata(a.Metadata, 0, make(map[uintptr]struct{}))
	if !ok {
		a.Metadata = nil
		return a
	}
	a.Metadata = metadata
	return a
}

func cloneAudioMetadata(in map[string]any, depth int, active map[uintptr]struct{}) (map[string]any, bool) {
	if in == nil {
		return nil, true
	}
	if depth > MaxAudioMetadataDepth || !enterAudioMetadataContainer(in, active) {
		return nil, false
	}
	defer leaveAudioMetadataContainer(in, active)
	out := make(map[string]any, len(in))
	for key, value := range in {
		cloned, ok := cloneAudioMetadataValue(value, depth+1, active)
		if !ok {
			return nil, false
		}
		out[key] = cloned
	}
	return out, true
}

func cloneAudioMetadataValue(value any, depth int, active map[uintptr]struct{}) (any, bool) {
	switch value := value.(type) {
	case nil, bool, string, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return value, true
	case float32:
		return value, !math.IsNaN(float64(value)) && !math.IsInf(float64(value), 0)
	case float64:
		return value, !math.IsNaN(value) && !math.IsInf(value, 0)
	case map[string]any:
		return cloneAudioMetadata(value, depth, active)
	case []any:
		if depth > MaxAudioMetadataDepth || !enterAudioMetadataContainer(value, active) {
			return nil, false
		}
		defer leaveAudioMetadataContainer(value, active)
		out := make([]any, len(value))
		for i, child := range value {
			cloned, ok := cloneAudioMetadataValue(child, depth+1, active)
			if !ok {
				return nil, false
			}
			out[i] = cloned
		}
		return out, true
	default:
		return nil, false
	}
}

func enterAudioMetadataContainer(value any, active map[uintptr]struct{}) bool {
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

func leaveAudioMetadataContainer(value any, active map[uintptr]struct{}) {
	if pointer := reflect.ValueOf(value).Pointer(); pointer != 0 {
		delete(active, pointer)
	}
}
