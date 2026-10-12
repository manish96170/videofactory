package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
)

func fixture(t *testing.T) (*Store, Checkpoint) {
	t.Helper()
	s, err := NewStore(StoreConfig{Root: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return s, Checkpoint{
		SchemaVersion: SchemaVersion,
		Identity:      Identity{ProjectID: "project-1", ProjectHash: digest("project-content"), Quality: domain.QualityDraft},
		Generation:    2,
		Shots: []Shot{
			{ShotID: "shot-z", Key: artifact.Key(strings.Repeat("a", 64)), State: Complete, ArtifactDigest: digest("artifact"), InvalidationGeneration: 1},
			{ShotID: "shot-a", Key: artifact.Key(strings.Repeat("b", 64)), State: Pending, InvalidationGeneration: 2},
		},
	}
}

func digest(s string) artifact.Digest { return artifact.Digest(sha256.Sum256([]byte(s))) }

func save(t *testing.T, s *Store, c Checkpoint) {
	t.Helper()
	if err := s.Save(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func load(t *testing.T, s *Store, id Identity) Checkpoint {
	t.Helper()
	c, err := s.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func write(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDeterministicRoundTripAndIsolation(t *testing.T) {
	s, c := fixture(t)
	want := c.Clone()
	save(t, s, c)
	name := filepath.Join(s.root, fileName(c.Identity))
	before := read(t, name)
	save(t, s, c)
	if !bytes.Equal(before, read(t, name)) {
		t.Fatal("identical snapshots changed persisted bytes")
	}
	if bytes.Contains(before, []byte(s.root)) {
		t.Fatal("persisted root path")
	}
	c.Shots[0].ShotID = "caller-mutated"
	if got := load(t, s, want.Identity); !reflect.DeepEqual(got, want) {
		t.Fatalf("caller alias or order changed: %+v", got)
	}
	got := load(t, s, want.Identity)
	got.Shots[0].ArtifactDigest[0]++
	got.Shots[1].State = Invalidated
	clone := want.Clone()
	clone.Shots[0].ShotID = "clone-mutated"
	s2, err := NewStore(StoreConfig{Root: s.root})
	if err != nil {
		t.Fatal(err)
	}
	if got := load(t, s2, want.Identity); !reflect.DeepEqual(got, want) {
		t.Fatalf("load/clone alias or restart lost data: %+v", got)
	}
	if want.Shots[0].ShotID != "shot-z" {
		t.Fatal("Clone aliased its input")
	}
	// Quality and case-sensitive project identities have independent filenames.
	for _, id := range []Identity{
		{ProjectID: "Project-1", ProjectHash: want.Identity.ProjectHash, Quality: domain.QualityDraft},
		{ProjectID: want.Identity.ProjectID, ProjectHash: want.Identity.ProjectHash, Quality: domain.QualityFinal},
	} {
		other := want.Clone()
		other.Identity = id
		other.Shots = nil
		save(t, s, other)
		if !reflect.DeepEqual(load(t, s, want.Identity), want) {
			t.Fatal("identity alias overwrote previous checkpoint")
		}
	}
}

func TestCorruptionSchemaAndIdentityFailures(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(Checkpoint, []byte) []byte
		want   error
	}{
		"truncated":     {func(_ Checkpoint, b []byte) []byte { return b[:len(b)/2] }, ErrCorrupt},
		"checksum":      {func(_ Checkpoint, b []byte) []byte { return bytes.Replace(b, []byte("shot-z"), []byte("shot-x"), 1) }, ErrCorrupt},
		"schema":        {func(c Checkpoint, _ []byte) []byte { c.SchemaVersion++; return encode(c) }, ErrSchema},
		"project":       {func(c Checkpoint, _ []byte) []byte { c.Identity.ProjectID = "other"; return encode(c) }, ErrIdentity},
		"quality":       {func(c Checkpoint, _ []byte) []byte { c.Identity.Quality = domain.QualityFinal; return encode(c) }, ErrIdentity},
		"project hash":  {func(c Checkpoint, _ []byte) []byte { c.Identity.ProjectHash = digest("changed"); return encode(c) }, ErrProjectHash},
		"unsafe shot":   {func(c Checkpoint, _ []byte) []byte { c.Shots[0].ShotID = "../outside"; return encode(c) }, ErrCorrupt},
		"unknown field": {func(_ Checkpoint, b []byte) []byte { return append([]byte(`{"prompt":"never persist",`), b[1:]...) }, ErrCorrupt},
		"trailing JSON": {func(_ Checkpoint, b []byte) []byte { return append(b, []byte(`{}`)...) }, ErrCorrupt},
		"duplicate schema": {func(_ Checkpoint, b []byte) []byte {
			return bytes.Replace(b, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"schemaVersion":1`), 1)
		}, ErrCorrupt},
		"omitted generation": {func(_ Checkpoint, b []byte) []byte { return bytes.Replace(b, []byte(`"generation":2,`), nil, 1) }, ErrCorrupt},
	} {
		t.Run(name, func(t *testing.T) {
			s, c := fixture(t)
			save(t, s, c)
			path := filepath.Join(s.root, fileName(c.Identity))
			write(t, path, tc.mutate(c, read(t, path)))
			got, err := s.Load(context.Background(), c.Identity)
			if !errors.Is(err, tc.want) || !reflect.DeepEqual(got, Checkpoint{}) {
				t.Fatalf("got %+v, %v; want %v", got, err, tc.want)
			}
		})
	}
	s, c := fixture(t)
	if _, err := s.Load(context.Background(), c.Identity); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	save(t, s, c)
	expected := c.Identity
	expected.ProjectHash = digest("authoritative-project-edited")
	if _, err := s.Load(context.Background(), expected); !errors.Is(err, ErrProjectHash) {
		t.Fatal(err)
	}
}

func TestInvalidSnapshotsPreservePrevious(t *testing.T) {
	for name, mutate := range map[string]func(*Checkpoint){
		"project handle":                 func(c *Checkpoint) { c.Identity.ProjectID = "../private" },
		"project hash":                   func(c *Checkpoint) { c.Identity.ProjectHash = artifact.Digest{} },
		"quality":                        func(c *Checkpoint) { c.Identity.Quality = "unknown" },
		"shot handle":                    func(c *Checkpoint) { c.Shots[0].ShotID = "/private" },
		"duplicate shot":                 func(c *Checkpoint) { c.Shots[1].ShotID = c.Shots[0].ShotID },
		"key":                            func(c *Checkpoint) { c.Shots[0].Key = "raw-prompt" },
		"uppercase digest":               func(c *Checkpoint) { c.Shots[0].Key = artifact.Key(strings.Repeat("A", 64)) },
		"unverified completion":          func(c *Checkpoint) { c.Shots[0].ArtifactDigest = artifact.Digest{} },
		"state":                          func(c *Checkpoint) { c.Shots[0].State = "running-provider-process" },
		"future generation":              func(c *Checkpoint) { c.Shots[0].InvalidationGeneration = c.Generation + 1 },
		"invalidated without generation": func(c *Checkpoint) { c.Shots[0].State = Invalidated; c.Shots[0].InvalidationGeneration = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			s, c := fixture(t)
			save(t, s, c)
			bad := c.Clone()
			mutate(&bad)
			if err := s.Save(context.Background(), bad); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid input accepted: %v", err)
			}
			if !reflect.DeepEqual(load(t, s, c.Identity), c) {
				t.Fatal("invalid save replaced previous data")
			}
		})
	}
}

type cancelOnStage struct {
	context.Context
	root   string
	cancel context.CancelFunc
}

func (c cancelOnStage) Err() error {
	files, _ := filepath.Glob(filepath.Join(c.root, ".checkpoint-stage-*"))
	if len(files) > 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestInterruptedWritePreservesPrevious(t *testing.T) {
	s, c := fixture(t)
	save(t, s, c)
	updated := c.Clone()
	updated.Generation++
	target := filepath.Join(s.root, fileName(c.Identity))
	previous := read(t, target)
	// Fail at the commit boundary, after complete staged bytes have been synced.
	err := s.save(context.Background(), updated, func(stage, dst string) error {
		if dst != target || !bytes.Equal(read(t, stage), encode(updated)) || !bytes.Equal(read(t, dst), previous) {
			t.Fatal("previous snapshot changed before atomic commit")
		}
		return errors.New("private diagnostic must not escape")
	})
	if !errors.Is(err, ErrIO) || strings.Contains(err.Error(), "private diagnostic") {
		t.Fatalf("unsafe or missing error: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err = s.Save(cancelOnStage{Context: ctx, root: s.root, cancel: cancel}, updated)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("staged cancellation: %v", err)
	}
	if !bytes.Equal(previous, read(t, target)) {
		t.Fatal("interruption damaged checkpoint")
	}
	stages, _ := filepath.Glob(filepath.Join(s.root, ".checkpoint-stage-*"))
	if len(stages) != 0 {
		t.Fatal("failed save leaked owned stage")
	}
	// A process killed before rename may leave arbitrary incomplete staging bytes.
	write(t, filepath.Join(s.root, ".checkpoint-stage-orphan"), []byte("{"))
	if !reflect.DeepEqual(load(t, s, c.Identity), c) {
		t.Fatal("loaded abandoned staging file")
	}
	save(t, s, updated)
	if !reflect.DeepEqual(load(t, s, c.Identity), updated) {
		t.Fatal("subsequent atomic replacement failed")
	}
}

func TestLimitsCancellationAndSafeFilesystem(t *testing.T) {
	s, c := fixture(t)
	if err := s.Save(nil, c); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Load(nil, c.Identity); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Save(ctx, c); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Load(ctx, c.Identity); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var zero Store
	if err := zero.Save(context.Background(), c); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	limited, err := NewStore(StoreConfig{Root: s.root, MaxBytes: 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := limited.Save(context.Background(), c); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	save(t, s, c)
	if _, err := limited.Load(context.Background(), c.Identity); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	path := filepath.Join(s.root, fileName(c.Identity))
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	write(t, outside, []byte("untouched"))
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if err := s.Save(context.Background(), c); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
	if _, err := s.Load(context.Background(), c.Identity); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
	if string(read(t, outside)) != "untouched" {
		t.Fatal("escaped root")
	}
	link := filepath.Join(t.TempDir(), "root-link")
	if err := os.Symlink(s.root, link); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(StoreConfig{Root: link}); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
	if err := os.Rename(s.root, s.root+"-old"); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(s.root + "-old")
	if err := os.Mkdir(s.root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(context.Background(), c.Identity); !errors.Is(err, ErrUnsafePath) {
		t.Fatal(err)
	}
}

func TestConcurrentAtomicSnapshots(t *testing.T) {
	s, c := fixture(t)
	save(t, s, c)
	s2, err := NewStore(StoreConfig{Root: s.root})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			writer := s
			if n%2 == 0 {
				writer = s2
			}
			for j := 0; j < 12; j++ {
				next := c.Clone()
				next.Generation = uint64(10 + n*12 + j)
				next.Shots[1].InvalidationGeneration = next.Generation
				if err := writer.Save(context.Background(), next); err != nil {
					t.Error(err)
					return
				}
				got, err := s.Load(context.Background(), c.Identity)
				if err != nil {
					t.Error(err)
					return
				}
				if got.Generation != got.Shots[1].InvalidationGeneration {
					t.Error("torn snapshot")
					return
				}
			}
		}(n)
	}
	wg.Wait()
}

func TestPersistedShapeIsNarrow(t *testing.T) {
	_, c := fixture(t)
	var raw map[string]json.RawMessage
	data, _ := json.Marshal(c)
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 4 {
		t.Fatalf("unexpected checkpoint fields: %v", raw)
	}
	for _, key := range []string{"schemaVersion", "identity", "generation", "shots"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("missing %s", key)
		}
	}
}
