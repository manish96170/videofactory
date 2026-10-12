package preview

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/render"
	"github.com/manish96170/videofactory/validation"
)

type coordinatorRenderer func(context.Context, render.ShotRenderRequest) (render.ShotRenderResult, error)

func (f coordinatorRenderer) RenderShot(ctx context.Context, req render.ShotRenderRequest) (render.ShotRenderResult, error) {
	return f(ctx, req)
}

type coordinatorValidator func(context.Context, validation.Request) (domain.ValidationResult, error)

func (f coordinatorValidator) Validate(ctx context.Context, req validation.Request) (domain.ValidationResult, error) {
	return f(ctx, req)
}

func coordinatorFixture(t *testing.T) (*Coordinator, BuildRequest, string, *int, *int) {
	t.Helper()
	_, p, published, root := fixture(t)
	input := artifact.KeyInput{Quality: domain.QualityDraft, Shot: artifact.ShotInput{Duration: 4 * time.Second}, Render: artifact.RenderInput{Revision: "r1", Width: 640, Height: 360, FPS: 24}}
	renders, validations := new(int), new(int)
	renderer := coordinatorRenderer(func(ctx context.Context, req render.ShotRenderRequest) (render.ShotRenderResult, error) {
		*renders++
		if ctx.Err() != nil || req.Shot.ID != "shot" || !filepath.IsAbs(req.OutputDir) {
			t.Fatal("invalid render request")
		}
		media := filepath.Join(req.OutputDir, "shot.mp4")
		if err := os.WriteFile(media, []byte("verified-rendered-media"), 0600); err != nil {
			t.Fatal(err)
		}
		var manifest domain.RenderManifest
		if coordinatorSnapshot(published.Manifest, &manifest) != nil {
			t.Fatal("manifest snapshot")
		}
		manifest.MediaPath = media
		return render.ShotRenderResult{MediaPath: media, Manifest: manifest}, nil
	})
	validator := coordinatorValidator(func(ctx context.Context, req validation.Request) (domain.ValidationResult, error) {
		*validations++
		data, err := os.ReadFile(req.Asset.URI)
		if err != nil || string(data) != "verified-rendered-media" || ctx.Err() != nil {
			t.Fatal("validator did not receive rendered media")
		}
		if req.ID != "shot" || req.TargetID != "shot" || req.Asset.Kind != domain.AssetKindVideo || req.Requirements.Kind != domain.AssetKindVideo || req.Requirements.ExpectedDuration != 4*time.Second || req.Requirements.Width != 640 || req.Requirements.Height != 360 || req.Requirements.FPS != 24 {
			t.Fatalf("incorrect verification requirements: %+v", req)
		}
		return domain.ValidationResult{ID: req.ID, TargetID: req.TargetID, Status: domain.ValidationPassed}, nil
	})
	c, err := NewCoordinator(artifact.StoreConfig{Root: root}, renderer, validator)
	if err != nil {
		t.Fatal(err)
	}
	return c, BuildRequest{Project: p, Identity: published.Identity, Input: input}, root, renders, validations
}

func coordinatorNoEntry(t *testing.T, c *Coordinator, req BuildRequest, root string) {
	t.Helper()
	if _, hit, err := c.reader.store.Lookup(context.Background(), req.Identity); err != nil || hit {
		t.Fatalf("unexpected publication: hit=%v err=%v", hit, err)
	}
	coordinatorNoWork(t, root)
}

func coordinatorNoWork(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".preview-") {
			t.Fatal("temporary render directory leaked")
		}
	}
}

func TestCoordinatorMissPublishesAndHitAvoidsRendering(t *testing.T) {
	for _, quality := range []domain.Quality{domain.QualityDraft, domain.QualityFinal} {
		t.Run(string(quality), func(t *testing.T) {
			c, req, root, renders, validations := coordinatorFixture(t)
			req.Input.Quality, req.Identity.Quality = quality, quality
			req.Input.Seed = new(string)
			*req.Input.Seed = "0"
			req.Input.Sources = map[string]artifact.Digest{"font": sha256.Sum256([]byte("font"))}
			req.Input.Dependencies = map[string]artifact.Digest{"background": sha256.Sum256([]byte("background"))}
			req.Input.Provider = &artifact.Revision{Name: "deterministic", Revision: "1"}
			req.Identity.Key, _ = artifact.BuildKey(req.Input)
			first, err := c.Build(context.Background(), req)
			if err != nil || first.CacheHit || first.View.Identity() != req.Identity || first.View.Revision() == "" || *renders != 1 || *validations != 1 {
				t.Fatalf("miss: %+v %v renders=%d validations=%d", first, err, *renders, *validations)
			}
			entry, hit, err := c.reader.store.Lookup(context.Background(), req.Identity)
			if err != nil || !hit || entry.Media.Digest != artifact.Digest(sha256.Sum256([]byte("verified-rendered-media"))) || entry.Validation.Status != domain.ValidationPassed || entry.Provenance.Sources["font"] != req.Input.Sources["font"] || *entry.Provenance.Seed != "0" {
				t.Fatalf("publication: %+v %v", entry, err)
			}
			if filepath.IsAbs(first.View.MediaName()) || first.View.Manifest().MediaPath != entry.Media.Name {
				t.Fatal("manifest path not rebound to logical name")
			}
			position, err := first.View.AtShot(2 * time.Second)
			if err != nil || len(position.Elements) != 2 || position.ProjectTimestamp != 7*time.Second {
				t.Fatal("preview did not resolve authority")
			}
			coordinatorNoWork(t, root)
			// A new coordinator/store instance must resolve the same immutable hit.
			c, err = NewCoordinator(artifact.StoreConfig{Root: root}, coordinatorRenderer(func(context.Context, render.ShotRenderRequest) (render.ShotRenderResult, error) {
				t.Fatal("cache hit invoked renderer")
				return render.ShotRenderResult{}, nil
			}), coordinatorValidator(func(context.Context, validation.Request) (domain.ValidationResult, error) {
				t.Fatal("cache hit invoked validator")
				return domain.ValidationResult{}, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			second, err := c.Build(context.Background(), req)
			if err != nil || !second.CacheHit || second.View.Revision() != first.View.Revision() {
				t.Fatalf("hit: %+v %v", second, err)
			}
			coordinatorNoWork(t, root)
		})
	}
}

func TestCoordinatorPersistsRenderTimingAndUnknownPeak(t *testing.T) {
	c, req, root, renders, validations := coordinatorFixture(t)
	base := c.renderer
	var measured time.Duration
	c.renderer = coordinatorRenderer(func(ctx context.Context, request render.ShotRenderRequest) (render.ShotRenderResult, error) {
		start := time.Now()
		time.Sleep(20 * time.Millisecond)
		result, err := base.RenderShot(ctx, request)
		measured = time.Since(start)
		return result, err
	})
	out, err := c.Build(context.Background(), req)
	if err != nil || out.CacheHit || *renders != 1 || *validations != 1 {
		t.Fatalf("render: %+v %v", out, err)
	}
	entry, hit, err := c.reader.store.Lookup(context.Background(), req.Identity)
	if err != nil || !hit || entry.Generation == nil {
		t.Fatalf("generation result missing: %+v %v", entry, err)
	}
	g := entry.Generation
	if g.DurationMs <= 0 || g.DurationMs < measured.Milliseconds() || g.ID == "" || g.RequestID == "" || g.AssetID != req.Identity.ShotID {
		t.Fatalf("render timing/correlation missing: %+v (render measured %v)", g, measured)
	}
	if g.PeakMemoryMB != nil {
		t.Fatalf("invented peak memory: %v", *g.PeakMemoryMB)
	}
	if entry.Validation == nil || entry.Validation.Status != domain.ValidationPassed || entry.Validation.TargetID != req.Identity.ShotID || entry.Provenance == nil {
		t.Fatal("validation or provenance lost")
	}
	// Read from a reopened store to ensure the measurements are persisted, and
	// verify a hit preserves the original timing instead of measuring the lookup.
	store, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	second, err := c.Build(context.Background(), req)
	loaded, hit, lookupErr := store.Lookup(context.Background(), req.Identity)
	if err != nil || !second.CacheHit || *renders != 1 || *validations != 1 || lookupErr != nil || !hit || !reflect.DeepEqual(entry, loaded) {
		t.Fatalf("hit changed generation evidence: %+v %v %v", second, err, lookupErr)
	}
}

func TestCoordinatorRenderErrorsAndCancellationNeverPublish(t *testing.T) {
	for _, mode := range []string{"error-with-result", "already-canceled", "canceled-error", "late-success", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			c, req, root, renders, validations := coordinatorFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("private-renderer-detail")
			base := c.renderer
			c.renderer = coordinatorRenderer(func(got context.Context, request render.ShotRenderRequest) (render.ShotRenderResult, error) {
				if got != ctx {
					t.Fatal("context was not forwarded")
				}
				result, _ := base.RenderShot(got, request)
				if mode != "error-with-result" {
					cancel()
				}
				if mode == "late-success" {
					return result, nil
				}
				return result, cause
			})
			want := context.Canceled
			switch mode {
			case "error-with-result":
				want = ErrRender
			case "already-canceled":
				cancel()
			case "deadline":
				var end context.CancelFunc
				ctx, end = context.WithDeadline(context.Background(), time.Unix(1, 0))
				defer end()
				want = context.DeadlineExceeded
			}
			out, err := c.Build(ctx, req)
			if !errors.Is(err, want) || !reflect.DeepEqual(out, BuildResult{}) || *validations != 0 {
				t.Fatalf("got %+v %v validations=%d", out, err, *validations)
			}
			if mode == "error-with-result" && (!errors.Is(err, cause) || strings.Contains(err.Error(), cause.Error())) {
				t.Fatal("dependency error must retain cause with safe public text")
			}
			if (mode == "already-canceled" || mode == "deadline") && *renders != 0 {
				t.Fatal("canceled request rendered")
			}
			coordinatorNoEntry(t, c, req, root)
		})
	}
}

func TestCoordinatorRejectsMalformedRendererResults(t *testing.T) {
	for _, mode := range []string{"empty-result", "outside-work", "relative-path", "manifest-path", "shot", "manifest-id", "element-time", "element-bounds", "missing-element", "missing-media", "empty-media", "symlink", "directory", "unsafe-properties"} {
		t.Run(mode, func(t *testing.T) {
			c, req, root, _, _ := coordinatorFixture(t)
			if mode == "unsafe-properties" {
				req.Project.Timeline.Elements[0].EditableProperties["apiToken"] = "private"
			}
			base := c.renderer
			c.renderer = coordinatorRenderer(func(ctx context.Context, request render.ShotRenderRequest) (render.ShotRenderResult, error) {
				result, err := base.RenderShot(ctx, request)
				switch mode {
				case "empty-result":
					return render.ShotRenderResult{}, nil
				case "outside-work":
					result.MediaPath = filepath.Join(root, "source.mp4")
					result.Manifest.MediaPath = result.MediaPath
				case "relative-path":
					result.MediaPath = "shot.mp4"
					result.Manifest.MediaPath = result.MediaPath
				case "manifest-path":
					result.Manifest.MediaPath = "other.mp4"
				case "shot":
					result.Manifest.ShotID = "other"
				case "manifest-id":
					result.Manifest.ID = ""
				case "element-time":
					result.Manifest.Elements[0].End++
				case "element-bounds":
					result.Manifest.Elements[0].Bounds.Width = 0.5
				case "missing-element":
					result.Manifest.Elements = result.Manifest.Elements[:1]
				case "unsafe-properties":
					result.Manifest.Elements[0].EditableProperties["apiToken"] = "private"
				case "empty-media":
					if err := os.Truncate(result.MediaPath, 0); err != nil {
						t.Fatal(err)
					}
				case "missing-media", "symlink", "directory":
					if err := os.Remove(result.MediaPath); err != nil {
						t.Fatal(err)
					}
					if mode == "symlink" {
						if err := os.Symlink(filepath.Join(root, "source.mp4"), result.MediaPath); err != nil {
							t.Fatal(err)
						}
					} else if mode == "directory" {
						if err := os.Mkdir(result.MediaPath, 0700); err != nil {
							t.Fatal(err)
						}
					}
				}
				return result, err
			})
			if out, err := c.Build(context.Background(), req); err == nil || !reflect.DeepEqual(out, BuildResult{}) {
				t.Fatalf("malformed result accepted: %+v %v", out, err)
			}
			coordinatorNoEntry(t, c, req, root)
			if data, err := os.ReadFile(filepath.Join(root, "source.mp4")); err != nil || string(data) != "rendered-media" {
				t.Fatal("caller-owned file modified or removed")
			}
		})
	}
}

func TestCoordinatorMediaVerificationGate(t *testing.T) {
	for _, mode := range []string{"failed", "unknown", "wrong-target", "wrong-id", "failure-class", "retry", "error-with-result", "late-success", "warning"} {
		t.Run(mode, func(t *testing.T) {
			c, req, root, _, _ := coordinatorFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("private-inspection-detail")
			c.validator = coordinatorValidator(func(got context.Context, request validation.Request) (domain.ValidationResult, error) {
				if got != ctx {
					t.Fatal("verification context not forwarded")
				}
				out := domain.ValidationResult{ID: request.ID, TargetID: request.TargetID, Status: domain.ValidationPassed}
				switch mode {
				case "failed":
					out.Status = domain.ValidationFailed
				case "unknown":
					out.Status = "UNKNOWN"
				case "wrong-target":
					out.TargetID = "other"
				case "wrong-id":
					out.ID = "other"
				case "failure-class":
					out.FailureClass = "media"
				case "retry":
					out.RetryRecommended = true
				case "error-with-result":
					return out, cause
				case "late-success":
					cancel()
				case "warning":
					out.Status = domain.ValidationWarning
					out.Details = map[string]any{"rawError": "private"}
				}
				return out, nil
			})
			out, err := c.Build(ctx, req)
			if mode == "warning" {
				if err != nil || out.View.Revision() == "" {
					t.Fatalf("valid warning rejected: %v", err)
				}
				entry, _, _ := c.reader.store.Lookup(context.Background(), req.Identity)
				if entry.Validation.Status != domain.ValidationWarning || entry.Validation.Details != nil {
					t.Fatal("verdict was lost or raw adapter details persisted")
				}
				return
			}
			want := ErrMedia
			if mode == "late-success" {
				want = context.Canceled
			}
			if !errors.Is(err, want) || !reflect.DeepEqual(out, BuildResult{}) {
				t.Fatalf("verification gate: %+v %v", out, err)
			}
			if mode == "error-with-result" && (!errors.Is(err, cause) || strings.Contains(err.Error(), cause.Error())) {
				t.Fatal("unsafe verification error")
			}
			coordinatorNoEntry(t, c, req, root)
		})
	}
}

func TestCoordinatorOwnership(t *testing.T) {
	c, req, root, _, _ := coordinatorFixture(t)
	req.Project.Plan.Shots[0].Primitives = []domain.Primitive{{ID: "primitive", Type: domain.PrimitiveTypeText, Properties: map[string]any{"nested": map[string]any{"values": []any{"original"}}}}}
	req.Input.Shot.Primitives = []artifact.PrimitiveInput{{Type: domain.PrimitiveTypeText}}
	req.Input.Sources = map[string]artifact.Digest{"font": sha256.Sum256([]byte("original-font"))}
	req.Input.Seed = new(string)
	*req.Input.Seed = "0"
	req.Identity.Key, _ = artifact.BuildKey(req.Input)
	wantProject, _ := json.Marshal(req.Project)
	wantSource := req.Input.Sources["font"]
	base := c.renderer
	var returned render.ShotRenderResult
	c.renderer = coordinatorRenderer(func(ctx context.Context, request render.ShotRenderRequest) (render.ShotRenderResult, error) {
		request.Shot.Primitives[0].Properties["nested"].(map[string]any)["values"].([]any)[0] = "renderer-mutation"
		request.Shot.Primitives[0].ID = "changed"
		got, _ := json.Marshal(req.Project)
		if string(got) != string(wantProject) {
			t.Fatal("renderer mutated caller project")
		}
		// Caller changes after the entry snapshot cannot alter retained authority
		// or reproducibility facts used later in this synchronous operation.
		req.Project.Timeline.Elements[0].Bounds.Width = 0.5
		req.Project.Timeline.Elements[0].EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "caller-mutation"
		req.Input.Sources["font"] = sha256.Sum256([]byte("changed"))
		*req.Input.Seed = "changed"
		var err error
		returned, err = base.RenderShot(ctx, request)
		return returned, err
	})
	out, err := c.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	returned.Manifest.Elements[0].Bounds.Width = 0.25
	returned.Manifest.Elements[0].EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "result-mutation"
	entry, hit, err := c.reader.store.Lookup(context.Background(), req.Identity)
	if err != nil || !hit || entry.Provenance.Sources["font"] != wantSource || *entry.Provenance.Seed != "0" {
		t.Fatalf("input projection not owned: %+v %v", entry.Provenance, err)
	}
	element, ok := out.View.Element("text")
	if !ok || element.Visual.Bounds.Width != 1 || element.Visual.EditableProperties["nested"].(map[string]any)["values"].([]any)[0] != "original" {
		t.Fatal("view aliased renderer or caller")
	}
	coordinatorNoWork(t, root)
}

func TestCoordinatorInvalidRequestsAndDependencies(t *testing.T) {
	for _, mode := range []string{"nil-context", "key", "quality", "project", "shot", "duration", "primitive-count", "primitive-type", "unplaced"} {
		t.Run(mode, func(t *testing.T) {
			c, req, root, renders, validations := coordinatorFixture(t)
			ctx := context.Background()
			switch mode {
			case "nil-context":
				ctx = nil
			case "key":
				req.Identity.Key = artifact.Key(strings.Repeat("a", 64))
			case "quality":
				req.Identity.Quality = domain.QualityFinal
			case "project":
				req.Identity.ProjectID = "other"
			case "shot":
				req.Identity.ShotID = "other"
			case "duration":
				req.Input.Shot.Duration++
				req.Identity.Key, _ = artifact.BuildKey(req.Input)
			case "primitive-count":
				req.Input.Shot.Primitives = []artifact.PrimitiveInput{{Type: domain.PrimitiveTypeText}}
				req.Identity.Key, _ = artifact.BuildKey(req.Input)
			case "primitive-type":
				req.Project.Plan.Shots[0].Primitives = []domain.Primitive{{ID: "primitive", Type: domain.PrimitiveTypeImage}}
				req.Input.Shot.Primitives = []artifact.PrimitiveInput{{Type: domain.PrimitiveTypeText}}
				req.Identity.Key, _ = artifact.BuildKey(req.Input)
			case "unplaced":
				req.Project.Timeline.Entries, req.Project.Timeline.Elements = nil, nil
			}
			out, err := c.Build(ctx, req)
			if !errors.Is(err, ErrInvalid) || !reflect.DeepEqual(out, BuildResult{}) || *renders != 0 || *validations != 0 {
				t.Fatalf("invalid request: %+v %v", out, err)
			}
			coordinatorNoWork(t, root)
		})
	}
	c, req, root, _, _ := coordinatorFixture(t)
	for _, renderer := range []render.Renderer{nil, coordinatorRenderer(nil), (*render.FFmpegRenderer)(nil)} {
		if _, err := NewCoordinator(artifact.StoreConfig{Root: root}, renderer, c.validator); !errors.Is(err, ErrInvalid) {
			t.Fatalf("nil renderer: %v", err)
		}
	}
	for _, validator := range []validation.Validator{nil, coordinatorValidator(nil), (*validation.MediaChecker)(nil)} {
		if _, err := NewCoordinator(artifact.StoreConfig{Root: root}, c.renderer, validator); !errors.Is(err, ErrInvalid) {
			t.Fatalf("nil validator: %v", err)
		}
	}
	for _, coordinator := range []*Coordinator{nil, {}} {
		if _, err := coordinator.Build(context.Background(), req); !errors.Is(err, ErrInvalid) {
			t.Fatalf("nil/zero coordinator: %v", err)
		}
	}
}

func TestCoordinatorPreservesDamagedPublishedEntry(t *testing.T) {
	c, req, root, renders, _ := coordinatorFixture(t)
	first, err := c.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(root, filepath.FromSlash(first.View.MediaName()))
	if err := os.WriteFile(media, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Build(context.Background(), req); !errors.Is(err, artifact.ErrEntryConflict) || *renders != 2 {
		t.Fatalf("damaged entry must be a miss and conflict, renders=%d err=%v", *renders, err)
	}
	if data, err := os.ReadFile(media); err != nil || string(data) != "damaged" {
		t.Fatal("coordinator overwrote or removed damaged published entry")
	}
	coordinatorNoWork(t, root)
}

func TestCoordinatorExistingRendererAndNativeVerification(t *testing.T) {
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("native integration requires %s", binary)
		}
	}
	_, req, root, _, _ := coordinatorFixture(t)
	req.Project.Plan.Shots[0].Primitives = []domain.Primitive{{ID: "background", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#0000FF"}}}
	req.Project.Timeline.Elements = []domain.VisualElement{{ID: "background", ShotID: "shot", Type: "Background", End: 4 * time.Second, EditableProperties: map[string]any{"color": "#0000FF"}}}
	req.Input.Shot.Primitives = []artifact.PrimitiveInput{{Type: domain.PrimitiveTypeBackground, Parameters: artifact.Parameters{Strings: map[string]string{"color": "#0000FF"}}}}
	req.Identity.Key, _ = artifact.BuildKey(req.Input)
	inspector, err := validation.NewManagedNativeInspector(validation.DefaultNativeConfig(root))
	if err != nil {
		t.Fatal(err)
	}
	checker, err := validation.NewMediaChecker(inspector, validation.DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	c, err := NewCoordinator(artifact.StoreConfig{Root: root}, render.NewFFmpegRenderer(render.Settings{Width: 640, Height: 360, FPS: 24}), checker)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := c.Build(ctx, req)
	if err != nil || out.View.Revision() == "" || out.CacheHit {
		t.Fatalf("native render/verify/publish failed: %+v %v", out, err)
	}
	coordinatorNoWork(t, root)
	// A new key with non-media output must be rejected by real inspection, even
	// though the renderer claims success and supplies a structurally valid manifest.
	req.Input.Render.Revision = "malformed"
	req.Identity.Key, _ = artifact.BuildKey(req.Input)
	c.renderer = coordinatorRenderer(func(ctx context.Context, request render.ShotRenderRequest) (render.ShotRenderResult, error) {
		media := filepath.Join(request.OutputDir, "shot.mp4")
		if err := os.WriteFile(media, []byte("not video"), 0600); err != nil {
			t.Fatal(err)
		}
		return render.ShotRenderResult{MediaPath: media, Manifest: domain.RenderManifest{ID: "manifest", ShotID: "shot", MediaPath: media, Elements: req.Project.Timeline.Elements}}, nil
	})
	if _, err := c.Build(ctx, req); !errors.Is(err, ErrMedia) {
		t.Fatalf("non-media accepted: %v", err)
	}
	coordinatorNoEntry(t, c, req, root)
}
