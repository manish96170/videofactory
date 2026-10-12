package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/manish96170/videofactory/domain"
)

// ErrRerenderRequired means verified cached media cannot be rebound to the
// requested metadata: visual facts changed beyond element IDs.
var ErrRerenderRequired = errors.New("artifact: visual metadata changed; rerender required")

// ManifestBinding is a detached metadata version for an immutable artifact. Its
// digest binds the verified original entry to the authoritative visual elements.
// The original entry and media are never changed or copied by metadata binding.
type ManifestBinding struct {
	ArtifactDigest  Digest                `json:"artifactDigest"`
	AuthorityDigest Digest                `json:"authorityDigest"`
	Manifest        domain.RenderManifest `json:"manifest"`
	ManifestFile    FileRecord            `json:"manifestFile"`
	Digest          Digest                `json:"-"`
}

type bindingRecord struct {
	SchemaVersion int             `json:"schemaVersion"`
	Identity      Identity        `json:"identity"`
	Binding       ManifestBinding `json:"binding"`
	Digest        Digest          `json:"digest"`
}

// ManifestDigest validates and hashes a canonical, detached visual authority.
// It is metadata identity only, never a pixel cache key. Callers attest that the
// elements describe the pixels associated with id, and must separately validate
// their project/timeline relationships. No paths, clocks or raw execution data
// are accepted. Callers must not mutate elements during any binding operation.
func ManifestDigest(id Identity, elements []domain.VisualElement) (Digest, error) {
	_, digest, err := manifestAuthority(id, elements)
	return digest, err
}

func manifestAuthority(id Identity, elements []domain.VisualElement) ([]domain.VisualElement, Digest, error) {
	for _, e := range elements {
		if !plainJSON(e.EditableProperties, 0) {
			return nil, Digest{}, ErrStoreInput
		}
	}
	data, err := json.Marshal(elements)
	if err != nil {
		return nil, Digest{}, ErrStoreInput
	}
	var owned []domain.VisualElement
	if strictJSON(data, &owned) != nil {
		return nil, Digest{}, ErrStoreInput
	}
	// Treat nil/empty and element ordering identically, as preview matching does.
	if len(owned) == 0 {
		owned = nil
	}
	sort.Slice(owned, func(i, j int) bool { return owned[i].ID < owned[j].ID })
	e := Entry{Identity: id, Manifest: domain.RenderManifest{ID: "authority", ShotID: id.ShotID, Elements: owned}}
	if err := validateEntry(e); err != nil {
		return nil, Digest{}, err
	}
	data, err = json.Marshal(owned)
	if err != nil {
		return nil, Digest{}, ErrStoreInput
	}
	return owned, Digest(sha256.Sum256(data)), nil
}

func bindingName(id Identity, artifactDigest, authorityDigest Digest) string {
	return "bindings/" + entryName(id) + "/" + hex.EncodeToString(artifactDigest[:]) + "/" + hex.EncodeToString(authorityDigest[:])
}

func bindingDigest(record bindingRecord) (Digest, error) {
	record.Digest = Digest{}
	data, err := json.Marshal(record)
	return Digest(sha256.Sum256(data)), err
}

// checkManifestRebind compares already validated, detached elements as multisets.
// Only IDs are erased; marshaling the whole struct keeps every other field in
// the comparison. Sorting by content (not renamed IDs) permits arbitrary ID-only
// renames and order changes while preserving duplicate-content multiplicity.
func checkManifestRebind(ctx context.Context, cached, requested []domain.VisualElement) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(cached) != len(requested) {
		return ErrRerenderRequired
	}
	canonical := func(elements []domain.VisualElement) ([]string, error) {
		out := make([]string, len(elements))
		for i, e := range elements {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			e.ID = "" // e is a value copy; nested data is only read.
			data, err := json.Marshal(e)
			if err != nil {
				return nil, ErrStoreInput
			}
			out[i] = string(data)
		}
		sort.Strings(out)
		return out, nil
	}
	a, err := canonical(cached)
	if err != nil {
		return err
	}
	b, err := canonical(requested)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	for i := range a {
		if a[i] != b[i] {
			return ErrRerenderRequired
		}
	}
	return nil
}

// LookupManifest resolves exactly the requested authority, never a latest pointer.
// Missing/corrupt bindings or base artifacts are safe misses. Every hit verifies
// the base artifact's media and sidecars as well as the binding's bytes/digests.
// Valid authority differing from the base beyond IDs returns ErrRerenderRequired.
func (s *Store) LookupManifest(ctx context.Context, id Identity, elements []domain.VisualElement) (ManifestBinding, bool, error) {
	if err := s.check(ctx, id); err != nil {
		return ManifestBinding{}, false, err
	}
	owned, authority, err := manifestAuthority(id, elements)
	if err != nil {
		return ManifestBinding{}, false, err
	}
	e, hit, err := s.Lookup(ctx, id)
	if err != nil || !hit {
		return ManifestBinding{}, false, err
	}
	if err := checkManifestRebind(ctx, e.Manifest.Elements, owned); err != nil {
		return ManifestBinding{}, false, err
	}
	b, err := s.lookupManifest(ctx, e, authority)
	if err != nil {
		return ManifestBinding{}, false, ctx.Err()
	}
	return b, true, nil
}

func (s *Store) lookupManifest(ctx context.Context, e Entry, authority Digest) (ManifestBinding, error) {
	base := bindingName(e.Identity, e.Digest, authority)
	data, err := s.read(ctx, base+"/entry.json", s.metadataLimit)
	if err != nil {
		return ManifestBinding{}, err
	}
	var record bindingRecord
	if strictJSON(data, &record) != nil || record.SchemaVersion != StoreSchemaVersion || record.Identity != e.Identity {
		return ManifestBinding{}, ErrEntryConflict
	}
	b := record.Binding
	sum, err := bindingDigest(record)
	if err != nil || sum != record.Digest || b.ArtifactDigest != e.Digest || b.AuthorityDigest != authority || b.ManifestFile.Name != base+"/render.json" || b.ManifestFile.Size <= 0 || b.ManifestFile.Size > s.metadataLimit {
		return ManifestBinding{}, ErrEntryConflict
	}
	_, actual, err := manifestAuthority(e.Identity, b.Manifest.Elements)
	if err != nil || actual != authority || b.Manifest.ID != domain.ID("binding-"+hex.EncodeToString(authority[:])) || b.Manifest.ShotID != e.Identity.ShotID || b.Manifest.MediaPath != e.Media.Name || !b.Manifest.CreatedAt.IsZero() {
		return ManifestBinding{}, ErrEntryConflict
	}
	// Integrity alone is insufficient: older bindings may contain non-ID edits.
	if err := checkManifestRebind(ctx, e.Manifest.Elements, b.Manifest.Elements); err != nil {
		return ManifestBinding{}, err
	}
	data, err = s.read(ctx, b.ManifestFile.Name, s.metadataLimit)
	want, marshalErr := json.Marshal(storeManifest{StoreSchemaVersion, b.Manifest})
	if err != nil || marshalErr != nil || int64(len(data)) != b.ManifestFile.Size || Digest(sha256.Sum256(data)) != b.ManifestFile.Digest || string(data) != string(want) {
		return ManifestBinding{}, ErrEntryConflict
	}
	if err := ctx.Err(); err != nil {
		return ManifestBinding{}, err
	}
	b.Digest = record.Digest
	return b, nil
}

// BindManifest publishes metadata for an existing verified artifact without
// rendering, only if elements differ from the base manifest solely in their IDs
// (ordering is immaterial). Other valid edits return ErrRerenderRequired before
// any binding writes. The caller owns project authority validation and pixel-key
// attestation; structural/metadata safety and ID-only equivalence are checked here.
// Like Publish, atomic rename is the commit point; existing versions (including
// damaged versions) are never replaced/deleted. Cancellation before commit
// cleans only this operation's staging directory; a retry resolves the winner.
func (s *Store) BindManifest(ctx context.Context, id Identity, elements []domain.VisualElement) (ManifestBinding, error) {
	if err := s.check(ctx, id); err != nil {
		return ManifestBinding{}, err
	}
	owned, authority, err := manifestAuthority(id, elements)
	if err != nil {
		return ManifestBinding{}, err
	}
	e, hit, err := s.Lookup(ctx, id)
	if err != nil {
		return ManifestBinding{}, err
	}
	if !hit {
		return ManifestBinding{}, ErrEntryConflict
	}
	if err := checkManifestRebind(ctx, e.Manifest.Elements, owned); err != nil {
		return ManifestBinding{}, err
	}
	if prior, err := s.lookupManifest(ctx, e, authority); err == nil {
		return prior, nil
	}
	base := bindingName(id, e.Digest, authority)
	if _, err := s.checkedPath(base, false); err == nil {
		if winner, err := s.lookupManifest(ctx, e, authority); err == nil {
			return winner, nil
		}
		if ctx.Err() != nil {
			return ManifestBinding{}, ctx.Err()
		}
		return ManifestBinding{}, ErrEntryConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return ManifestBinding{}, ErrUnsafePath
	}
	b := ManifestBinding{ArtifactDigest: e.Digest, AuthorityDigest: authority,
		Manifest: domain.RenderManifest{ID: domain.ID("binding-" + hex.EncodeToString(authority[:])), ShotID: id.ShotID, MediaPath: e.Media.Name, Elements: owned}}
	manifest, err := json.Marshal(storeManifest{StoreSchemaVersion, b.Manifest})
	if err != nil {
		return ManifestBinding{}, ErrStoreInput
	}
	b.ManifestFile = FileRecord{Name: base + "/render.json", Digest: Digest(sha256.Sum256(manifest)), Size: int64(len(manifest))}
	record := bindingRecord{SchemaVersion: StoreSchemaVersion, Identity: id, Binding: b}
	record.Digest, err = bindingDigest(record)
	if err != nil {
		return ManifestBinding{}, ErrStoreInput
	}
	data, err := json.Marshal(record)
	if err != nil {
		return ManifestBinding{}, ErrStoreInput
	}
	if int64(len(data)) > s.metadataLimit || int64(len(manifest)) > s.metadataLimit {
		return ManifestBinding{}, ErrStoreLimit
	}
	if err := ctx.Err(); err != nil {
		return ManifestBinding{}, err
	}
	parent := path.Dir(base)
	if err := s.makeDirs(parent); err != nil {
		return ManifestBinding{}, safeStoreError(err)
	}
	parentPath, err := s.checkedPath(parent, true)
	if err != nil {
		return ManifestBinding{}, safeStoreError(err)
	}
	stage, err := os.MkdirTemp(parentPath, ".stage-")
	if err != nil {
		return ManifestBinding{}, ErrStoreIO
	}
	stageName := parent + "/" + filepath.Base(stage)
	defer s.removeStage(stageName)
	if err := s.write(stageName+"/render.json", manifest); err != nil {
		return ManifestBinding{}, safeStoreError(err)
	}
	if err := s.write(stageName+"/entry.json", data); err != nil {
		return ManifestBinding{}, safeStoreError(err)
	}
	if err := s.syncDir(stageName); err != nil {
		return ManifestBinding{}, ErrStoreIO
	}
	stagePath, err := s.checkedPath(stageName, true)
	if err != nil {
		return ManifestBinding{}, safeStoreError(err)
	}
	parentPath, err = s.checkedPath(parent, true)
	if err != nil {
		return ManifestBinding{}, safeStoreError(err)
	}
	if err := ctx.Err(); err != nil {
		return ManifestBinding{}, err
	}
	if err := os.Rename(stagePath, filepath.Join(parentPath, path.Base(base))); err != nil {
		if winner, err := s.lookupManifest(ctx, e, authority); err == nil {
			return winner, nil
		}
		if ctx.Err() != nil {
			return ManifestBinding{}, ctx.Err()
		}
		return ManifestBinding{}, ErrEntryConflict
	}
	_ = s.syncDir(parent)
	b.Digest = record.Digest
	return b, nil
}
