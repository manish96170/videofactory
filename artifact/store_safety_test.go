package artifact

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestStoreSymlinkEscapes(t *testing.T) {
	for _, source := range []string{"leaf outside", "directory outside", "leaf inside"} {
		t.Run(source, func(t *testing.T) {
			s, req := storeFixture(t)
			outside := t.TempDir()
			writeTestFile(t, filepath.Join(outside, "shot.mp4"), []byte("outside sentinel"))
			var name, target string
			switch source {
			case "leaf outside":
				name, target = "linked.mp4", filepath.Join(outside, "shot.mp4")
			case "directory outside":
				name, target = "linked", outside
			case "leaf inside":
				name, target = "linked.mp4", filepath.Join(s.root, req.MediaPath)
			}
			if err := os.Symlink(target, filepath.Join(s.root, name)); err != nil {
				t.Fatal(err)
			}
			req.MediaPath = name
			if source == "directory outside" {
				req.MediaPath += "/shot.mp4"
			}
			req.Manifest.MediaPath = req.MediaPath
			if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("symlink source: %v", err)
			}
			mustMiss(t, s, req.Identity)
			data, err := os.ReadFile(filepath.Join(outside, "shot.mp4"))
			if err != nil || string(data) != "outside sentinel" {
				t.Fatal("outside file touched")
			}
		})
	}
	for _, file := range []string{"media", "render.json", "entry.json"} {
		t.Run("published "+file, func(t *testing.T) {
			s, req := storeFixture(t)
			mustPublish(t, s, req)
			name := filepath.Join(s.root, entryName(req.Identity), file)
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), file)
			writeTestFile(t, outside, data)
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(outside, name); err != nil {
				t.Fatal(err)
			}
			mustMiss(t, s, req.Identity)
			if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrEntryConflict) {
				t.Fatalf("damaged entry replaced: %v", err)
			}
			if got, err := os.ReadFile(outside); err != nil || string(got) != string(data) {
				t.Fatal("outside data changed")
			}
		})
	}
	t.Run("cache parent outside", func(t *testing.T) {
		s, req := storeFixture(t)
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(s.root, "v1")); err != nil {
			t.Fatal(err)
		}
		mustMiss(t, s, req.Identity)
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("parent escape: %v", err)
		}
		items, err := os.ReadDir(outside)
		if err != nil || len(items) != 0 {
			t.Fatal("created files outside root")
		}
	})
	t.Run("whole published entry outside", func(t *testing.T) {
		s, req := storeFixture(t)
		mustPublish(t, s, req)
		name := filepath.Join(s.root, entryName(req.Identity))
		moved := filepath.Join(t.TempDir(), "moved")
		if err := os.Rename(name, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(moved, name); err != nil {
			t.Fatal(err)
		}
		mustMiss(t, s, req.Identity)
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("entry symlink: %v", err)
		}
	})
	t.Run("configured root symlink", func(t *testing.T) {
		name := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(t.TempDir(), name); err != nil {
			t.Fatal(err)
		}
		if _, err := NewStore(StoreConfig{Root: name}); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("root symlink: %v", err)
		}
		if _, err := NewStore(StoreConfig{Root: name + string(filepath.Separator)}); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("trailing slash root symlink: %v", err)
		}
	})
	t.Run("replaced root", func(t *testing.T) {
		s, req := storeFixture(t)
		mustPublish(t, s, req)
		moved := s.root + "-moved"
		if err := os.Rename(s.root, moved); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(moved) })
		if err := os.Mkdir(s.root, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.Lookup(context.Background(), req.Identity); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("replaced root: %v", err)
		}
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("replaced root publish: %v", err)
		}
	})
}

// Hooks on a real context's Err boundary let tests deterministically cancel or
// inject a competing publisher after staging, without sleeps or production seams.
type storeHookContext struct {
	context.Context
	hook func()
}

func (c storeHookContext) Err() error { c.hook(); return c.Context.Err() }

func stageComplete(s *Store) bool {
	complete := false
	_ = filepath.WalkDir(s.root, func(name string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "entry.json" && strings.HasPrefix(filepath.Base(filepath.Dir(name)), ".stage-") {
			complete = true
		}
		return err
	})
	return complete
}

func TestStoreCancellation(t *testing.T) {
	t.Run("before work", func(t *testing.T) {
		s, req := storeFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := s.Publish(ctx, req); !errors.Is(err, context.Canceled) {
			t.Fatalf("publish: %v", err)
		}
		if _, _, err := s.Lookup(ctx, req.Identity); !errors.Is(err, context.Canceled) {
			t.Fatalf("lookup: %v", err)
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
	})
	t.Run("after all staged files before rename", func(t *testing.T) {
		s, req := storeFixture(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		triggered := false
		hooked := storeHookContext{ctx, func() {
			if stageComplete(s) {
				triggered = true
				cancel()
			}
		}}
		if _, err := s.Publish(hooked, req); !errors.Is(err, context.Canceled) {
			t.Fatalf("publish: %v", err)
		}
		if !triggered {
			t.Fatal("did not reach pre-rename boundary")
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
		// A canceled staging attempt does not poison a later publication.
		mustPublish(t, s, req)
		mustHit(t, s, req.Identity)
	})
	t.Run("during media copy", func(t *testing.T) {
		s, req := storeFixture(t)
		writeTestFile(t, filepath.Join(s.root, req.MediaPath), []byte(strings.Repeat("x", 256<<10)))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		triggered := false
		hooked := storeHookContext{ctx, func() {
			_ = filepath.WalkDir(s.root, func(name string, d os.DirEntry, err error) error {
				if err == nil && d.Name() == "media" && strings.HasPrefix(filepath.Base(filepath.Dir(name)), ".stage-") {
					if info, err := d.Info(); err == nil && info.Size() > 0 {
						triggered = true
						cancel()
					}
				}
				return err
			})
		}}
		if _, err := s.Publish(hooked, req); !errors.Is(err, context.Canceled) {
			t.Fatalf("copy cancellation: %v", err)
		}
		if !triggered {
			t.Fatal("did not cancel mid-copy")
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
	})
	t.Run("canceled retry preserves published entry", func(t *testing.T) {
		s, req := storeFixture(t)
		want := mustPublish(t, s, req)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := s.Publish(ctx, req); !errors.Is(err, context.Canceled) {
			t.Fatalf("retry: %v", err)
		}
		if got := mustHit(t, s, req.Identity); !reflect.DeepEqual(want, got) {
			t.Fatal("published entry changed")
		}
	})
}

func TestStoreAtomicFailurePreservation(t *testing.T) {
	t.Run("rename failure leaves target intact", func(t *testing.T) {
		s, req := storeFixture(t)
		injected := false
		hooked := storeHookContext{context.Background(), func() {
			if injected || !stageComplete(s) {
				return
			}
			injected = true
			if err := s.makeDirs(entryName(req.Identity)); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(s.root, entryName(req.Identity), "owner-data"), []byte("preserve"))
		}}
		if _, err := s.Publish(hooked, req); !errors.Is(err, ErrEntryConflict) {
			t.Fatalf("rename failure: %v", err)
		}
		if !injected {
			t.Fatal("failure was not injected")
		}
		data, err := os.ReadFile(filepath.Join(s.root, entryName(req.Identity), "owner-data"))
		if err != nil || string(data) != "preserve" {
			t.Fatal("existing data removed")
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
	})
	t.Run("competing valid publication at commit boundary wins", func(t *testing.T) {
		s, req := storeFixture(t)
		injected := false
		var winner Entry
		hooked := storeHookContext{context.Background(), func() {
			if injected || !stageComplete(s) {
				return
			}
			injected = true
			second := req
			second.MediaPath = "sources/winner.mp4"
			second.Manifest.MediaPath = second.MediaPath
			writeTestFile(t, filepath.Join(s.root, second.MediaPath), []byte("winner bytes differ"))
			winner = mustPublish(t, s, second)
		}}
		got, err := s.Publish(hooked, req)
		if err != nil {
			t.Fatal(err)
		}
		if !injected || !reflect.DeepEqual(got, winner) {
			t.Fatal("first successful publication not preserved")
		}
		if got := mustHit(t, s, req.Identity); !reflect.DeepEqual(got, winner) {
			t.Fatal("winner changed on lookup")
		}
		assertNoStages(t, s)
	})
	t.Run("corrupt entry never replaced", func(t *testing.T) {
		s, req := storeFixture(t)
		mustPublish(t, s, req)
		name := filepath.Join(s.root, entryName(req.Identity), "entry.json")
		writeTestFile(t, name, []byte("damaged but owner retained"))
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrEntryConflict) {
			t.Fatalf("corrupt retry: %v", err)
		}
		got, err := os.ReadFile(name)
		if err != nil || string(got) != "damaged but owner retained" {
			t.Fatal("corrupt entry deleted/replaced")
		}
		assertNoStages(t, s)
	})
	t.Run("source error never creates a hit", func(t *testing.T) {
		s, req := storeFixture(t)
		if err := os.Remove(filepath.Join(s.root, req.MediaPath)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrStoreIO) {
			t.Fatalf("missing source: %v", err)
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
	})
	t.Run("media limit cleans staging", func(t *testing.T) {
		s, req := storeFixture(t)
		s.mediaLimit = 4
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrStoreLimit) {
			t.Fatalf("media limit: %v", err)
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
	})
	t.Run("metadata limit", func(t *testing.T) {
		s, req := storeFixture(t)
		s.metadataLimit = 4
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrStoreLimit) {
			t.Fatalf("metadata limit: %v", err)
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
	})
	t.Run("completed metadata exceeds limit after media copied", func(t *testing.T) {
		s, req := storeFixture(t)
		// Input fits, but the fully bound record additionally contains long logical
		// names and digests. This exercises cleanup after successful media staging.
		data, err := json.Marshal(req)
		if err != nil {
			t.Fatal(err)
		}
		s.metadataLimit = int64(len(data))
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrStoreLimit) {
			t.Fatalf("bound metadata limit: %v", err)
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
	})
	t.Run("empty media", func(t *testing.T) {
		s, req := storeFixture(t)
		writeTestFile(t, filepath.Join(s.root, req.MediaPath), nil)
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrStoreInput) {
			t.Fatalf("empty media: %v", err)
		}
		mustMiss(t, s, req.Identity)
		assertNoStages(t, s)
	})
	t.Run("directory source", func(t *testing.T) {
		s, req := storeFixture(t)
		req.MediaPath = "sources"
		req.Manifest.MediaPath = req.MediaPath
		if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("directory: %v", err)
		}
		mustMiss(t, s, req.Identity)
	})
}
