package render

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestBuildManifestStableElementIdentity(t *testing.T) {
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: 2 * time.Second,
		Primitives: []domain.Primitive{
			{ID: "prim-1", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "hi", "color": "#FFFFFF"}},
			{ID: "prim-2", Type: domain.PrimitiveTypeImage, Properties: map[string]any{"assetPath": "/tmp/x.png"}},
		},
	}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	manifest := buildManifest(shot, "/out/shot-1.mp4", now)

	if manifest.ShotID != shot.ID {
		t.Fatalf("ShotID = %q, want %q", manifest.ShotID, shot.ID)
	}
	if manifest.MediaPath != "/out/shot-1.mp4" {
		t.Fatalf("MediaPath = %q", manifest.MediaPath)
	}
	if len(manifest.Elements) != 2 {
		t.Fatalf("expected 2 elements, got %d", len(manifest.Elements))
	}
	if manifest.Elements[0].ID != "prim-1" || manifest.Elements[1].ID != "prim-2" {
		t.Fatalf("expected elements to reuse primitive IDs directly: %+v", manifest.Elements)
	}
	for _, e := range manifest.Elements {
		if e.ShotID != shot.ID {
			t.Fatalf("element %q: ShotID = %q, want %q", e.ID, e.ShotID, shot.ID)
		}
		if e.Start != 0 || e.End != shot.Duration {
			t.Fatalf("element %q: [%v,%v), want [0,%v)", e.ID, e.Start, e.End, shot.Duration)
		}
	}
	if manifest.Elements[0].EditableProperties["text"] != "hi" {
		t.Fatalf("expected text element's EditableProperties to expose \"text\": %+v", manifest.Elements[0].EditableProperties)
	}
	if manifest.Elements[1].EditableProperties["assetPath"] != "/tmp/x.png" {
		t.Fatalf("expected image element's EditableProperties to expose \"assetPath\": %+v", manifest.Elements[1].EditableProperties)
	}
}

func TestBuildManifestCapturesBounds(t *testing.T) {
	bounds := domain.Bounds{X: 0.1, Y: 0.2, Width: 0.3, Height: 0.4}
	shot := domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "prim-1", Type: domain.PrimitiveTypeImage, Properties: map[string]any{"assetPath": "/tmp/x.png", "bounds": bounds}},
		},
	}
	manifest := buildManifest(shot, "/out/shot-1.mp4", time.Now())
	if manifest.Elements[0].Bounds == nil || *manifest.Elements[0].Bounds != bounds {
		t.Fatalf("expected element Bounds = %+v, got %+v", bounds, manifest.Elements[0].Bounds)
	}
}

func TestMarshalUnmarshalManifestRoundTrip(t *testing.T) {
	manifest := buildManifest(domain.Shot{
		ID:       "shot-1",
		Duration: time.Second,
		Primitives: []domain.Primitive{
			{ID: "prim-1", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#000000"}},
		},
	}, "/out/shot-1.mp4", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	data, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalManifest: %v", err)
	}
	decoded, err := UnmarshalManifest(data)
	if err != nil {
		t.Fatalf("UnmarshalManifest: %v", err)
	}
	if decoded.ShotID != manifest.ShotID || decoded.MediaPath != manifest.MediaPath {
		t.Fatalf("round trip mismatch: got %+v", decoded)
	}
	if len(decoded.Elements) != 1 || decoded.Elements[0].ID != "prim-1" {
		t.Fatalf("round trip element mismatch: %+v", decoded.Elements)
	}
}

func TestMarshalManifestIsDeterministic(t *testing.T) {
	manifest := buildManifest(domain.Shot{ID: "shot-1", Duration: time.Second}, "/out/shot-1.mp4", time.Now())
	a, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalManifest: %v", err)
	}
	b, err := MarshalManifest(manifest)
	if err != nil {
		t.Fatalf("MarshalManifest: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("expected identical bytes from repeated MarshalManifest calls")
	}
}

func TestUnmarshalManifestRejectsCorruptJSON(t *testing.T) {
	if _, err := UnmarshalManifest([]byte("{not json")); err == nil {
		t.Fatalf("expected error for corrupt JSON")
	}
}

func TestUnmarshalManifestRejectsUnsupportedSchemaVersion(t *testing.T) {
	data, err := json.Marshal(manifestEnvelope{SchemaVersion: CurrentManifestSchemaVersion + 1, Manifest: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if _, err := UnmarshalManifest(data); err == nil {
		t.Fatalf("expected error for unsupported schema version")
	}
}
