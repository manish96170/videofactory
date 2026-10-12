package audio

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func testAsset() domain.AudioAsset {
	return domain.AudioAsset{ID: "audio-1", ShotID: "shot-1", URI: "assets/narration.wav", Codec: "wav", Duration: 2 * time.Second, SampleRateHz: 48000, Channels: 2, Metadata: map[string]any{"nested": map[string]any{"name": "narration"}}}
}

func testWords() []domain.WordTiming {
	return []domain.WordTiming{{AudioAssetID: "audio-1", Word: "hello", Start: 0, End: time.Second}, {AudioAssetID: "audio-1", Word: "world", Start: time.Second, End: 2 * time.Second}}
}

func testCues() []domain.SubtitleCue {
	return []domain.SubtitleCue{{ID: "cue-1", AudioAssetID: "audio-1", Text: "hello", Start: 0, End: time.Second}, {ID: "cue-2", AudioAssetID: "audio-1", Text: "world", Start: time.Second, End: 2 * time.Second}}
}

func TestValidateNormalizedAudioTimeline(t *testing.T) {
	asset := testAsset()
	if err := ValidateAudioAsset(asset); err != nil {
		t.Fatalf("asset: %v", err)
	}
	for _, codec := range []string{"aac", "flac", "mp3", "opus", "pcm_s16le", "wav"} {
		candidate := asset
		candidate.Codec = codec
		if err := ValidateAudioAsset(candidate); err != nil {
			t.Fatalf("codec %q rejected: %v", codec, err)
		}
	}
	if err := ValidateWordTimings(asset, testWords()); err != nil {
		t.Fatalf("words: %v", err)
	}
	if err := ValidateSubtitleCues(asset, testCues()); err != nil {
		t.Fatalf("cues: %v", err)
	}
	for _, bad := range []domain.AudioAsset{{ID: "a", ShotID: "s", URI: "x", Codec: "unknown", Duration: time.Second, SampleRateHz: 1, Channels: 1}, {ID: "a", ShotID: "s", URI: "x", Codec: "wav", Duration: 0, SampleRateHz: 1, Channels: 1}} {
		if err := ValidateAudioAsset(bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("bad asset accepted: %+v", bad)
		}
	}
	bad := testWords()
	bad[1].Start = 900 * time.Millisecond
	if err := ValidateWordTimings(asset, bad); !errors.Is(err, ErrInvalid) {
		t.Fatal("overlapping words accepted")
	}
	badCue := testCues()
	badCue[1].AudioAssetID = "other"
	if err := ValidateSubtitleCues(asset, badCue); !errors.Is(err, ErrInvalid) {
		t.Fatal("mislinked cue accepted")
	}
}

func TestValidateAudioAssetRejectsUnsafeMetadataAndInvalidUTF8(t *testing.T) {
	deep := any("leaf")
	for range domain.MaxAudioMetadataDepth + 2 {
		deep = []any{deep}
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	for name, metadata := range map[string]map[string]any{
		"NaN":         {"value": math.NaN()},
		"infinity":    {"value": math.Inf(1)},
		"typed map":   {"value": map[string]string{"x": "y"}},
		"typed slice": {"value": []string{"x"}},
		"array":       {"value": [1]string{"x"}},
		"struct":      {"value": struct{ Name string }{"x"}},
		"pointer":     {"value": new(string)},
		"deep":        {"value": deep},
		"cycle":       {"value": cycle},
		"bad string":  {"value": string([]byte{0xff})},
		"bad key":     {string([]byte{0xff}): "value"},
	} {
		t.Run(name, func(t *testing.T) {
			asset := testAsset()
			asset.Metadata = metadata
			if err := ValidateAudioAsset(asset); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted unsafe metadata: %v", err)
			}
		})
	}

	for name, mutate := range map[string]func(*domain.AudioAsset){
		"URI": func(asset *domain.AudioAsset) { asset.URI = string([]byte{0xff}) },
		"ID":  func(asset *domain.AudioAsset) { asset.ID = domain.ID([]byte{0xff}) },
	} {
		t.Run("invalid UTF-8 "+name, func(t *testing.T) {
			asset := testAsset()
			mutate(&asset)
			if err := ValidateAudioAsset(asset); !errors.Is(err, ErrInvalid) {
				t.Fatalf("accepted invalid UTF-8: %v", err)
			}
		})
	}

	words := testWords()
	words[0].Word = string([]byte{0xff})
	if err := ValidateWordTimings(testAsset(), words); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted invalid UTF-8 word")
	}
	cues := testCues()
	cues[0].Text = string([]byte{0xff})
	if err := ValidateSubtitleCues(testAsset(), cues); !errors.Is(err, ErrInvalid) {
		t.Fatal("accepted invalid UTF-8 cue")
	}
}

func TestValidateTemporalTarget(t *testing.T) {
	valid := domain.TemporalTarget{ShotID: "shot-1", ElementID: "element-1", Start: 0, End: time.Second}
	if err := ValidateTemporalTarget(valid, 2*time.Second); err != nil {
		t.Fatalf("valid target: %v", err)
	}
	valid.EventID = "beat" // exactly one target is required.
	if err := ValidateTemporalTarget(valid, 2*time.Second); !errors.Is(err, ErrInvalid) {
		t.Fatal("ambiguous target accepted")
	}
}

func TestSyncAdvice(t *testing.T) {
	asset := testAsset()
	got, err := ValidateSync(asset, 1900*time.Millisecond, 100*time.Millisecond)
	if err != nil || got.Advice != SyncAligned || got.Delta != 100*time.Millisecond {
		t.Fatalf("aligned: %+v, %v", got, err)
	}
	got, err = ValidateSync(asset, time.Second, 0)
	if !errors.Is(err, ErrSyncMismatch) || got.Advice != SyncAudioLong {
		t.Fatalf("long: %+v, %v", got, err)
	}
}

func TestFixtureVoiceCopiesAndHonorsCancellation(t *testing.T) {
	result := VoiceResult{Asset: testAsset(), Words: testWords(), Cues: testCues()}
	p, err := NewFixtureVoiceProvider("fixture", map[string]VoiceResult{"hello world": result})
	if err != nil {
		t.Fatal(err)
	}
	result.Asset.Metadata["nested"].(map[string]any)["name"] = "mutated"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Synthesize(ctx, VoiceRequest{ID: "request-1", ShotID: "shot-1", Script: "hello world", Voice: "neutral"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	first, err := p.Synthesize(context.Background(), VoiceRequest{ID: "request-1", ShotID: "shot-1", Script: "hello world", Voice: "neutral"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Asset.Metadata["nested"].(map[string]any)["name"] != "narration" {
		t.Fatal("constructor retained metadata")
	}
	first.Words[0].Word = "changed"
	second, err := p.Synthesize(context.Background(), VoiceRequest{ID: "request-1", ShotID: "shot-1", Script: "hello world", Voice: "neutral"})
	if err != nil || second.Words[0].Word != "hello" {
		t.Fatalf("egress retained mutation: %+v, %v", second, err)
	}
}
