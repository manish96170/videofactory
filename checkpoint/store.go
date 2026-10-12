package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/manish96170/videofactory/artifact"
)

// StoreConfig requires an existing controlled root, shared only with cooperating
// stores. Root itself must not be a symlink; ancestor system aliases are resolved
// once at construction. The owner must prevent external directory replacement
// throughout operations. This is not a sandbox against other filesystem writers.
type StoreConfig struct {
	Root     string
	MaxBytes int64 // default 4 MiB; maximum 64 MiB
}

// Store uses one atomic-replace file per project/quality. Concurrent calls and
// multiple instances are safe, with last-rename-wins semantics (no CAS/merge).
// Callers must serialize logical updates to avoid overwriting newer progress.
// Runtime root paths are never serialized. Errors expose only safe core codes.
type Store struct {
	root     string
	rootInfo os.FileInfo
	limit    int64
}

type record struct {
	Checkpoint Checkpoint      `json:"checkpoint"`
	Digest     artifact.Digest `json:"digest"`
}

func NewStore(cfg StoreConfig) (*Store, error) {
	if cfg.Root == "" || cfg.MaxBytes < 0 || cfg.MaxBytes > 64<<20 {
		return nil, ErrInvalid
	}
	i, err := os.Lstat(filepath.Clean(cfg.Root))
	if err != nil {
		return nil, ErrIO
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
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
	if cfg.MaxBytes == 0 {
		cfg.MaxBytes = 4 << 20
	}
	s := &Store{root: root, rootInfo: i, limit: cfg.MaxBytes}
	f, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return s, nil
}

// fileName hashes case-sensitive handles, avoiding case-folding filesystem
// aliases. ProjectHash is deliberately excluded: Load detects a stale revision
// instead of silently treating it as a nonexistent checkpoint.
func fileName(id Identity) string {
	sum := sha256.Sum256([]byte(string(id.ProjectID) + "\x00" + string(id.Quality)))
	return "checkpoint-v1-" + hex.EncodeToString(sum[:]) + ".json"
}

func (s *Store) openRoot() (*os.File, error) {
	i, err := os.Lstat(s.root)
	if err != nil {
		return nil, ErrIO
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || !os.SameFile(i, s.rootInfo) {
		return nil, ErrUnsafePath
	}
	f, err := openNoFollow(s.root, true)
	if err != nil {
		return nil, safeError(err)
	}
	i, err = f.Stat()
	if err != nil || !os.SameFile(i, s.rootInfo) {
		_ = f.Close()
		return nil, ErrUnsafePath
	}
	return f, nil
}

func (s *Store) check(ctx context.Context, id Identity) error {
	if s == nil || s.root == "" || ctx == nil || !validIdentity(id) {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := s.openRoot()
	if err == nil {
		_ = f.Close()
	}
	return err
}

func encode(c Checkpoint) []byte {
	// All fields are fixed JSON-compatible values, so marshaling cannot fail.
	data, _ := json.Marshal(c)
	data, _ = json.Marshal(record{Checkpoint: c, Digest: artifact.Digest(sha256.Sum256(data))})
	return data
}

// Load accepts only the canonical versioned format produced by Save. Checking
// canonical bytes also rejects duplicate/omitted fields, ambiguous casing and
// shortened digest arrays. The checksum detects accidental corruption, not
// malicious modification by a writer authorized to replace both data and digest.
func (s *Store) Load(ctx context.Context, expected Identity) (Checkpoint, error) {
	if err := s.check(ctx, expected); err != nil {
		return Checkpoint{}, err
	}
	f, err := openNoFollow(filepath.Join(s.root, fileName(expected)), false)
	if err != nil {
		return Checkpoint{}, safeError(err)
	}
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return Checkpoint{}, ErrIO
	}
	if i.Size() > s.limit {
		return Checkpoint{}, ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, s.limit+1))
	if err != nil {
		return Checkpoint{}, ErrIO
	}
	if err := s.check(ctx, expected); err != nil {
		return Checkpoint{}, err
	}
	if int64(len(data)) > s.limit {
		return Checkpoint{}, ErrLimit
	}
	var r record
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&r); err != nil {
		return Checkpoint{}, ErrCorrupt
	}
	if r.Checkpoint.SchemaVersion != SchemaVersion {
		return Checkpoint{}, ErrSchema
	}
	if !bytes.Equal(data, encode(r.Checkpoint)) || validate(r.Checkpoint) != nil {
		return Checkpoint{}, ErrCorrupt
	}
	id := r.Checkpoint.Identity
	if id.ProjectID != expected.ProjectID || id.Quality != expected.Quality {
		return Checkpoint{}, ErrIdentity
	}
	if id.ProjectHash != expected.ProjectHash {
		return Checkpoint{}, ErrProjectHash
	}
	if err := ctx.Err(); err != nil {
		return Checkpoint{}, err
	}
	return r.Checkpoint, nil
}

// Save snapshots input, writes and syncs an owned same-directory staging file,
// and atomically replaces the checkpoint. Failures or observed cancellation
// before rename preserve the previous checkpoint. After rename, visibility is
// committed; parent-directory sync is best effort and cancellation does not roll
// back success. Abandoned staging files from a killed process are never loaded.
// Callers must not mutate input while Save or Clone is snapshotting it.
func (s *Store) Save(ctx context.Context, c Checkpoint) error {
	return s.save(ctx, c, os.Rename)
}

func (s *Store) save(ctx context.Context, c Checkpoint, rename func(string, string) error) error {
	if err := s.check(ctx, c.Identity); err != nil {
		return err
	}
	if err := validate(c); err != nil {
		return err
	}
	c = c.Clone()
	data := encode(c)
	if int64(len(data)) > s.limit {
		return ErrLimit
	}
	target := filepath.Join(s.root, fileName(c.Identity))
	if err := regularOrMissing(target); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(s.root, ".checkpoint-stage-*")
	if err != nil {
		return ErrIO
	}
	defer os.Remove(tmp.Name())
	n, writeErr := tmp.Write(data)
	syncErr := tmp.Sync()
	closeErr := tmp.Close()
	if writeErr != nil || n != len(data) || syncErr != nil || closeErr != nil {
		return ErrIO
	}
	if err := s.check(ctx, c.Identity); err != nil {
		return err
	}
	if err := regularOrMissing(target); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rename(tmp.Name(), target); err != nil {
		return safeError(err)
	}
	if dir, err := s.openRoot(); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func regularOrMissing(name string) error {
	i, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrIO
	}
	if !i.Mode().IsRegular() {
		return ErrUnsafePath
	}
	return nil
}

func safeError(err error) error {
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	for _, safe := range []error{context.Canceled, context.DeadlineExceeded, ErrUnsafePath, ErrUnsupported} {
		if errors.Is(err, safe) {
			return safe
		}
	}
	return ErrIO
}
