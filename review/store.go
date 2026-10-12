package review

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
	"sort"
	"strings"

	"github.com/manish96170/videofactory/domain"
)

var (
	ErrInvalid     = errors.New("review: invalid input")
	ErrConflict    = errors.New("review: feedback ID already exists")
	ErrCorrupt     = errors.New("review: corrupt feedback record")
	ErrIO          = errors.New("review: store I/O failure")
	ErrUnsafePath  = errors.New("review: unsafe store path")
	ErrLimit       = errors.New("review: store limit exceeded")
	ErrUnsupported = errors.New("review: unsupported platform")
)

const schemaVersion = 1

// StoreConfig declares an existing, exclusively controlled local directory. Root's
// ancestors may be system aliases (e.g. macOS /var); Root itself must not be a
// symlink. Cooperating stores may share it. Other processes must not modify
// records or replace directories during operations. This is not an OS sandbox.
type StoreConfig struct {
	Root           string
	MaxRecordBytes int64 // default 1 MiB; maximum 4 MiB
	MaxListRecords int   // default 10,000; maximum 100,000 per project
	MaxListBytes   int64 // default 16 MiB; maximum 64 MiB of encoded records per list
}

// Store is an append-only file-backed feedback store, safe for concurrent use
// across instances. IDs are caller supplied, unique within a project, and use
// portable ASCII letters, digits, underscores and hyphens (1..128). Filenames
// hash exact IDs to avoid case-insensitive filesystem aliases. No mutable input
// or output is retained. Filesystem operations support macOS and Linux only.
type Store struct {
	root      string
	rootInfo  os.FileInfo
	limit     int64
	listLimit int
	listBytes int64
}

type record struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Feedback      domain.ReviewFeedback `json:"feedback"`
	Digest        string                `json:"digest"`
}

func NewStore(cfg StoreConfig) (*Store, error) {
	if cfg.Root == "" || cfg.MaxRecordBytes < 0 || cfg.MaxRecordBytes > 4<<20 || cfg.MaxListRecords < 0 || cfg.MaxListRecords > 100_000 || cfg.MaxListBytes < 0 || cfg.MaxListBytes > 64<<20 {
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
	if cfg.MaxRecordBytes == 0 {
		cfg.MaxRecordBytes = 1 << 20
	}
	if cfg.MaxListRecords == 0 {
		cfg.MaxListRecords = 10_000
	}
	if cfg.MaxListBytes == 0 {
		cfg.MaxListBytes = 16 << 20
	}
	s := &Store{root: root, rootInfo: i, limit: cfg.MaxRecordBytes, listLimit: cfg.MaxListRecords, listBytes: cfg.MaxListBytes}
	f, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return s, nil
}

// Append validates against the caller's authoritative snapshot, writes and syncs
// an owned temporary file, then atomically links it to its final name without
// replacement. Duplicate IDs always return ErrConflict, even on identical retry.
// Cancellation observed before the link prevents publication; after that commit
// point success is returned. Directory sync afterward is best effort. An interrupted
// write may leave an ignored .feedback-* staging file, never a partial record.
// Feedback notes are user content: callers own their confidentiality and must not
// concurrently mutate inputs during this call. No timestamp or ID is generated.
func (s *Store) Append(ctx context.Context, p domain.Project, feedback domain.ReviewFeedback) error {
	if err := s.check(ctx); err != nil {
		return err
	}
	f := clone(feedback)
	if int64(len(f.Note)) > s.limit {
		return ErrLimit
	}
	if err := Validate(p, f); err != nil {
		return err
	}
	r := record{SchemaVersion: schemaVersion, Feedback: f}
	r.Digest = checksum(r)
	data, err := json.Marshal(r)
	if err != nil {
		return ErrInvalid
	}
	if int64(len(data)) > s.limit {
		return ErrLimit
	}
	dir, err := s.openRoot()
	if err != nil {
		return err
	}
	defer dir.Close()
	stage, err := os.CreateTemp(s.root, ".feedback-")
	if err != nil {
		return ErrIO
	}
	defer os.Remove(stage.Name())
	_, writeErr := stage.Write(data)
	syncErr := stage.Sync()
	closeErr := stage.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil {
		return ErrIO
	}
	if err := s.check(ctx); err != nil {
		return err
	}
	// Link is the atomic no-clobber commit point on supported local filesystems.
	if err := os.Link(stage.Name(), filepath.Join(s.root, fileName(f))); err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrConflict
		}
		return ErrIO
	}
	_ = dir.Sync()
	return nil
}

// List returns detached historical feedback ordered by CreatedAt ascending, then
// exact ID byte order. Records are integrity/identity/schema checked; a malformed
// matching record fails the whole listing rather than hiding feedback. The project
// may have changed since Append, so listing does not revalidate historical targets
// against current state. It is not a transaction snapshot across concurrent appends.
func (s *Store) List(ctx context.Context, projectID domain.ID) ([]domain.ReviewFeedback, error) {
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	if !handle(projectID) {
		return nil, ErrInvalid
	}
	dir, err := s.openRoot()
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	var out []domain.ReviewFeedback
	remaining := s.listBytes
	prefix := projectPrefix(projectID)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		names, readErr := dir.ReadDir(128)
		for _, name := range names {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !strings.HasPrefix(name.Name(), prefix) {
				continue
			}
			if len(out) >= s.listLimit {
				return nil, ErrLimit
			}
			f, size, err := s.read(name.Name(), projectID, remaining)
			if err != nil {
				return nil, err
			}
			out = append(out, f)
			remaining -= size
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, ErrIO
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	if err := s.check(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) read(name string, projectID domain.ID, remaining int64) (domain.ReviewFeedback, int64, error) {
	f, err := openNoFollow(filepath.Join(s.root, name), false)
	if err != nil {
		return domain.ReviewFeedback{}, 0, err
	}
	defer f.Close()
	i, err := f.Stat()
	if err != nil {
		return domain.ReviewFeedback{}, 0, ErrIO
	}
	limit := min(s.limit, remaining)
	if i.Size() > limit {
		return domain.ReviewFeedback{}, 0, ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return domain.ReviewFeedback{}, 0, ErrIO
	}
	if int64(len(data)) > limit {
		return domain.ReviewFeedback{}, 0, ErrLimit
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var r record
	if d.Decode(&r) != nil || d.Decode(new(any)) != io.EOF || r.SchemaVersion != schemaVersion || r.Feedback.ProjectID != projectID || validateFields(r.Feedback) != nil || fileName(r.Feedback) != name || checksum(r) != r.Digest {
		return domain.ReviewFeedback{}, 0, ErrCorrupt
	}
	return clone(r.Feedback), int64(len(data)), nil
}

func checksum(r record) string {
	r.Digest = ""
	data, _ := json.Marshal(r) // fields were validated before encoding
	return digest(data)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func projectPrefix(id domain.ID) string { return "v1-" + digest([]byte(id)) + "-" }
func fileName(f domain.ReviewFeedback) string {
	return projectPrefix(f.ProjectID) + digest([]byte(f.ID)) + ".json"
}

func (s *Store) check(ctx context.Context) error {
	if ctx == nil || s == nil || s.root == "" {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	i, err := os.Lstat(s.root)
	if err != nil {
		return ErrIO
	}
	if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || !os.SameFile(i, s.rootInfo) {
		return ErrUnsafePath
	}
	return nil
}

func (s *Store) openRoot() (*os.File, error) {
	if err := s.check(context.Background()); err != nil {
		return nil, err
	}
	f, err := openNoFollow(s.root, true)
	if err != nil {
		return nil, err
	}
	i, err := f.Stat()
	if err != nil || !os.SameFile(i, s.rootInfo) {
		_ = f.Close()
		return nil, ErrUnsafePath
	}
	return f, nil
}
