package checkpoint

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
)

func publish(t *testing.T, store *artifact.Store, root string, c Checkpoint, shot Shot) artifact.Entry {
	t.Helper()
	write(t, filepath.Join(root, "source"), []byte("already-rendered-"+string(shot.ShotID)))
	e, err := store.Publish(context.Background(), artifact.PublishRequest{
		Identity:  artifact.Identity{ProjectID: c.Identity.ProjectID, ShotID: shot.ShotID, Quality: c.Identity.Quality, Key: shot.Key},
		MediaPath: "source",
		Manifest:  domain.RenderManifest{ID: "manifest", ShotID: shot.ShotID, MediaPath: "source", Elements: []domain.VisualElement{{ID: "element", ShotID: shot.ShotID, Type: "Text", End: time.Second}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestResumeRevalidatesEveryArtifactAndPreservesOrder(t *testing.T) {
	s, c := fixture(t)
	root := t.TempDir()
	a, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	// Put pending work before completed work to pin the absence of a skip cursor.
	c.Shots = nil
	names := []domain.ID{"incomplete", "ready", "missing", "partial", "corrupt", "invalidated", "wrong-digest"}
	for _, name := range names {
		shot := Shot{ShotID: name, Key: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", State: Complete, InvalidationGeneration: 1, ArtifactDigest: digest("missing-entry")}
		c.Shots = append(c.Shots, shot)
	}
	for i := range c.Shots {
		if c.Shots[i].ShotID == "missing" {
			continue
		}
		e := publish(t, a, root, c, c.Shots[i])
		c.Shots[i].ArtifactDigest = e.Digest
		switch c.Shots[i].ShotID {
		case "incomplete":
			c.Shots[i].State = Pending
		case "partial":
			if err := os.Remove(filepath.Join(root, e.ManifestFile.Name)); err != nil {
				t.Fatal(err)
			}
		case "corrupt":
			write(t, filepath.Join(root, e.Media.Name), []byte("corrupted-media"))
		case "invalidated":
			c.Shots[i].State = Invalidated
			c.Shots[i].InvalidationGeneration = c.Generation
		case "wrong-digest":
			c.Shots[i].ArtifactDigest = digest("different-completion-record")
		}
	}
	save(t, s, c)
	got, err := s.Resume(context.Background(), c.Identity, a)
	if err != nil {
		t.Fatal(err)
	}
	wantReasons := []Reason{ReasonIncomplete, ReasonVerified, ReasonUnavailable, ReasonUnavailable, ReasonUnavailable, ReasonInvalidated, ReasonDigestMismatch}
	if got.Ready != 1 || len(got.Shots) != len(names) {
		t.Fatalf("progress: %+v", got)
	}
	for i, shot := range got.Shots {
		state := ResumePending
		if i == 1 {
			state = ResumeReady
		}
		if shot.ShotID != names[i] || shot.State != state || shot.Reason != wantReasons[i] {
			t.Fatalf("shot %d: %+v", i, shot)
		}
	}
	if !reflect.DeepEqual(got.Checkpoint, c) {
		t.Fatal("resume mutated persisted snapshot")
	}
	got.Checkpoint.Shots[1].ArtifactDigest[0]++
	got.Shots[1].State = ResumePending
	if !reflect.DeepEqual(load(t, s, c.Identity), c) {
		t.Fatal("resume result aliases storage")
	}
	again, err := s.Resume(context.Background(), c.Identity, a)
	if err != nil || again.Ready != 1 {
		t.Fatalf("repeat resume: %+v %v", again, err)
	}
}

func TestResumeFailsClosedOnCheckpointOrStoreFailure(t *testing.T) {
	s, c := fixture(t)
	root := t.TempDir()
	a, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	e := publish(t, a, root, c, c.Shots[0])
	c.Shots[0].ArtifactDigest = e.Digest
	save(t, s, c)
	expected := c.Identity
	expected.ProjectHash = digest("edited-project")
	if _, err := s.Resume(context.Background(), expected, a); !errors.Is(err, ErrProjectHash) {
		t.Fatal(err)
	}
	if _, err := s.Resume(context.Background(), c.Identity, nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := s.Resume(nil, c.Identity, a); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Resume(ctx, c.Identity, a); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := os.Rename(root, root+"-old"); err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root + "-old")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := s.Resume(context.Background(), c.Identity, a)
	if !errors.Is(err, ErrArtifact) || !reflect.DeepEqual(got, ResumeResult{}) {
		t.Fatalf("unavailable store returned partial readiness: %+v %v", got, err)
	}
	write(t, filepath.Join(s.root, fileName(c.Identity)), []byte("{"))
	if _, err := s.Resume(context.Background(), c.Identity, a); !errors.Is(err, ErrCorrupt) {
		t.Fatal(err)
	}
}
