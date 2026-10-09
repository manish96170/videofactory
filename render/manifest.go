// Manifest construction: turns a rendered Shot's Primitives into the
// domain.RenderManifest/domain.VisualElement metadata PHASES.md section
// 4.6.1 describes (shot-017.mp4 / shot-017.render.json), using the same
// versioned-envelope persistence pattern as project.Marshal/Unmarshal
// (DECISIONS.md ADR-010/ADR-011) for consistency across the module.
package render

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/idgen"
)

// CurrentManifestSchemaVersion is the schema version written by
// MarshalManifest. UnmarshalManifest rejects any version it doesn't
// know how to read.
const CurrentManifestSchemaVersion = 1

// ErrUnsupportedManifestSchemaVersion is returned when loading a
// manifest written by a schema version this build doesn't understand.
var ErrUnsupportedManifestSchemaVersion = errors.New("render: unsupported manifest schema version")

// ErrManifestCorrupt is wrapped into any error caused by manifest data
// that isn't valid JSON or doesn't match the expected shape.
var ErrManifestCorrupt = errors.New("render: corrupt manifest data")

type manifestEnvelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	Manifest      json.RawMessage `json:"manifest"`
}

// buildManifest derives a domain.RenderManifest (and one
// domain.VisualElement per Primitive) from a rendered shot.
//
// Each VisualElement reuses its Primitive's own ID directly as its ID:
// Primitive IDs are already stable (assigned by whatever created the
// Shot — a test fixture today, the Director from Phase 05 later), so
// this satisfies "stable rendered element identity" without needing a
// separate content-hashing scheme (PHASES.md Phase 03). Every element
// spans the full shot (Start=0, End=shot.Duration): Phase 03 doesn't
// yet support a primitive active for only part of its shot.
func buildManifest(shot domain.Shot, mediaPath string, now time.Time) domain.RenderManifest {
	elements := make([]domain.VisualElement, 0, len(shot.Primitives))
	for _, p := range shot.Primitives {
		elements = append(elements, primitiveToElement(shot, p))
	}
	return domain.RenderManifest{
		ID:        domain.ID(idgen.New()),
		ShotID:    shot.ID,
		MediaPath: mediaPath,
		Elements:  elements,
		CreatedAt: now,
	}
}

// primitiveToElement maps one Primitive to its VisualElement,
// exposing the subset of Properties that make sense as directly
// user-editable metadata for a preview surface (PHASES.md section
// 4.6.1/4.6.2) — e.g. Text's "text"/"color", not internal
// renderer-only detail.
func primitiveToElement(shot domain.Shot, p domain.Primitive) domain.VisualElement {
	elem := domain.VisualElement{
		ID:     p.ID,
		ShotID: shot.ID,
		Type:   string(p.Type),
		Start:  0,
		End:    shot.Duration,
	}

	if bounds, err := boundsProp(p.Properties, "bounds"); err == nil && bounds != nil {
		b := *bounds
		elem.Bounds = &b
	}

	switch p.Type {
	case domain.PrimitiveTypeText:
		editable := map[string]any{}
		if v, ok := p.Properties["text"]; ok {
			editable["text"] = v
		}
		if v, ok := p.Properties["color"]; ok {
			editable["color"] = v
		}
		if v, ok := p.Properties["fontSize"]; ok {
			editable["fontSize"] = v
		}
		elem.EditableProperties = editable
	case domain.PrimitiveTypeImage:
		editable := map[string]any{}
		if v, ok := p.Properties["assetPath"]; ok {
			editable["assetPath"] = v
		}
		elem.EditableProperties = editable
	case domain.PrimitiveTypeBackground:
		editable := map[string]any{}
		if v, ok := p.Properties["color"]; ok {
			editable["color"] = v
		}
		elem.EditableProperties = editable
	case domain.PrimitiveTypeTransition:
		editable := map[string]any{}
		if v, ok := p.Properties["style"]; ok {
			editable["style"] = v
		}
		elem.EditableProperties = editable
	}

	return elem
}

// MarshalManifest serializes manifest into its versioned, deterministic
// on-disk form, matching project.Marshal's conventions.
func MarshalManifest(manifest domain.RenderManifest) ([]byte, error) {
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("render: marshal manifest: %w", err)
	}
	env := manifestEnvelope{SchemaVersion: CurrentManifestSchemaVersion, Manifest: manifestJSON}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render: marshal manifest envelope: %w", err)
	}
	return data, nil
}

// UnmarshalManifest parses data written by MarshalManifest.
func UnmarshalManifest(data []byte) (domain.RenderManifest, error) {
	var env manifestEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.RenderManifest{}, fmt.Errorf("%w: %w", ErrManifestCorrupt, err)
	}
	if env.SchemaVersion != CurrentManifestSchemaVersion {
		return domain.RenderManifest{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedManifestSchemaVersion, env.SchemaVersion, CurrentManifestSchemaVersion)
	}
	var m domain.RenderManifest
	if err := json.Unmarshal(env.Manifest, &m); err != nil {
		return domain.RenderManifest{}, fmt.Errorf("%w: manifest payload: %w", ErrManifestCorrupt, err)
	}
	return m, nil
}
