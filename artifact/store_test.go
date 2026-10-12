package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func storeFixture(t *testing.T) (*Store, PublishRequest) {
	t.Helper()
	root := t.TempDir()
	s, err := NewStore(StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "sources"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "sources", "shot.mp4"), []byte("already-rendered-media"))
	key, err := BuildKey(KeyInput{Quality: domain.QualityDraft, Shot: ShotInput{Duration: 2 * time.Second}, Render: RenderInput{Revision: "r1", Width: 640, Height: 360, FPS: 24}})
	if err != nil {
		t.Fatal(err)
	}
	seed := "17"
	peak := 2.0
	return s, PublishRequest{
		Identity:   Identity{Key: key, ProjectID: "project-1", ShotID: "shot-1", Quality: domain.QualityDraft},
		MediaPath:  "sources/shot.mp4",
		Manifest:   domain.RenderManifest{ID: "manifest-1", ShotID: "shot-1", MediaPath: "sources/shot.mp4", Elements: []domain.VisualElement{{ID: "element-1", ShotID: "shot-1", Type: "Text", End: 2 * time.Second, Bounds: &domain.Bounds{Width: 1, Height: 1}, EditableProperties: map[string]any{"text": "hello", "nested": map[string]any{"values": []any{float64(3), "four"}}}}}},
		Generation: &domain.GenerationResult{ID: "generation-1", RequestID: "request-1", AssetID: "asset-1", Seed: "17", DurationMs: 4, PeakMemoryMB: &peak, Metadata: map[string]any{"measurements": []any{float64(1), float64(2)}}},
		Validation: &domain.ValidationResult{ID: "validation-1", TargetID: "asset-1", Status: domain.ValidationPassed, Details: map[string]any{"issues": []any{map[string]any{"code": "checked"}}}},
		Provenance: &Provenance{Seed: &seed, Provider: &Revision{Name: "renderer", Revision: "v1"}, Model: &Revision{Name: "fixture", Revision: "v2"}, Profile: &Revision{Name: "small", Revision: "v3"}, Sources: map[string]Digest{"font": Digest(sha256.Sum256([]byte("font")))}, Dependencies: map[string]Digest{"background": Digest(sha256.Sum256([]byte("background")))}},
	}
}

func writeTestFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGenerationPeakMemoryMeasurementPersistence(t *testing.T) {
	for _, mode := range []string{"unknown", "zero", "measured", "negative", "nan", "infinity"} {
		t.Run(mode, func(t *testing.T) {
			s, req := storeFixture(t)
			peak := 0.0
			req.Generation.PeakMemoryMB = &peak
			switch mode {
			case "unknown":
				req.Generation.PeakMemoryMB = nil
			case "measured":
				peak = 12.5
			case "negative":
				peak = -1
			case "nan":
				peak = math.NaN()
			case "infinity":
				peak = math.Inf(1)
			}
			if mode == "negative" || mode == "nan" || mode == "infinity" {
				if _, err := s.Publish(context.Background(), req); !errors.Is(err, ErrStoreInput) {
					t.Fatalf("invalid measurement accepted: %v", err)
				}
				mustMiss(t, s, req.Identity)
				return
			}
			mustPublish(t, s, req)
			// Mutating the caller's measurement must not alter stored evidence.
			peak = 99
			got := mustHit(t, s, req.Identity).Generation
			if mode == "unknown" {
				if got.PeakMemoryMB != nil {
					t.Fatal("unknown peak became a measurement")
				}
			} else {
				want := 0.0
				if mode == "measured" {
					want = 12.5
				}
				if got.PeakMemoryMB == nil || *got.PeakMemoryMB != want {
					t.Fatalf("measurement not preserved: %+v", got)
				}
			}
		})
	}
}

func mustPublish(t *testing.T, s *Store, req PublishRequest) Entry {
	t.Helper()
	e, err := s.Publish(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func mustMiss(t *testing.T, s *Store, id Identity) {
	t.Helper()
	e, hit, err := s.Lookup(context.Background(), id)
	if err != nil || hit || !reflect.DeepEqual(e, Entry{}) {
		t.Fatalf("expected safe miss, got %#v %v %v", e, hit, err)
	}
}

func mustHit(t *testing.T, s *Store, id Identity) Entry {
	t.Helper()
	e, hit, err := s.Lookup(context.Background(), id)
	if err != nil || !hit {
		t.Fatalf("expected hit: %v %v", hit, err)
	}
	return e
}

func TestStorePublishAndVerifiedHit(t *testing.T) {
	s, req := storeFixture(t)
	mustMiss(t, s, req.Identity)
	e := mustPublish(t, s, req)
	wantDigest := Digest(sha256.Sum256([]byte("already-rendered-media")))
	if e.Media.Digest != wantDigest || e.Media.Size != 22 {
		t.Fatalf("media identity: %+v", e.Media)
	}
	if !reflect.DeepEqual(e, mustHit(t, s, req.Identity)) {
		t.Fatal("publication and lookup snapshots differ")
	}
	if e.Manifest.MediaPath != e.Media.Name || !logicalName(e.Media.Name) || !logicalName(e.ManifestFile.Name) {
		t.Fatal("unsafe logical names")
	}
	if req.Manifest.MediaPath != req.MediaPath {
		t.Fatal("caller manifest modified")
	}
	for _, name := range []string{e.ManifestFile.Name, entryName(req.Identity) + "/entry.json"} {
		data, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), s.root) {
			t.Fatal("persisted absolute root")
		}
	}
	// Source is independent of the entry after copying.
	writeTestFile(t, filepath.Join(s.root, filepath.FromSlash(req.MediaPath)), []byte("replacement source"))
	if got := mustHit(t, s, req.Identity); !reflect.DeepEqual(e, got) {
		t.Fatal("source mutation changed entry")
	}
	if got := mustPublish(t, s, req); !reflect.DeepEqual(e, got) {
		t.Fatal("second publisher replaced winner")
	}
	s2, err := NewStore(StoreConfig{Root: s.root})
	if err != nil {
		t.Fatal(err)
	}
	if got := mustHit(t, s2, req.Identity); !reflect.DeepEqual(e, got) {
		t.Fatal("entry not persistent across store instances")
	}
	if _, err := os.Stat(filepath.Join(s.root, filepath.FromSlash(req.MediaPath))); err != nil {
		t.Fatal("source removed")
	}
}

func mutateRecord(t *testing.T, s *Store, id Identity, reseal bool, change func(*storeRecord)) {
	t.Helper()
	name := filepath.Join(s.root, filepath.FromSlash(entryName(id)), "entry.json")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var r storeRecord
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	change(&r)
	if reseal {
		r.Digest, err = recordDigest(r)
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, name, data)
}

func TestStoreCorruptionIsSafeMiss(t *testing.T) {
	cases := map[string]func(*testing.T, *Store, PublishRequest, Entry){
		"truncated record": func(t *testing.T, s *Store, r PublishRequest, _ Entry) {
			writeTestFile(t, filepath.Join(s.root, entryName(r.Identity), "entry.json"), []byte("{"))
		},
		"partial media": func(t *testing.T, s *Store, _ PublishRequest, e Entry) {
			if err := os.Remove(filepath.Join(s.root, e.Media.Name)); err != nil {
				t.Fatal(err)
			}
		},
		"missing manifest": func(t *testing.T, s *Store, _ PublishRequest, e Entry) {
			if err := os.Remove(filepath.Join(s.root, e.ManifestFile.Name)); err != nil {
				t.Fatal(err)
			}
		},
		"media digest": func(t *testing.T, s *Store, _ PublishRequest, e Entry) {
			writeTestFile(t, filepath.Join(s.root, e.Media.Name), []byte("different-media-bytes!"))
		},
		"same size media digest": func(t *testing.T, s *Store, _ PublishRequest, e Entry) {
			writeTestFile(t, filepath.Join(s.root, e.Media.Name), []byte(strings.Repeat("X", int(e.Media.Size))))
		},
		"manifest digest": func(t *testing.T, s *Store, _ PublishRequest, e Entry) {
			writeTestFile(t, filepath.Join(s.root, e.ManifestFile.Name), []byte("{}"))
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			s, req := storeFixture(t)
			e := mustPublish(t, s, req)
			mutate(t, s, req, e)
			mustMiss(t, s, req.Identity)
		})
	}
	for name, mutate := range map[string]func(*storeRecord){
		"schema":                    func(r *storeRecord) { r.SchemaVersion++ },
		"key":                       func(r *storeRecord) { r.Entry.Identity.Key = Key(strings.Repeat("a", 64)) },
		"project":                   func(r *storeRecord) { r.Entry.Identity.ProjectID = "different" },
		"shot":                      func(r *storeRecord) { r.Entry.Identity.ShotID = "different" },
		"quality":                   func(r *storeRecord) { r.Entry.Identity.Quality = domain.QualityFinal },
		"media size":                func(r *storeRecord) { r.Entry.Media.Size++ },
		"media size zero":           func(r *storeRecord) { r.Entry.Media.Size = 0 },
		"media digest":              func(r *storeRecord) { r.Entry.Media.Digest = Digest{} },
		"manifest size":             func(r *storeRecord) { r.Entry.ManifestFile.Size++ },
		"manifest digest":           func(r *storeRecord) { r.Entry.ManifestFile.Digest = Digest{} },
		"manifest shot":             func(r *storeRecord) { r.Entry.Manifest.ShotID = "different" },
		"element shot":              func(r *storeRecord) { r.Entry.Manifest.Elements[0].ShotID = "different" },
		"manifest content mismatch": func(r *storeRecord) { r.Entry.Manifest.Elements[0].EditableProperties["text"] = "different" },
		"manifest name traversal":   func(r *storeRecord) { r.Entry.ManifestFile.Name = "../outside" },
		"media name traversal":      func(r *storeRecord) { r.Entry.Media.Name = "../outside" },
		"absolute media name":       func(r *storeRecord) { r.Entry.Media.Name = "/outside/media" },
		"validation target":         func(r *storeRecord) { r.Entry.Validation.TargetID = "wrong-shot" },
		"metadata path":             func(r *storeRecord) { r.Entry.Generation.Metadata["location"] = "/private/media" },
	} {
		t.Run(name, func(t *testing.T) {
			s, req := storeFixture(t)
			mustPublish(t, s, req)
			mutateRecord(t, s, req.Identity, true, mutate)
			mustMiss(t, s, req.Identity)
		})
	}
	t.Run("record checksum binds provenance", func(t *testing.T) {
		s, req := storeFixture(t)
		mustPublish(t, s, req)
		mutateRecord(t, s, req.Identity, false, func(r *storeRecord) { *r.Entry.Provenance.Seed = "999" })
		mustMiss(t, s, req.Identity)
	})
}

func TestStoreIdentityIsolation(t *testing.T) {
	s, req := storeFixture(t)
	mustPublish(t, s, req)
	for _, changed := range []Identity{
		{req.Identity.Key, "other-project", req.Identity.ShotID, req.Identity.Quality},
		{req.Identity.Key, req.Identity.ProjectID, "other-shot", req.Identity.Quality},
		{req.Identity.Key, req.Identity.ProjectID, req.Identity.ShotID, domain.QualityFinal},
	} {
		mustMiss(t, s, changed)
		// Copying the complete files under another identity is still a miss.
		if err := s.makeDirs(entryName(changed)); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"media", "render.json", "entry.json"} {
			data, err := os.ReadFile(filepath.Join(s.root, entryName(req.Identity), file))
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(s.root, entryName(changed), file), data)
		}
		mustMiss(t, s, changed)
	}
}

func TestStoreRejectsInvalidInputs(t *testing.T) {
	for name, mutate := range map[string]func(*PublishRequest){
		"project traversal": func(r *PublishRequest) { r.Identity.ProjectID = "../outside" },
		"shot separator":    func(r *PublishRequest) { r.Identity.ShotID = "shot/other" },
		"shot backslash":    func(r *PublishRequest) { r.Identity.ShotID = `shot\other` },
		"key":               func(r *PublishRequest) { r.Identity.Key = "../../escape" },
		"quality":           func(r *PublishRequest) { r.Identity.Quality = "unknown" },
		"path traversal": func(r *PublishRequest) {
			r.MediaPath = "sources/../sources/shot.mp4"
			r.Manifest.MediaPath = r.MediaPath
		},
		"absolute path":         func(r *PublishRequest) { r.MediaPath = "/private/source.mp4"; r.Manifest.MediaPath = r.MediaPath },
		"windows path":          func(r *PublishRequest) { r.MediaPath = `C:\source.mp4`; r.Manifest.MediaPath = r.MediaPath },
		"uri":                   func(r *PublishRequest) { r.MediaPath = "file://source.mp4"; r.Manifest.MediaPath = r.MediaPath },
		"nul path":              func(r *PublishRequest) { r.MediaPath = "source\x00.mp4"; r.Manifest.MediaPath = r.MediaPath },
		"wrong manifest source": func(r *PublishRequest) { r.Manifest.MediaPath = "sources/wrong.mp4" },
		"wrong manifest shot":   func(r *PublishRequest) { r.Manifest.ShotID = "other" },
		"wrong element shot":    func(r *PublishRequest) { r.Manifest.Elements[0].ShotID = "other" },
		"duplicate element":     func(r *PublishRequest) { r.Manifest.Elements = append(r.Manifest.Elements, r.Manifest.Elements[0]) },
		"negative time":         func(r *PublishRequest) { r.Manifest.Elements[0].Start = -1 },
		"empty interval":        func(r *PublishRequest) { r.Manifest.Elements[0].End = 0 },
		"invalid bounds":        func(r *PublishRequest) { r.Manifest.Elements[0].Bounds.X = 2 },
		"infinite bounds":       func(r *PublishRequest) { r.Manifest.Elements[0].Bounds.X = math.Inf(1) },
		"non JSON":              func(r *PublishRequest) { r.Generation.Metadata["callback"] = func() {} },
		"live context":          func(r *PublishRequest) { r.Generation.Metadata["execution"] = context.Background() },
		"raw error":             func(r *PublishRequest) { r.Generation.Metadata["failure"] = errors.New("private error") },
		"nested path": func(r *PublishRequest) {
			r.Generation.Metadata["nested"] = []any{map[string]any{"file": "/private/media"}}
		},
		"prompt":            func(r *PublishRequest) { r.Generation.Metadata["prompt"] = "do not persist" },
		"token":             func(r *PublishRequest) { r.Validation.Details["api_token"] = "do not persist" },
		"validation target": func(r *PublishRequest) { r.Validation.TargetID = "other" },
		"validation status": func(r *PublishRequest) { r.Validation.Status = "invented" },
		"provenance source": func(r *PublishRequest) { r.Provenance.Sources["image"] = Digest{} },
		"provenance path":   func(r *PublishRequest) { r.Provenance.Provider.Name = "/private/provider" },
	} {
		t.Run(name, func(t *testing.T) {
			s, req := storeFixture(t)
			original := req.Identity
			mutate(&req)
			e, err := s.Publish(context.Background(), req)
			if err == nil || !reflect.DeepEqual(e, Entry{}) {
				t.Fatalf("accepted invalid input: %v", err)
			}
			if strings.Contains(err.Error(), s.root) || strings.Contains(err.Error(), "do not persist") {
				t.Fatal("unsafe error text")
			}
			mustMiss(t, s, original)
		})
	}
	s, req := storeFixture(t)
	if _, err := s.Publish(nil, req); !errors.Is(err, ErrStoreInput) {
		t.Fatalf("nil context: %v", err)
	}
	if _, _, err := s.Lookup(nil, req.Identity); !errors.Is(err, ErrStoreInput) {
		t.Fatalf("nil context lookup: %v", err)
	}
	if _, _, err := s.Lookup(context.Background(), Identity{}); !errors.Is(err, ErrStoreInput) {
		t.Fatalf("invalid lookup: %v", err)
	}
	var zero Store
	if _, err := zero.Publish(context.Background(), req); !errors.Is(err, ErrStoreInput) {
		t.Fatalf("zero store: %v", err)
	}
}

func TestStoreConcurrentFirstPublisherWins(t *testing.T) {
	s, req := storeFixture(t)
	const count = 16
	results := make(chan Entry, count)
	errs := make(chan error, count)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, err := NewStore(StoreConfig{Root: s.root})
			if err != nil {
				errs <- err
				return
			}
			<-start
			e, err := other.Publish(context.Background(), req)
			if err != nil {
				errs <- err
				return
			}
			results <- e
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	winner := mustHit(t, s, req.Identity)
	for e := range results {
		if !reflect.DeepEqual(e, winner) {
			t.Fatal("publishers did not receive same winner")
		}
	}
	assertNoStages(t, s)
}

func assertNoStages(t *testing.T, s *Store) {
	t.Helper()
	err := filepath.WalkDir(s.root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasPrefix(d.Name(), ".stage-") {
			t.Errorf("staging directory leaked")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStoreSnapshotOwnership(t *testing.T) {
	s, req := storeFixture(t)
	e := mustPublish(t, s, req)
	want := mustHit(t, s, req.Identity)
	// Exercise every pointer, map, slice and nested JSON extension family.
	req.Manifest.Elements[0].Bounds.X = 0.5
	req.Manifest.Elements[0].EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "mutated"
	req.Generation.Metadata["measurements"].([]any)[0] = "mutated"
	req.Validation.Details["issues"].([]any)[0].(map[string]any)["code"] = "mutated"
	*req.Provenance.Seed = "changed"
	req.Provenance.Provider.Revision = "changed"
	req.Provenance.Model.Name = "changed"
	req.Provenance.Profile.Name = "changed"
	req.Provenance.Sources["font"] = Digest{}
	req.Provenance.Dependencies["background"] = Digest{}
	if !reflect.DeepEqual(e, want) {
		t.Fatal("Publish output aliases input")
	}
	e.Manifest.Elements[0].Bounds.X = 0.8
	e.Manifest.Elements[0].EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "returned mutation"
	e.Generation.Metadata["measurements"].([]any)[0] = "returned mutation"
	e.Validation.Details["issues"].([]any)[0].(map[string]any)["code"] = "returned mutation"
	*e.Provenance.Seed = "changed"
	e.Provenance.Sources["font"] = Digest{}
	e.Provenance.Dependencies["background"] = Digest{}
	if got := mustHit(t, s, req.Identity); !reflect.DeepEqual(got, want) {
		t.Fatal("output mutation changed stored entry")
	}
	got := mustHit(t, s, req.Identity)
	got.Manifest.Elements[0].EditableProperties["text"] = "lookup mutation"
	got.Generation.Metadata["measurements"].([]any)[0] = "lookup mutation"
	if next := mustHit(t, s, req.Identity); !reflect.DeepEqual(next, want) {
		t.Fatal("Lookup output aliases retained state")
	}
}
