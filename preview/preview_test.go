package preview

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
)

func fixture(t *testing.T) (*Reader, domain.Project, artifact.PublishRequest, string) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("artifact store requires macOS or Linux")
	}
	root := t.TempDir()
	s, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewReader(s)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	e := domain.VisualElement{ID: "text", ShotID: "shot", Type: "Text", Start: time.Second, End: 3 * time.Second, Bounds: &domain.Bounds{Width: 1, Height: 1}, EditableProperties: map[string]any{"nested": map[string]any{"values": []any{"original", float64(2)}}}}
	p := domain.Project{ID: "project", Name: "preview fixture", Plan: &domain.VideoPlan{ID: "plan", ProjectID: "project", FlowID: "flow", FramingID: "framing", CreatedAt: now, Shots: []domain.Shot{{ID: "shot", PlanID: "plan", Category: domain.ShotCategory("TEXT"), Duration: 4 * time.Second}}}, Timeline: &domain.Timeline{ID: "timeline", ProjectID: "project", Entries: []domain.TimelineEntry{{ShotID: "shot", Start: 5 * time.Second, End: 9 * time.Second}}, Elements: []domain.VisualElement{e, {ID: "overlay", ShotID: "shot", Type: "Text", Start: 2 * time.Second, End: 4 * time.Second}}}}
	key, err := artifact.BuildKey(artifact.KeyInput{Quality: domain.QualityDraft, Shot: artifact.ShotInput{Duration: 4 * time.Second}, Render: artifact.RenderInput{Revision: "r1", Width: 640, Height: 360, FPS: 24}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source.mp4"), []byte("rendered-media"), 0600); err != nil {
		t.Fatal(err)
	}
	req := artifact.PublishRequest{Identity: artifact.Identity{Key: key, ProjectID: p.ID, ShotID: "shot", Quality: domain.QualityDraft}, MediaPath: "source.mp4", Manifest: domain.RenderManifest{ID: "manifest", ShotID: "shot", MediaPath: "source.mp4", CreatedAt: now, Elements: []domain.VisualElement{cloneElement(e), cloneElement(p.Timeline.Elements[1])}}}
	req.Validation = &domain.ValidationResult{ID: "validation", TargetID: "shot", Status: domain.ValidationPassed}
	return r, p, req, root
}

func resolve(t *testing.T, r *Reader, p domain.Project, id artifact.Identity) View {
	t.Helper()
	v, hit, err := r.Resolve(context.Background(), p, id)
	if err != nil || !hit {
		t.Fatalf("resolve: hit=%v err=%v", hit, err)
	}
	return v
}

func publish(t *testing.T, r *Reader, req artifact.PublishRequest) artifact.Entry {
	t.Helper()
	e, err := r.store.Publish(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestResolveVerifiedArtifactAndManifest(t *testing.T) {
	r, p, req, root := fixture(t)
	v, hit, err := r.Resolve(context.Background(), p, req.Identity)
	if err != nil || hit || !reflect.DeepEqual(v, View{}) {
		t.Fatalf("missing: %v %v", hit, err)
	}
	e := publish(t, r, req)
	v = resolve(t, r, p, req.Identity)
	if v.Identity() != req.Identity || v.MediaName() != e.Media.Name || v.ManifestName() != e.ManifestFile.Name || v.MediaDigest() != e.Media.Digest || v.Placement() != p.Timeline.Entries[0] || !reflect.DeepEqual(v.Manifest(), e.Manifest) {
		t.Fatal("verified entry not preserved")
	}
	if filepath.IsAbs(v.MediaName()) || strings.Contains(v.MediaName(), root) {
		t.Fatal("absolute media name")
	}
	wrongQuality := req.Identity
	wrongQuality.Quality = domain.QualityFinal
	if _, hit, err := r.Resolve(context.Background(), p, wrongQuality); err != nil || hit {
		t.Fatalf("quality scope: %v %v", hit, err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(e.Media.Name)), []byte("tampered"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := r.Resolve(context.Background(), p, req.Identity); err != nil || hit {
		t.Fatalf("unverified media exposed: %v %v", hit, err)
	}
}

func TestManifestMustMatchAuthority(t *testing.T) {
	for _, kind := range []string{"missing", "extra", "unknown", "wrong-time", "wrong-properties", "wrong-bounds", "wrong-type", "reordered", "corrupt-sidecar"} {
		t.Run(kind, func(t *testing.T) {
			r, p, req, root := fixture(t)
			switch kind {
			case "missing":
				req.Manifest.Elements = req.Manifest.Elements[:1]
			case "extra":
				extra := cloneElement(req.Manifest.Elements[0])
				extra.ID = "extra"
				req.Manifest.Elements = append(req.Manifest.Elements, extra)
			case "unknown":
				req.Manifest.Elements[0].ID = "unknown"
			case "wrong-time":
				req.Manifest.Elements[0].End++
			case "wrong-properties":
				req.Manifest.Elements[0].EditableProperties["text"] = "changed"
			case "wrong-bounds":
				req.Manifest.Elements[0].Bounds.Width = 0.5
			case "wrong-type":
				req.Manifest.Elements[0].Type = "Image"
			case "reordered":
				req.Manifest.Elements[0], req.Manifest.Elements[1] = req.Manifest.Elements[1], req.Manifest.Elements[0]
			}
			e := publish(t, r, req)
			if kind == "corrupt-sidecar" {
				if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(e.ManifestFile.Name)), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			_, hit, err := r.Resolve(context.Background(), p, req.Identity)
			switch kind {
			case "reordered":
				if err != nil || !hit {
					t.Fatalf("order mismatch rejected: %v", err)
				}
			case "corrupt-sidecar":
				if err != nil || hit {
					t.Fatalf("corrupt sidecar accepted: %v %v", hit, err)
				}
			default:
				if hit || !errors.Is(err, ErrManifest) {
					t.Fatalf("stale manifest accepted: %v %v", hit, err)
				}
			}
		})
	}
}

func TestTimelineCoordinatesAndElementSelection(t *testing.T) {
	r, p, req, _ := fixture(t)
	publish(t, r, req)
	v := resolve(t, r, p, req.Identity)
	for _, tc := range []struct {
		at  time.Duration
		ids []domain.ID
	}{
		{0, nil}, {time.Second, []domain.ID{"text"}}, {2 * time.Second, []domain.ID{"text", "overlay"}}, {3 * time.Second, []domain.ID{"overlay"}},
	} {
		shot, err := v.AtShot(tc.at)
		if err != nil {
			t.Fatal(err)
		}
		absolute, err := v.AtProject(5*time.Second + tc.at)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(shot, absolute) || shot.ShotTimestamp != tc.at || shot.ProjectTimestamp != 5*time.Second+tc.at || shot.ProjectID != p.ID || shot.ShotID != "shot" {
			t.Fatalf("coordinates: %+v %+v", shot, absolute)
		}
		var ids []domain.ID
		for _, e := range shot.Elements {
			ids = append(ids, e.ID)
		}
		if !reflect.DeepEqual(ids, tc.ids) {
			t.Fatalf("active at %v: %v want %v", tc.at, ids, tc.ids)
		}
	}
	e, ok := v.Element("text")
	if !ok || e.ProjectStart != 6*time.Second || e.ProjectEnd != 8*time.Second || e.Visual.Start != time.Second {
		t.Fatalf("element coordinates: %+v", e)
	}
	if _, ok := v.Element("missing"); ok {
		t.Fatal("invented element")
	}
	for _, at := range []time.Duration{-1, 4 * time.Second, math.MaxInt64} {
		if _, err := v.AtShot(at); !errors.Is(err, ErrTimestamp) {
			t.Fatal(err)
		}
	}
	for _, at := range []time.Duration{-1, 0, 5*time.Second - 1, 9 * time.Second, math.MaxInt64} {
		if _, err := v.AtProject(at); !errors.Is(err, ErrTimestamp) {
			t.Fatal(err)
		}
	}
	if _, err := (View{}).AtShot(0); !errors.Is(err, ErrTimestamp) {
		t.Fatal(err)
	}
	// Largest representable valid project interval: conversions must not overflow.
	p.Timeline.Entries[0].End = time.Duration(math.MaxInt64)
	p.Timeline.Entries[0].Start = time.Duration(math.MaxInt64) - 4*time.Second
	v = resolve(t, r, p, req.Identity)
	position, err := v.AtShot(4*time.Second - 1)
	if err != nil || position.ProjectTimestamp != time.Duration(math.MaxInt64)-1 {
		t.Fatalf("overflow: %+v %v", position, err)
	}
}

func TestDeepOwnershipAndConcurrentReads(t *testing.T) {
	r, p, req, _ := fixture(t)
	publish(t, r, req)
	v := resolve(t, r, p, req.Identity)
	want, err := json.Marshal(v.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	mutate := func(e *domain.VisualElement) {
		e.Bounds.Width = 0.25
		e.EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "mutated"
	}
	mutate(&p.Timeline.Elements[0])
	p.Timeline.Entries[0].Start = 0
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m := v.Manifest()
			mutate(&m.Elements[0])
			m.Elements[0].ID = "changed"
			e, _ := v.Element("text")
			mutate(&e.Visual)
			pos, err := v.AtShot(time.Second)
			if err != nil {
				t.Error(err)
				return
			}
			mutate(&pos.Elements[0])
		}()
	}
	wg.Wait()
	got, err := json.Marshal(v.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) || v.Placement().Start != 5*time.Second {
		t.Fatal("view aliased input/output")
	}
}

func TestRevisionTracksRebuildAndPlacement(t *testing.T) {
	r, p, req, root := fixture(t)
	publish(t, r, req)
	first := resolve(t, r, p, req.Identity)
	if len(first.Revision()) != 64 {
		t.Fatal("missing revision")
	}
	s, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	r2, err := NewReader(s)
	if err != nil {
		t.Fatal(err)
	}
	if resolve(t, r2, p, req.Identity).Revision() != first.Revision() {
		t.Fatal("revision changed across restart")
	}
	p.UpdatedAt = time.Now()
	if resolve(t, r, p, req.Identity).Revision() != first.Revision() {
		t.Fatal("clock affected revision")
	}
	p.Timeline.Entries[0].Start += time.Second
	p.Timeline.Entries[0].End += time.Second
	if resolve(t, r, p, req.Identity).Revision() == first.Revision() {
		t.Fatal("placement did not affect revision")
	}
	p.Timeline.Entries[0] = first.Placement()
	req.Identity.Key = artifact.Key(strings.Repeat("a", 64))
	if err := os.WriteFile(filepath.Join(root, "source.mp4"), []byte("rebuilt-media"), 0600); err != nil {
		t.Fatal(err)
	}
	publish(t, r, req)
	rebuilt := resolve(t, r, p, req.Identity)
	if rebuilt.Revision() == first.Revision() || rebuilt.MediaName() == first.MediaName() {
		t.Fatal("rebuild not reflected")
	}
	if resolve(t, r, p, first.Identity()).Revision() != first.Revision() {
		t.Fatal("new artifact modified old snapshot")
	}
}

func TestInvalidAuthorityAndContexts(t *testing.T) {
	r, p, req, _ := fixture(t)
	publish(t, r, req)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := r.Resolve(ctx, p, req.Identity); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := r.Resolve(nil, p, req.Identity); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := NewReader(nil); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, _, err := (*Reader)(nil).Resolve(context.Background(), p, req.Identity); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, kind := range []string{"project", "shot", "timeline", "plan", "unplaced", "invalid"} {
		t.Run(kind, func(t *testing.T) {
			r, p, req, _ := fixture(t)
			switch kind {
			case "project":
				req.Identity.ProjectID = "other"
			case "shot":
				req.Identity.ShotID = "other"
			case "timeline":
				p.Timeline = nil
			case "plan":
				p.Plan = nil
			case "unplaced":
				p.Timeline.Entries = nil
				p.Timeline.Elements = nil
			case "invalid":
				p.Timeline.Entries[0].End++
			}
			if _, hit, err := r.Resolve(context.Background(), p, req.Identity); hit || !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid authority: %v %v", hit, err)
			}
		})
	}
}
