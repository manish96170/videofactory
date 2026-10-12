package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/manish96170/videofactory/domain"
)

// StoreSchemaVersion versions the cache layout and completion record.
const StoreSchemaVersion = 1

var (
	ErrStoreInput       = errors.New("artifact: invalid store input")
	ErrStoreIO          = errors.New("artifact: store I/O failure")
	ErrUnsafePath       = errors.New("artifact: unsafe store path")
	ErrEntryConflict    = errors.New("artifact: entry already exists or is damaged")
	ErrStoreLimit       = errors.New("artifact: store size limit exceeded")
	ErrStoreUnsupported = errors.New("artifact: store platform unsupported")
)

// StoreConfig declares an exclusively controlled directory, shared only with
// cooperating Store instances and callers producing source files. Root must
// already exist. Sources are regular files beneath Root. Entries live in v1/;
// callers must not modify published entries or replace directories during use.
// Root's ancestors may include system aliases (e.g. macOS /var); Root itself and
// all paths beneath it must be symlink-free. This is not an OS sandbox against
// other processes with write access to the controlled directory.
type StoreConfig struct {
	Root             string
	MaxMediaBytes    int64 // default 1 GB
	MaxMetadataBytes int64 // default 4 MiB, per JSON sidecar
}

// Identity scopes a pixel key to its owning project/shot and requested quality.
// IDs use portable ASCII letters, digits, underscores and hyphens (1..128).
// BuildKey excludes ownership IDs, so they must also be checked on every hit.
type Identity struct {
	Key       Key            `json:"key"`
	ProjectID domain.ID      `json:"projectId"`
	ShotID    domain.ID      `json:"shotId"`
	Quality   domain.Quality `json:"quality"`
}

// Provenance contains only nonsecret reproducibility facts, not generation
// requests, prompts, live execution state or filesystem locations. Sources and
// Dependencies use the same semantic roles as KeyInput. Callers attest that
// these facts correspond to the supplied key; the store does not rebuild keys.
type Provenance struct {
	Seed         *string           `json:"seed,omitempty"`
	Provider     *Revision         `json:"provider,omitempty"`
	Model        *Revision         `json:"model,omitempty"`
	Profile      *Revision         `json:"profile,omitempty"`
	Sources      map[string]Digest `json:"sources,omitempty"`
	Dependencies map[string]Digest `json:"dependencies,omitempty"`
}

// PublishRequest supplies an already rendered file. MediaPath is a clean,
// slash-separated path relative to Root, never a URI or absolute path. Manifest
// MediaPath must equal it; the stored copy is rebound to its published logical
// name. The source is copied, never moved or removed. Optional results are
// snapshots of existing domain contracts; JSON extension fields must contain
// nonsecret data only. Obvious secret/prompt keys and path escapes are rejected.
// Callers must not mutate inputs or source bytes during Publish.
type PublishRequest struct {
	Identity   Identity
	MediaPath  string
	Manifest   domain.RenderManifest
	Generation *domain.GenerationResult
	Validation *domain.ValidationResult
	Provenance *Provenance
}

// FileRecord stores a logical root-relative name and verified byte identity.
type FileRecord struct {
	Name   string `json:"name"`
	Digest Digest `json:"digest"`
	Size   int64  `json:"size"`
}

// Entry is a detached snapshot. Digest hashes the completion record with its
// checksum field zeroed, binding
// identity, media/manifest digests and all optional metadata. Media.Digest is
// suitable for KeyInput.Dependencies when only upstream media affects pixels.
// No returned name is a persisted absolute filesystem path.
type Entry struct {
	Identity     Identity                 `json:"identity"`
	Media        FileRecord               `json:"media"`
	ManifestFile FileRecord               `json:"manifestFile"`
	Manifest     domain.RenderManifest    `json:"manifest"`
	Generation   *domain.GenerationResult `json:"generation,omitempty"`
	Validation   *domain.ValidationResult `json:"validation,omitempty"`
	Provenance   *Provenance              `json:"provenance,omitempty"`
	Digest       Digest                   `json:"-"`
}

type storeRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	Entry         Entry  `json:"entry"`
	Digest        Digest `json:"digest"`
}

type storeManifest struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Manifest      domain.RenderManifest `json:"manifest"`
}

// Store is concurrency-safe, including publication through multiple instances.
// A completed directory is immutable and nonempty: atomic directory rename can
// never replace it. First successful publisher wins; later publishers receive
// the verified winner. Corrupt existing entries are never repaired or deleted.
type Store struct {
	root          string
	rootInfo      os.FileInfo
	mediaLimit    int64
	metadataLimit int64
}

// NewStore opens an existing controlled root. Errors contain only safe codes.
// Filesystem access currently supports macOS and Linux.
// Other platforms fail closed with ErrStoreUnsupported.
func NewStore(cfg StoreConfig) (*Store, error) {
	if cfg.Root == "" || cfg.MaxMediaBytes < 0 || cfg.MaxMetadataBytes < 0 {
		return nil, ErrStoreInput
	}
	info, err := os.Lstat(filepath.Clean(cfg.Root))
	if err != nil {
		return nil, ErrStoreIO
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrUnsafePath
	}
	root, err := filepath.EvalSymlinks(cfg.Root)
	if err != nil {
		return nil, ErrUnsafePath
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, ErrUnsafePath
	}
	if cfg.MaxMediaBytes == 0 {
		cfg.MaxMediaBytes = 1_000_000_000
	}
	if cfg.MaxMetadataBytes == 0 {
		cfg.MaxMetadataBytes = 4 << 20
	}
	if cfg.MaxMetadataBytes > 64<<20 {
		return nil, ErrStoreInput
	}
	s := &Store{root: root, rootInfo: info, mediaLimit: cfg.MaxMediaBytes, metadataLimit: cfg.MaxMetadataBytes}
	f, err := s.openDir(".")
	if err != nil {
		return nil, safeStoreError(err)
	}
	_ = f.Close()
	return s, nil
}

// Lookup returns (zero, false, nil) for missing, partial, corrupt, unsupported,
// wrong-identity or unsafe entries. Invalid caller arguments, canceled contexts,
// and an unavailable/replaced root return safe errors. A hit checks all bytes,
// not just existence. There is no negative cache or retained mutable result.
func (s *Store) Lookup(ctx context.Context, id Identity) (Entry, bool, error) {
	if err := s.check(ctx, id); err != nil {
		return Entry{}, false, err
	}
	e, err := s.lookup(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return Entry{}, false, ctx.Err()
		}
		return Entry{}, false, nil
	}
	return e, true, nil
}

func (s *Store) lookup(ctx context.Context, id Identity) (Entry, error) {
	base := entryName(id)
	data, err := s.read(ctx, base+"/entry.json", s.metadataLimit)
	if err != nil {
		return Entry{}, err
	}
	var record storeRecord
	if strictJSON(data, &record) != nil || record.SchemaVersion != StoreSchemaVersion || record.Entry.Identity != id {
		return Entry{}, ErrEntryConflict
	}
	if sum, err := recordDigest(record); err != nil || record.Digest != sum {
		return Entry{}, ErrEntryConflict
	}
	e := record.Entry
	if e.Media.Name != base+"/media" || e.ManifestFile.Name != base+"/render.json" || e.Media.Size <= 0 || e.Media.Size > s.mediaLimit || e.ManifestFile.Size <= 0 || e.ManifestFile.Size > s.metadataLimit {
		return Entry{}, ErrEntryConflict
	}
	if err := validateEntry(e); err != nil {
		return Entry{}, err
	}
	manifest, err := s.read(ctx, e.ManifestFile.Name, s.metadataLimit)
	if err != nil || int64(len(manifest)) != e.ManifestFile.Size || Digest(sha256.Sum256(manifest)) != e.ManifestFile.Digest {
		return Entry{}, ErrEntryConflict
	}
	var sidecar storeManifest
	if strictJSON(manifest, &sidecar) != nil || sidecar.SchemaVersion != StoreSchemaVersion {
		return Entry{}, ErrEntryConflict
	}
	expected, err := json.Marshal(storeManifest{StoreSchemaVersion, e.Manifest})
	if err != nil || string(expected) != string(manifest) {
		return Entry{}, ErrEntryConflict
	}
	f, err := s.openRegular(e.Media.Name)
	if err != nil {
		return Entry{}, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := copyContext(ctx, h, f, s.mediaLimit)
	var mediaDigest Digest
	copy(mediaDigest[:], h.Sum(nil))
	if err != nil || n != e.Media.Size || mediaDigest != e.Media.Digest {
		return Entry{}, ErrEntryConflict
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	e.Digest = record.Digest
	return e, nil
}

// Publish snapshots input, copies/hashes media into an owned staging directory,
// writes and syncs both sidecars, then atomically renames the directory. Observed
// cancellation before the rename prevents publication. Cancellation after that
// commit point does not roll back a published entry. A retry returns the winner.
// File/directory sync before rename is required; parent sync afterward is best
// effort (visibility is committed). No published entry is ever removed.
func (s *Store) Publish(ctx context.Context, req PublishRequest) (Entry, error) {
	if err := s.check(ctx, req.Identity); err != nil {
		return Entry{}, err
	}
	if !logicalName(req.MediaPath) || req.Manifest.MediaPath != req.MediaPath {
		return Entry{}, ErrUnsafePath
	}
	for _, element := range req.Manifest.Elements {
		if !plainJSON(element.EditableProperties, 0) {
			return Entry{}, ErrStoreInput
		}
	}
	if req.Generation != nil && !plainJSON(req.Generation.Metadata, 0) || req.Validation != nil && !plainJSON(req.Validation.Details, 0) {
		return Entry{}, ErrStoreInput
	}
	// Decode into fresh storage, never back into the caller-aliased request.
	data, err := json.Marshal(req)
	if err != nil {
		return Entry{}, ErrStoreInput
	}
	if int64(len(data)) > s.metadataLimit {
		return Entry{}, ErrStoreLimit
	}
	var snapshot PublishRequest
	if strictJSON(data, &snapshot) != nil {
		return Entry{}, ErrStoreInput
	}
	req = snapshot
	base := entryName(req.Identity)
	e := Entry{Identity: req.Identity, Media: FileRecord{Name: base + "/media"}, ManifestFile: FileRecord{Name: base + "/render.json"}, Manifest: req.Manifest, Generation: req.Generation, Validation: req.Validation, Provenance: req.Provenance}
	e.Manifest.MediaPath = e.Media.Name
	if err := validateEntry(e); err != nil {
		return Entry{}, err
	}
	if prior, hit, err := s.Lookup(ctx, req.Identity); err != nil {
		return Entry{}, err
	} else if hit {
		return prior, nil
	}
	// Even an invalid existing target is preserved for explicit owner recovery.
	if _, err := s.checkedPath(base, false); err == nil {
		// A cooperating publisher may have committed after the first lookup.
		if winner, hit, lookupErr := s.Lookup(ctx, req.Identity); lookupErr != nil {
			return Entry{}, lookupErr
		} else if hit {
			return winner, nil
		}
		return Entry{}, ErrEntryConflict
	} else if !errors.Is(err, os.ErrNotExist) {
		return Entry{}, ErrUnsafePath
	}
	source, err := s.openRegular(req.MediaPath)
	if err != nil {
		return Entry{}, safeStoreError(err)
	}
	defer source.Close()
	parent := filepath.ToSlash(filepath.Dir(base))
	if err := s.makeDirs(parent); err != nil {
		return Entry{}, safeStoreError(err)
	}
	parentPath, err := s.checkedPath(parent, true)
	if err != nil {
		return Entry{}, safeStoreError(err)
	}
	stage, err := os.MkdirTemp(parentPath, ".stage-")
	if err != nil {
		return Entry{}, ErrStoreIO
	}
	stageName := parent + "/" + filepath.Base(stage)
	published := false
	defer func() {
		if !published {
			s.removeStage(stageName)
		}
	}()
	out, err := s.create(stageName + "/media")
	if err != nil {
		return Entry{}, safeStoreError(err)
	}
	h := sha256.New()
	n, copyErr := copyContext(ctx, io.MultiWriter(out, h), source, s.mediaLimit)
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil {
		return Entry{}, safeStoreError(copyErr)
	}
	if syncErr != nil || closeErr != nil {
		return Entry{}, ErrStoreIO
	}
	if n == 0 {
		return Entry{}, ErrStoreInput
	}
	e.Media.Size = n
	copy(e.Media.Digest[:], h.Sum(nil))
	manifest, err := json.Marshal(storeManifest{StoreSchemaVersion, e.Manifest})
	if err != nil {
		return Entry{}, ErrStoreInput
	}
	e.ManifestFile.Size = int64(len(manifest))
	e.ManifestFile.Digest = Digest(sha256.Sum256(manifest))
	record := storeRecord{SchemaVersion: StoreSchemaVersion, Entry: e}
	record.Digest, err = recordDigest(record)
	if err != nil {
		return Entry{}, ErrStoreInput
	}
	data, err = json.Marshal(record)
	if err != nil {
		return Entry{}, ErrStoreInput
	}
	if int64(len(data)) > s.metadataLimit || int64(len(manifest)) > s.metadataLimit {
		return Entry{}, ErrStoreLimit
	}
	if err := s.write(stageName+"/render.json", manifest); err != nil {
		return Entry{}, safeStoreError(err)
	}
	if err := s.write(stageName+"/entry.json", data); err != nil {
		return Entry{}, safeStoreError(err)
	}
	if err := s.syncDir(stageName); err != nil {
		return Entry{}, ErrStoreIO
	}
	stagePath, err := s.checkedPath(stageName, true)
	if err != nil {
		return Entry{}, safeStoreError(err)
	}
	parentPath, err = s.checkedPath(parent, true)
	if err != nil {
		return Entry{}, safeStoreError(err)
	}
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(stagePath, filepath.Join(parentPath, filepath.Base(base))); err != nil {
		if winner, hit, lookupErr := s.Lookup(ctx, req.Identity); lookupErr != nil {
			return Entry{}, lookupErr
		} else if hit {
			return winner, nil
		}
		return Entry{}, ErrEntryConflict
	}
	published = true
	_ = s.syncDir(parent)
	e.Digest = record.Digest
	return e, nil
}

func recordDigest(record storeRecord) (Digest, error) {
	record.Digest = Digest{}
	data, err := json.Marshal(record)
	return Digest(sha256.Sum256(data)), err
}

func (s *Store) check(ctx context.Context, id Identity) error {
	if ctx == nil || s == nil || s.root == "" || !validIdentity(id) {
		return ErrStoreInput
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.checkRoot()
}

func entryName(id Identity) string {
	return "v1/" + string(id.ProjectID) + "/" + string(id.ShotID) + "/" + string(id.Quality) + "/" + string(id.Key)
}

func safeStoreError(err error) error {
	for _, safe := range []error{context.Canceled, context.DeadlineExceeded, ErrUnsafePath, ErrStoreInput, ErrStoreLimit, ErrStoreUnsupported} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return ErrStoreIO
}
