package project

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestMarshalIsDeterministic(t *testing.T) {
	p := validProject(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	a, err := Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	b, err := Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(a) != string(b) {
		t.Fatalf("expected identical bytes from repeated Marshal calls")
	}
}

func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	p := validProject(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	data, err := Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	decoded, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.ID != p.ID || decoded.Name != p.Name {
		t.Fatalf("round trip mismatch: got %+v", decoded)
	}
	if decoded.Plan == nil || len(decoded.Plan.Shots) != len(p.Plan.Shots) {
		t.Fatalf("round trip mismatch on plan: got %+v", decoded.Plan)
	}
	if decoded.Timeline == nil || len(decoded.Timeline.Entries) != len(p.Timeline.Entries) {
		t.Fatalf("round trip mismatch on timeline: got %+v", decoded.Timeline)
	}
	if len(decoded.Assets) != len(p.Assets) {
		t.Fatalf("round trip mismatch on assets: got %+v", decoded.Assets)
	}
}

func TestUnmarshalRejectsCorruptJSON(t *testing.T) {
	_, err := Unmarshal([]byte("{not json"))
	if err == nil {
		t.Fatalf("expected error for corrupt JSON")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected error to wrap ErrCorrupt, got %v", err)
	}
}

func TestUnmarshalRejectsUnsupportedSchemaVersion(t *testing.T) {
	data, err := json.Marshal(envelope{SchemaVersion: CurrentSchemaVersion + 1, Project: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	_, err = Unmarshal(data)
	if err == nil {
		t.Fatalf("expected error for unsupported schema version")
	}
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("expected error to wrap ErrUnsupportedSchemaVersion, got %v", err)
	}
}

func TestFileStoreSaveLoadRoundTrip(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	p := validProject(time.Now())
	ctx := context.Background()
	if err := store.Save(ctx, p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := store.Load(ctx, p.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.ID != p.ID || loaded.Name != p.Name {
		t.Fatalf("loaded project mismatch: got %+v", loaded)
	}
	if loaded.Timeline == nil || len(loaded.Timeline.Entries) != len(p.Timeline.Entries) {
		t.Fatalf("loaded timeline mismatch: got %+v", loaded.Timeline)
	}
	if len(loaded.Assets) != len(p.Assets) || loaded.Assets[0].URI != p.Assets[0].URI {
		t.Fatalf("loaded assets mismatch: got %+v", loaded.Assets)
	}
}

// TestFileStoreModifyAndResave exercises the full "created, loaded,
// modified, saved" lifecycle the Phase 01 Required Result describes,
// including timeline state.
func TestFileStoreModifyAndResave(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctx := context.Background()

	p := validProject(time.Now())
	if err := store.Save(ctx, p); err != nil {
		t.Fatalf("initial Save: %v", err)
	}

	loaded, err := store.Load(ctx, p.ID)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	newAsset := domain.Asset{ID: NewID(), Kind: domain.AssetKindImage, URI: "assets/new-frame.png"}
	loaded.Assets = append(loaded.Assets, newAsset)
	loaded.UpdatedAt = time.Now()

	if err := store.Save(ctx, loaded); err != nil {
		t.Fatalf("re-Save: %v", err)
	}

	reloaded, err := store.Load(ctx, p.ID)
	if err != nil {
		t.Fatalf("re-Load: %v", err)
	}
	if len(reloaded.Assets) != len(p.Assets)+1 {
		t.Fatalf("expected modification to persist, got %d assets", len(reloaded.Assets))
	}
}

func TestFileStoreLoadNotFound(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_, err = store.Load(context.Background(), NewID())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load() error = %v, want to wrap ErrNotFound", err)
	}
}

func TestFileStoreSaveRejectsInvalidProject(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	err = store.Save(context.Background(), domain.Project{})
	if err == nil {
		t.Fatalf("expected Save to reject an invalid (empty) project")
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("expected error to wrap ErrInvalid, got %v", err)
	}
}

func TestFileStoreLoadRejectsPathTraversalID(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_, err = store.Load(context.Background(), domain.ID("../../etc/passwd"))
	if err == nil {
		t.Fatalf("expected Load to reject a path-traversal-shaped ID")
	}
}

func TestFileStoreSaveRejectsPathTraversalID(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	p := validProject(time.Now())
	p.ID = domain.ID("../escape")
	p.Plan.ProjectID = p.ID
	p.Timeline.ProjectID = p.ID

	if err := store.Save(context.Background(), p); err == nil {
		t.Fatalf("expected Save to reject a path-traversal-shaped ID")
	}

	// Confirm nothing escaped the store directory.
	if _, statErr := os.Stat(filepath.Join(dir, "..", "escape.json")); statErr == nil {
		t.Fatalf("a file was written outside the store directory")
	}
}

func TestFileStoreLoadRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	id := NewID()
	path := filepath.Join(dir, string(id)+".json")
	if err := os.WriteFile(path, []byte("not json at all"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err = store.Load(context.Background(), id)
	if err == nil {
		t.Fatalf("expected Load to reject corrupt file contents")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("expected error to wrap ErrCorrupt, got %v", err)
	}
}

// TestFileStoreLoadRejectsIdentityMismatch covers a file that is
// well-formed and even individually valid, but describes a different
// project than the filename under which it is stored (e.g. because it
// was copied or renamed by hand).
func TestFileStoreLoadRejectsIdentityMismatch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	other := validProject(time.Now())
	data, err := Marshal(other)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	wrongID := NewID()
	path := filepath.Join(dir, string(wrongID)+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, err = store.Load(context.Background(), wrongID)
	if err == nil {
		t.Fatalf("expected Load to reject a file whose project ID does not match the requested ID")
	}
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("expected error to wrap ErrIdentityMismatch, got %v", err)
	}
}

func TestFileStoreSaveLeavesNoTempFilesBehind(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	p := validProject(time.Now())
	if err := store.Save(context.Background(), p); err != nil {
		t.Fatalf("Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("found leftover temp file after Save: %s", e.Name())
		}
	}
}

// TestFileStoreInvalidSaveLeavesNoFile confirms that rejecting an
// invalid project never creates a (partial or otherwise) file on disk.
func TestFileStoreInvalidSaveLeavesNoFile(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	p := validProject(time.Now())
	p.Name = "" // now invalid

	if err := store.Save(context.Background(), p); err == nil {
		t.Fatalf("expected Save to reject an invalid project")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no files after a rejected Save, found %v", entries)
	}
}

func TestFileStoreRespectsCancellation(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := store.Save(ctx, validProject(time.Now())); err == nil {
		t.Fatalf("expected Save to respect a cancelled context")
	}
	if _, err := store.Load(ctx, NewID()); err == nil {
		t.Fatalf("expected Load to respect a cancelled context")
	}
}

// TestFileStoreConcurrentSaveLoadDistinctProjects exercises FileStore
// under -race with many goroutines saving and loading distinct
// projects concurrently, proving the documented "safe for concurrent
// use" contract for the common (distinct-ID) case.
func TestFileStoreConcurrentSaveLoadDistinctProjects(t *testing.T) {
	store, err := NewFileStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	ctx := context.Background()

	const n = 50
	var wg sync.WaitGroup
	errs := make(chan error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := validProject(time.Now())
			if err := store.Save(ctx, p); err != nil {
				errs <- err
				return
			}
			loaded, err := store.Load(ctx, p.ID)
			if err != nil {
				errs <- err
				return
			}
			if loaded.ID != p.ID {
				errs <- errors.New("loaded ID mismatch")
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent save/load failed: %v", err)
	}
}

var _ Store = &FileStore{}
