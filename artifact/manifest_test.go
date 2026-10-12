package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func bindingFixture(t *testing.T) (*Store, PublishRequest, Entry) {
	t.Helper()
	s, req := storeFixture(t)
	e := mustPublish(t, s, req)
	req.Manifest.Elements[0].ID = "new-element"
	return s, req, e
}

func mustBind(t *testing.T, s *Store, req PublishRequest) ManifestBinding {
	t.Helper()
	b, err := s.BindManifest(context.Background(), req.Identity, req.Manifest.Elements)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestManifestBindingRejectsValidNonIDChanges(t *testing.T) {
	for _, mode := range []string{"bounds", "bounds-removed", "start", "end", "type", "properties", "nested", "missing", "extra"} {
		t.Run(mode, func(t *testing.T) {
			s, req, original := bindingFixture(t)
			e := &req.Manifest.Elements[0]
			switch mode {
			case "bounds":
				e.Bounds.Width = 0.5
			case "bounds-removed":
				e.Bounds = nil
			case "start":
				e.Start++
			case "end":
				e.End--
			case "type":
				e.Type = "Image"
			case "properties":
				e.EditableProperties["text"] = "changed"
			case "nested":
				e.EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "changed"
			case "missing":
				req.Manifest.Elements = nil
			case "extra":
				extra := *e
				extra.ID = "extra"
				req.Manifest.Elements = append(req.Manifest.Elements, extra)
			}
			if _, err := ManifestDigest(req.Identity, req.Manifest.Elements); err != nil {
				t.Fatalf("regression must use valid metadata: %v", err)
			}
			before, _ := json.Marshal(req.Manifest)
			got, err := s.BindManifest(context.Background(), req.Identity, req.Manifest.Elements)
			if !errors.Is(err, ErrRerenderRequired) || !strings.Contains(err.Error(), "rerender required") || !reflect.DeepEqual(got, ManifestBinding{}) {
				t.Fatalf("non-ID change bound: %+v %v", got, err)
			}
			if got, hit, err := s.LookupManifest(context.Background(), req.Identity, req.Manifest.Elements); hit || !reflect.DeepEqual(got, ManifestBinding{}) || !errors.Is(err, ErrRerenderRequired) {
				t.Fatalf("non-ID lookup must require rerender: %+v %v %v", got, hit, err)
			}
			if _, err := os.Lstat(filepath.Join(s.root, "bindings")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("incompatible metadata wrote binding files")
			}
			after, _ := json.Marshal(req.Manifest)
			if string(before) != string(after) || !reflect.DeepEqual(mustHit(t, s, req.Identity), original) {
				t.Fatal("rejected binding changed input or artifact")
			}
		})
	}
}

func TestManifestBindingRejectsPreviouslyPublishedNonIDChanges(t *testing.T) {
	s, req, original := bindingFixture(t)
	b := mustBind(t, s, req)
	// Simulate a fully checksummed binding written by the former permissive
	// implementation. Recomputing all integrity fields must not bless stale pixels.
	b.Manifest.Elements[0].Bounds.Width = 0.5
	var err error
	b.AuthorityDigest, err = ManifestDigest(req.Identity, b.Manifest.Elements)
	if err != nil {
		t.Fatal(err)
	}
	b.Manifest.ID = domain.ID("binding-" + hex.EncodeToString(b.AuthorityDigest[:]))
	base := bindingName(req.Identity, original.Digest, b.AuthorityDigest)
	if err := s.makeDirs(base); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(storeManifest{StoreSchemaVersion, b.Manifest})
	if err != nil {
		t.Fatal(err)
	}
	b.ManifestFile = FileRecord{Name: base + "/render.json", Digest: Digest(sha256.Sum256(manifest)), Size: int64(len(manifest))}
	record := bindingRecord{SchemaVersion: StoreSchemaVersion, Identity: req.Identity, Binding: b}
	record.Digest, err = bindingDigest(record)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(s.root, b.ManifestFile.Name), manifest)
	name := filepath.Join(s.root, base, "entry.json")
	writeTestFile(t, name, data)
	if got, err := s.lookupManifest(context.Background(), original, b.AuthorityDigest); !errors.Is(err, ErrRerenderRequired) || !reflect.DeepEqual(got, ManifestBinding{}) {
		t.Fatalf("integrity-valid incompatible binding accepted: %+v %v", got, err)
	}
	if got, hit, err := s.LookupManifest(context.Background(), req.Identity, b.Manifest.Elements); hit || !errors.Is(err, ErrRerenderRequired) || !reflect.DeepEqual(got, ManifestBinding{}) {
		t.Fatalf("legacy binding resolved: %+v %v %v", got, hit, err)
	}
	if got, err := s.BindManifest(context.Background(), req.Identity, b.Manifest.Elements); !errors.Is(err, ErrRerenderRequired) || !reflect.DeepEqual(got, ManifestBinding{}) {
		t.Fatalf("legacy binding reused: %+v %v", got, err)
	}
	after, err := os.ReadFile(name)
	if err != nil || string(after) != string(data) || !reflect.DeepEqual(mustHit(t, s, req.Identity), original) {
		t.Fatal("rejected legacy binding modified persisted data")
	}
	assertNoStages(t, s)
}

func TestManifestBindingIDOnlyMatchingPreservesMultiplicity(t *testing.T) {
	s, req := storeFixture(t)
	// Two identical visuals and a distinct third visual make set-only comparison
	// insufficient, even when total element counts are equal.
	a := req.Manifest.Elements[0]
	b, c := a, a
	b.ID, c.ID, c.Type = "second", "third", "Image"
	req.Manifest.Elements = []domain.VisualElement{a, b, c}
	original := mustPublish(t, s, req)
	a.ID, b.ID, c.ID = "z-new", "y-new", "a-new"
	req.Manifest.Elements = []domain.VisualElement{c, b, a}
	bound := mustBind(t, s, req)
	if bound.ArtifactDigest != original.Digest || !bound.Manifest.CreatedAt.IsZero() || bound.Manifest.MediaPath != original.Media.Name {
		t.Fatal("ID-only binding lost base identity or wrapper constraints")
	}
	// Change the multiset from two Text + one Image to one Text + two Image.
	req.Manifest.Elements[1].Type = "Image"
	if _, err := s.BindManifest(context.Background(), req.Identity, req.Manifest.Elements); !errors.Is(err, ErrRerenderRequired) {
		t.Fatalf("changed multiplicity rebound: %v", err)
	}
}

func TestManifestBindingOwnershipAndConcurrentPublication(t *testing.T) {
	s, req, original := bindingFixture(t)
	const n = 8
	results := make(chan ManifestBinding, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := NewStore(StoreConfig{Root: s.root})
			if err != nil {
				t.Error(err)
				return
			}
			b, err := other.BindManifest(context.Background(), req.Identity, req.Manifest.Elements)
			if err != nil {
				t.Error(err)
				return
			}
			results <- b
		}()
	}
	wg.Wait()
	close(results)
	want := mustBind(t, s, req)
	for got := range results {
		if !reflect.DeepEqual(got, want) {
			t.Fatal("different publication winner")
		}
	}
	got, hit, err := s.LookupManifest(context.Background(), req.Identity, req.Manifest.Elements)
	if err != nil || !hit || !reflect.DeepEqual(got, want) {
		t.Fatalf("lookup: %+v %v %v", got, hit, err)
	}
	if want.ArtifactDigest != original.Digest || want.Manifest.MediaPath != original.Media.Name {
		t.Fatal("binding lost media identity")
	}
	data, _ := json.Marshal(want)
	if strings.Contains(string(data), s.root) {
		t.Fatal("persisted absolute path")
	}
	got.Manifest.Elements[0].Bounds.Width = 0.5
	got.Manifest.Elements[0].EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "output"
	req.Manifest.Elements[0].Bounds.Width = 0.25
	req.Manifest.Elements[0].EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "input"
	if want.Manifest.Elements[0].Bounds.Width != 1 || want.Manifest.Elements[0].EditableProperties["nested"].(map[string]any)["values"].([]any)[0] != float64(3) {
		t.Fatal("binding aliases input/output")
	}
	loaded, hit, err := s.LookupManifest(context.Background(), req.Identity, want.Manifest.Elements)
	if err != nil || !hit || !reflect.DeepEqual(loaded, want) {
		t.Fatal("ownership mutation changed persisted binding")
	}
	if !reflect.DeepEqual(mustHit(t, s, req.Identity), original) {
		t.Fatal("original entry changed")
	}
	assertNoStages(t, s)
}

func TestManifestBindingCorruptionAndIdentity(t *testing.T) {
	for _, mode := range []string{"record", "sidecar", "missing", "media", "base-manifest", "identity", "artifact-digest", "authority-digest", "elements", "path", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			s, req, e := bindingFixture(t)
			b := mustBind(t, s, req)
			base := filepath.Join(s.root, filepath.Dir(b.ManifestFile.Name))
			name := filepath.Join(base, "entry.json")
			switch mode {
			case "record":
				writeTestFile(t, name, []byte("{"))
			case "sidecar":
				writeTestFile(t, filepath.Join(s.root, b.ManifestFile.Name), []byte("{}"))
			case "missing":
				if err := os.Remove(filepath.Join(s.root, b.ManifestFile.Name)); err != nil {
					t.Fatal(err)
				}
			case "media":
				writeTestFile(t, filepath.Join(s.root, e.Media.Name), []byte("bad"))
			case "base-manifest":
				writeTestFile(t, filepath.Join(s.root, e.ManifestFile.Name), []byte("{}"))
			case "symlink":
				outside := filepath.Join(t.TempDir(), "sentinel")
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, outside, data)
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, name); err != nil {
					t.Fatal(err)
				}
			default:
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				var r bindingRecord
				if err := json.Unmarshal(data, &r); err != nil {
					t.Fatal(err)
				}
				switch mode {
				case "identity":
					r.Identity.ProjectID = "other"
				case "artifact-digest":
					r.Binding.ArtifactDigest = Digest{}
				case "authority-digest":
					r.Binding.AuthorityDigest = Digest{}
				case "elements":
					r.Binding.Manifest.Elements[0].ID = "other"
				case "path":
					r.Binding.Manifest.MediaPath = "../private"
				}
				r.Digest, _ = bindingDigest(r) // integrity alone cannot bypass identity
				data, _ = json.Marshal(r)
				writeTestFile(t, name, data)
			}
			before, _ := os.ReadFile(name)
			if got, hit, err := s.LookupManifest(context.Background(), req.Identity, req.Manifest.Elements); err != nil || hit || !reflect.DeepEqual(got, ManifestBinding{}) {
				t.Fatalf("corrupt binding hit: %+v %v %v", got, hit, err)
			}
			if _, err := s.BindManifest(context.Background(), req.Identity, req.Manifest.Elements); !errors.Is(err, ErrEntryConflict) {
				t.Fatalf("corrupt binding replaced: %v", err)
			}
			after, _ := os.ReadFile(name)
			if string(before) != string(after) {
				t.Fatal("damaged binding modified")
			}
			assertNoStages(t, s)
		})
	}
}

func TestManifestBindingCancellationAndAtomicFailure(t *testing.T) {
	for _, mode := range []string{"before", "staged", "conflict", "winner"} {
		t.Run(mode, func(t *testing.T) {
			s, req, original := bindingFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			triggered := false
			var winner ManifestBinding
			if mode == "before" {
				cancel()
			}
			hooked := storeHookContext{ctx, func() {
				if triggered {
					return
				}
				if stageComplete(s) {
					triggered = true
					switch mode {
					case "conflict":
						digest, _ := ManifestDigest(req.Identity, req.Manifest.Elements)
						base := bindingName(req.Identity, original.Digest, digest)
						if err := s.makeDirs(base); err != nil {
							t.Fatal(err)
						}
						writeTestFile(t, filepath.Join(s.root, base, "owner-data"), []byte("preserve"))
					case "winner":
						winner = mustBind(t, s, req)
					default:
						cancel()
					}
				}
			}}
			got, err := s.BindManifest(hooked, req.Identity, req.Manifest.Elements)
			switch mode {
			case "winner":
				if err != nil || !reflect.DeepEqual(got, winner) {
					t.Fatalf("winner not returned: %v", err)
				}
			case "conflict":
				if !errors.Is(err, ErrEntryConflict) {
					t.Fatal(err)
				}
				digest, _ := ManifestDigest(req.Identity, req.Manifest.Elements)
				data, err := os.ReadFile(filepath.Join(s.root, bindingName(req.Identity, original.Digest, digest), "owner-data"))
				if err != nil || string(data) != "preserve" {
					t.Fatal("owner data changed")
				}
			default:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				if _, hit, err := s.LookupManifest(context.Background(), req.Identity, req.Manifest.Elements); hit || err != nil {
					t.Fatal("canceled binding published")
				}
				mustBind(t, s, req)
			}
			if mode != "before" && !triggered {
				t.Fatal("boundary not exercised")
			}
			if !reflect.DeepEqual(mustHit(t, s, req.Identity), original) {
				t.Fatal("original changed")
			}
			assertNoStages(t, s)
		})
	}
}

func TestManifestBindingRejectsUnsafeInputAndRoot(t *testing.T) {
	for _, mode := range []string{"raw", "path", "secret", "bounds", "duplicate", "shot", "id", "limit", "root-link"} {
		t.Run(mode, func(t *testing.T) {
			s, req, original := bindingFixture(t)
			switch mode {
			case "raw":
				req.Manifest.Elements[0].EditableProperties["raw"] = errors.New("private")
			case "path":
				req.Manifest.Elements[0].EditableProperties["location"] = "/private/file"
			case "secret":
				req.Manifest.Elements[0].EditableProperties["apiToken"] = "private"
			case "bounds":
				req.Manifest.Elements[0].Bounds.Width = 2
			case "duplicate":
				req.Manifest.Elements = append(req.Manifest.Elements, req.Manifest.Elements[0])
			case "shot":
				req.Manifest.Elements[0].ShotID = "other"
			case "id":
				req.Manifest.Elements[0].ID = "../outside"
			case "limit":
				req.Manifest.Elements[0].EditableProperties["long"] = strings.Repeat("x", int(s.metadataLimit))
			case "root-link":
				outside := t.TempDir()
				if err := os.Symlink(outside, filepath.Join(s.root, "bindings")); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					items, err := os.ReadDir(outside)
					if err != nil || len(items) != 0 {
						t.Error("escaped binding root")
					}
				})
			}
			if got, err := s.BindManifest(context.Background(), req.Identity, req.Manifest.Elements); err == nil || !reflect.DeepEqual(got, ManifestBinding{}) {
				t.Fatalf("unsafe binding accepted: %+v %v", got, err)
			}
			if !reflect.DeepEqual(mustHit(t, s, req.Identity), original) {
				t.Fatal("invalid binding changed base entry")
			}
			assertNoStages(t, s)
		})
	}
	s, req, _ := bindingFixture(t)
	if _, err := s.BindManifest(nil, req.Identity, req.Manifest.Elements); !errors.Is(err, ErrStoreInput) {
		t.Fatal(err)
	}
	if _, _, err := s.LookupManifest(nil, req.Identity, req.Manifest.Elements); !errors.Is(err, ErrStoreInput) {
		t.Fatal(err)
	}
	if _, err := ManifestDigest(req.Identity, []domain.VisualElement{{ID: "empty"}}); !errors.Is(err, ErrStoreInput) {
		t.Fatal(err)
	}
}
