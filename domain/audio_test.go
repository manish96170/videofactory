package domain

import (
	"encoding/json"
	"testing"
	"time"
)

func TestAudioContractsJSONAndClone(t *testing.T) {
	asset := AudioAsset{
		ID:           "audio-1",
		ShotID:       "shot-1",
		URI:          "audio/narration.wav",
		Codec:        "wav",
		Duration:     time.Second,
		SampleRateHz: 48000,
		Channels:     1,
		Metadata: map[string]any{
			"tags": []any{"narration"},
			"gain": 0.75,
		},
	}

	encoded, err := json.Marshal(asset)
	if err != nil {
		t.Fatalf("marshal audio asset: %v", err)
	}
	var decoded AudioAsset
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal audio asset: %v", err)
	}
	if decoded.Duration != time.Second {
		t.Fatalf("duration = %v, want %v", decoded.Duration, time.Second)
	}
	if gain, ok := decoded.Metadata["gain"].(float64); !ok || gain != 0.75 {
		t.Fatalf("metadata gain = %#v, want float64(0.75)", decoded.Metadata["gain"])
	}
	tags, ok := decoded.Metadata["tags"].([]any)
	if !ok || len(tags) != 1 || tags[0] != "narration" {
		t.Fatalf("metadata tags = %#v, want []any{\"narration\"}", decoded.Metadata["tags"])
	}

	clone := decoded.Clone()
	clone.Metadata["tags"].([]any)[0] = "changed"
	if decoded.Metadata["tags"].([]any)[0] != "narration" {
		t.Fatal("clone retained nested metadata")
	}
	word := WordTiming{AudioAssetID: asset.ID, Word: "hello", Start: 0, End: time.Second}
	cue := SubtitleCue{ID: "cue-1", AudioAssetID: asset.ID, Text: "hello", Start: 0, End: time.Second}
	target := TemporalTarget{ShotID: asset.ShotID, ElementID: "subtitle-1", Start: 0, End: time.Second}
	if word.AudioAssetID != cue.AudioAssetID || target.ShotID != asset.ShotID {
		t.Fatal("audio/timeline linkage changed")
	}
}

func TestAudioAssetCloneDoesNotTraverseUnsupportedOrCyclicMetadata(t *testing.T) {
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	deep := any("leaf")
	for range 10000 {
		deep = []any{deep}
	}
	for _, metadata := range []map[string]any{
		{"typedMap": map[string][]string{"labels": {"narration"}}},
		{"typedSlice": []map[string]int{{"take": 1}}},
		{"bytes": []byte{1, 2, 3}},
		{"self": cyclic},
		{"deep": deep},
	} {
		clone := (AudioAsset{Metadata: metadata}).Clone()
		if clone.Metadata != nil {
			t.Fatalf("unsupported metadata was cloned: %#v", clone.Metadata)
		}
	}
}
