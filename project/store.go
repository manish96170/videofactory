// Store persistence: a Project is saved as a single JSON file per
// project, wrapped in a small versioned envelope (PHASES.md Phase 01
// "Project loading" / "Project saving" / "Versioning strategy").
package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/manish96170/videofactory/domain"
)

// CurrentSchemaVersion is the schema version written by Marshal/Save.
// Load/Unmarshal reject any version they don't know how to read.
const CurrentSchemaVersion = 1

// ErrNotFound is returned by FileStore.Load when no project exists for
// the given ID. Use errors.Is to check for it; the error FileStore
// actually returns also includes the requested ID for logging.
var ErrNotFound = errors.New("project: not found")

// ErrUnsupportedSchemaVersion is returned when loading data written by
// a schema version this build does not understand.
var ErrUnsupportedSchemaVersion = errors.New("project: unsupported schema version")

// ErrCorrupt is wrapped into any error caused by data that isn't valid
// JSON or doesn't match the expected envelope/Project shape, so callers
// can distinguish "corrupt on disk" from other failures with errors.Is.
var ErrCorrupt = errors.New("project: corrupt data")

// ErrInvalid is wrapped into the error FileStore.Load returns when the
// decoded project fails Validate, and into the error FileStore.Save
// returns when asked to persist an invalid project.
var ErrInvalid = errors.New("project: invalid project")

// ErrIdentityMismatch is returned by FileStore.Load when the file named
// after the requested ID decodes to a Project with a different ID (for
// example because a file was copied, renamed, or edited by hand).
var ErrIdentityMismatch = errors.New("project: loaded project ID does not match requested ID")

type envelope struct {
	SchemaVersion int             `json:"schemaVersion"`
	Project       json.RawMessage `json:"project"`
}

// Marshal serializes p into its versioned, deterministic on-disk form:
// the same Project value always marshals to byte-identical output
// (PHASES.md Phase 01 "Deterministic serialization"). Determinism
// relies on Go's fixed struct field order and encoding/json's sorted-key
// map encoding; callers must not rely on map iteration order mattering
// elsewhere.
func Marshal(p domain.Project) ([]byte, error) {
	projectJSON, err := json.Marshal(p)
	if err != nil {
		return nil, fmt.Errorf("project: marshal project: %w", err)
	}
	env := envelope{SchemaVersion: CurrentSchemaVersion, Project: projectJSON}
	data, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("project: marshal envelope: %w", err)
	}
	return data, nil
}

// Unmarshal parses data written by Marshal. It returns an error wrapping
// ErrUnsupportedSchemaVersion if the envelope's schema version is not
// CurrentSchemaVersion, and an error wrapping ErrCorrupt if data is not
// valid JSON or doesn't match the expected shape (PHASES.md Phase 01
// "Corrupt-state/error handling").
func Unmarshal(data []byte) (domain.Project, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return domain.Project{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}
	if env.SchemaVersion != CurrentSchemaVersion {
		return domain.Project{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedSchemaVersion, env.SchemaVersion, CurrentSchemaVersion)
	}
	var p domain.Project
	if err := json.Unmarshal(env.Project, &p); err != nil {
		return domain.Project{}, fmt.Errorf("%w: project payload: %w", ErrCorrupt, err)
	}
	return p, nil
}

// Store is the persistence contract Phase 01 implements and later
// phases depend on. Implementations must be safe to use from multiple
// goroutines.
//
// Store does not define any cross-process locking or optimistic
// concurrency: concurrent Save calls for the same project ID are
// last-writer-wins (whichever Save's atomic rename happens last is what
// Load will subsequently see). Callers that need conflict detection
// must build it on top, e.g. by comparing Project.UpdatedAt themselves;
// Store does not update it automatically.
type Store interface {
	Load(ctx context.Context, id domain.ID) (domain.Project, error)
	Save(ctx context.Context, p domain.Project) error
}

// FileStore persists each Project as a single JSON file
// (<dir>/<id>.json) using Marshal/Unmarshal, with atomic, durable writes
// so a crash mid-write cannot leave a corrupt file in place: data is
// flushed and synced to the temp file before the atomic rename, and the
// directory entry is best-effort synced afterward (not all filesystems
// support directory fsync; a failure there does not fail the Save,
// since the rename itself already completed).
type FileStore struct {
	dir string
}

// NewFileStore creates (if needed) and returns a FileStore rooted at
// dir.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("project: create store directory: %w", err)
	}
	return &FileStore{dir: dir}, nil
}

// safeIDPattern restricts IDs used as filenames to a conservative,
// portable character set, rejecting path separators and ".." segments
// so a maliciously or accidentally constructed ID cannot escape the
// store directory (path traversal). See DECISIONS.md ADR-009.
var safeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func (s *FileStore) path(id domain.ID) (string, error) {
	raw := string(id)
	if raw == "" || !safeIDPattern.MatchString(raw) {
		return "", fmt.Errorf("project: invalid project ID for storage: %q", raw)
	}
	return filepath.Join(s.dir, raw+".json"), nil
}

// Load reads and validates the project stored under id. It returns an
// error wrapping ErrNotFound if no such project exists, ErrCorrupt or
// ErrUnsupportedSchemaVersion if the stored data can't be parsed,
// ErrIdentityMismatch if the file's content does not actually describe
// id, and ErrInvalid if the decoded project fails Validate.
func (s *FileStore) Load(ctx context.Context, id domain.ID) (domain.Project, error) {
	if err := ctx.Err(); err != nil {
		return domain.Project{}, err
	}

	path, err := s.path(id)
	if err != nil {
		return domain.Project{}, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return domain.Project{}, fmt.Errorf("%w: %q", ErrNotFound, id)
		}
		return domain.Project{}, fmt.Errorf("project: read %s: %w", path, err)
	}

	if err := ctx.Err(); err != nil {
		return domain.Project{}, err // caller gave up while/after we were reading
	}

	p, err := Unmarshal(data)
	if err != nil {
		return domain.Project{}, err
	}
	if p.ID != id {
		return domain.Project{}, fmt.Errorf("%w: file for %q contains project %q", ErrIdentityMismatch, id, p.ID)
	}
	if err := Validate(p); err != nil {
		return domain.Project{}, fmt.Errorf("%w: %q: %w", ErrInvalid, id, err)
	}
	return p, nil
}

// Save validates p and writes it atomically and durably: it writes to a
// temporary file in the same directory, flushes and syncs it, then
// renames it over the destination, so a crash or interruption mid-write
// cannot leave a half-written or corrupt file where a valid one used to
// be. See the Store doc comment for Save's concurrency semantics.
func (s *FileStore) Save(ctx context.Context, p domain.Project) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := Validate(p); err != nil {
		return fmt.Errorf("%w: refusing to save project %q: %w", ErrInvalid, p.ID, err)
	}

	path, err := s.path(p.ID)
	if err != nil {
		return err
	}

	data, err := Marshal(p)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp(s.dir, "project-*.tmp")
	if err != nil {
		return fmt.Errorf("project: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once the rename below succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("project: write temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("project: sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("project: close temp file: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err // don't commit a write the caller already gave up on
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("project: rename into place: %w", err)
	}

	// Best effort: on some filesystems (notably tmpfs, used by e.g.
	// t.TempDir() in tests on some platforms) fsync on a directory is
	// unsupported or a no-op. The rename has already committed the
	// visible file, so a failure here must not fail an otherwise
	// successful Save.
	if dir, err := os.Open(s.dir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	return nil
}
