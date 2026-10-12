// Package preview exposes a verified, read-only per-shot preview model for
// programmatic clients. Project/Timeline remains the authority for addressability.
package preview

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
)

var (
	ErrInvalid   = errors.New("preview: invalid input")
	ErrManifest  = errors.New("preview: manifest does not match authoritative timeline")
	ErrTimestamp = errors.New("preview: timestamp outside shot")
)

// Reader depends on the artifact store's byte-verifying Lookup, not filesystem
// paths or unverified manifest input. A Reader is safe for concurrent use.
type Reader struct{ store *artifact.Store }

func NewReader(store *artifact.Store) (*Reader, error) {
	if store == nil {
		return nil, ErrInvalid
	}
	return &Reader{store: store}, nil
}

// View is an immutable, detached snapshot of one verified shot. Its zero value is
// not a resolved preview. Returned manifests/elements are independent deep copies.
// Re-resolve after a rebuild; logical names are store-root-relative, not URLs or
// authorization to open arbitrary files. A View does not keep media bytes open or
// guarantee their integrity after external modification of the controlled root.
type View struct {
	identity     artifact.Identity
	mediaName    string
	manifestName string
	manifest     domain.RenderManifest
	placement    domain.TimelineEntry
	revision     string
	mediaDigest  artifact.Digest
}

// Resolve looks up an exact project/shot/key/quality identity. Missing or corrupt
// artifacts produce (zero,false,nil), as in artifact.Store.Lookup. A verified
// artifact without an accepted persisted validation verdict for this shot returns
// ErrMedia. Passed/Warning verdicts must have no failure class or retry advice.
// A verified
// artifact with stale/mismatched element metadata resolves an exact verified
// ID-only metadata binding if present, otherwise returns ErrManifest. Non-ID
// changes also wrap artifact.ErrRerenderRequired; they cannot reuse cached media.
// All rendered
// elements must exactly match the shot's authoritative Timeline elements (including
// timing, bounds and editable properties); ordering alone is immaterial. The caller
// must not mutate p during this operation. The project is never modified or retained.
func (r *Reader) Resolve(ctx context.Context, p domain.Project, id artifact.Identity) (View, bool, error) {
	if ctx == nil || r == nil || r.store == nil {
		return View{}, false, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return View{}, false, err
	}
	if p.ID != id.ProjectID || p.Plan == nil || p.Timeline == nil || project.Validate(p) != nil {
		return View{}, false, ErrInvalid
	}
	placement, ok := project.EntryForShot(*p.Timeline, id.ShotID)
	if !ok {
		return View{}, false, ErrInvalid
	}
	expected := project.ElementsForShot(*p.Timeline, id.ShotID)
	if _, err := artifact.ManifestDigest(id, expected); err != nil {
		return View{}, false, ErrInvalid
	}
	entry, hit, err := r.store.Lookup(ctx, id)
	if err != nil || !hit {
		return View{}, false, err
	}
	// Integrity proves the bytes are unchanged, not that they passed media
	// validation. Check the persisted verdict before exposing or rebinding them.
	if !acceptedValidation(id, entry.Validation) {
		return View{}, false, ErrMedia
	}
	manifest, manifestFile := entry.Manifest, entry.ManifestFile
	var bindingDigest artifact.Digest
	if !sameElements(expected, entry.Manifest.Elements) {
		binding, found, err := r.store.LookupManifest(ctx, id, expected)
		if err != nil {
			if errors.Is(err, artifact.ErrRerenderRequired) {
				return View{}, false, errors.Join(ErrManifest, err)
			}
			return View{}, false, err
		}
		if !found {
			return View{}, false, ErrManifest
		}
		manifest, manifestFile, bindingDigest = binding.Manifest, binding.ManifestFile, binding.Digest
	}
	// Bind the complete verified artifact plus authoritative placement. No clock,
	// event delivery, or project UpdatedAt is used to decide whether to reload.
	revisionData, err := json.Marshal(struct {
		Version        int
		Identity       artifact.Identity
		ArtifactDigest artifact.Digest
		BindingDigest  artifact.Digest
		TimelineID     domain.ID
		Placement      domain.TimelineEntry
	}{2, id, entry.Digest, bindingDigest, p.Timeline.ID, placement})
	if err != nil {
		return View{}, false, ErrInvalid
	}
	sum := sha256.Sum256(revisionData)
	v := View{identity: id, mediaName: entry.Media.Name, manifestName: manifestFile.Name, manifest: manifest, placement: placement, revision: hex.EncodeToString(sum[:]), mediaDigest: entry.Media.Digest}
	if err := ctx.Err(); err != nil {
		return View{}, false, err
	}
	return v, true, nil
}

func acceptedValidation(id artifact.Identity, result *domain.ValidationResult) bool {
	return result != nil && result.ID != "" && result.TargetID == id.ShotID &&
		(result.Status == domain.ValidationPassed || result.Status == domain.ValidationWarning) &&
		result.FailureClass == "" && !result.RetryRecommended
}

func (v View) Identity() artifact.Identity  { return v.identity }
func (v View) MediaName() string            { return v.mediaName }
func (v View) ManifestName() string         { return v.manifestName }
func (v View) MediaDigest() artifact.Digest { return v.mediaDigest }

// Revision is an opaque deterministic rebuild/reload token. It changes when the
// verified artifact, authoritative metadata binding or timeline placement changes.
// Identical verified content and placement yield the same token across restarts.
func (v View) Revision() string                { return v.revision }
func (v View) Placement() domain.TimelineEntry { return v.placement }

func (v View) Manifest() domain.RenderManifest {
	m := v.manifest
	if m.Elements != nil {
		m.Elements = make([]domain.VisualElement, len(v.manifest.Elements))
		for i, e := range v.manifest.Elements {
			m.Elements[i] = cloneElement(e)
		}
	}
	return m
}

// Element combines verified shot-relative metadata with project-relative bounds.
type Element struct {
	Visual       domain.VisualElement
	ProjectStart time.Duration
	ProjectEnd   time.Duration
}

func (v View) Element(id domain.ID) (Element, bool) {
	for _, e := range v.manifest.Elements {
		if e.ID == id {
			start, _ := project.AbsoluteStart(v.placement, e)
			end, _ := project.AbsoluteEnd(v.placement, e)
			return Element{Visual: cloneElement(e), ProjectStart: start, ProjectEnd: end}, true
		}
	}
	return Element{}, false
}

// Position reports both existing timestamp coordinate systems and active visual
// elements in manifest order. Intervals are end-exclusive. An empty element list
// is valid; there need not be an element covering every instant in a shot.
type Position struct {
	ProjectID        domain.ID
	ShotID           domain.ID
	ProjectTimestamp time.Duration
	ShotTimestamp    time.Duration
	Elements         []domain.VisualElement
}

func (v View) AtShot(timestamp time.Duration) (Position, error) {
	if v.revision == "" || timestamp < 0 || timestamp >= v.placement.End-v.placement.Start {
		return Position{}, ErrTimestamp
	}
	p := Position{ProjectID: v.identity.ProjectID, ShotID: v.identity.ShotID, ShotTimestamp: timestamp, ProjectTimestamp: v.placement.Start + timestamp}
	for _, e := range v.manifest.Elements {
		if e.Start <= timestamp && timestamp < e.End {
			p.Elements = append(p.Elements, cloneElement(e))
		}
	}
	return p, nil
}

func (v View) AtProject(timestamp time.Duration) (Position, error) {
	if v.revision == "" || timestamp < v.placement.Start || timestamp >= v.placement.End {
		return Position{}, ErrTimestamp
	}
	return v.AtShot(timestamp - v.placement.Start)
}

func sameElements(a, b []domain.VisualElement) bool {
	if len(a) != len(b) {
		return false
	}
	// Copy slice headers/backing arrays before sorting; maps are only read.
	a = append([]domain.VisualElement(nil), a...)
	b = append([]domain.VisualElement(nil), b...)
	sort.Slice(a, func(i, j int) bool { return a[i].ID < a[j].ID })
	sort.Slice(b, func(i, j int) bool { return b[i].ID < b[j].ID })
	x, err := json.Marshal(a)
	if err != nil {
		return false
	}
	y, err := json.Marshal(b)
	return err == nil && bytes.Equal(x, y)
}

func cloneElement(e domain.VisualElement) domain.VisualElement {
	if e.Bounds != nil {
		b := *e.Bounds
		e.Bounds = &b
	}
	if e.EditableProperties != nil {
		e.EditableProperties = cloneJSON(e.EditableProperties).(map[string]any)
	}
	return e
}

// Store.Lookup has already normalized these trees to plain JSON values.
func cloneJSON(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = cloneJSON(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = cloneJSON(child)
		}
		return out
	default:
		return value
	}
}
