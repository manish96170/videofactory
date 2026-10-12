package preview

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/render"
	"github.com/manish96170/videofactory/validation"
)

func TestPersistedValidationRequiredForPreviewAndCacheHit(t *testing.T) {
	for _, mode := range []string{"unvalidated", "failed", "failure-class", "retry", "wrong-target", "passed", "warning"} {
		for _, rebind := range []bool{false, true} {
			name := mode
			if rebind {
				name += "-rebind"
			}
			t.Run(name, func(t *testing.T) {
				r, p, req, root := fixture(t)
				switch mode {
				case "unvalidated":
					req.Validation = nil
				case "failed":
					req.Validation.Status = domain.ValidationFailed
				case "failure-class":
					req.Validation.FailureClass = "media"
				case "retry":
					req.Validation.RetryRecommended = true
				case "wrong-target":
					// The generic store permits validation of a generated asset;
					// that is not evidence that this preview's shot was accepted.
					req.Generation = &domain.GenerationResult{ID: "generation", RequestID: "request", AssetID: "other"}
					req.Validation.TargetID = "other"
				case "warning":
					req.Validation.Status = domain.ValidationWarning
				}
				original := publish(t, r, req)
				if rebind {
					p.Timeline.Elements[0].ID = "new-text"
				}
				accepted := mode == "passed" || mode == "warning"
				v, hit, err := r.Resolve(context.Background(), p, req.Identity)
				if !accepted {
					if hit || !reflect.DeepEqual(v, View{}) || !errors.Is(err, ErrMedia) {
						t.Fatalf("unaccepted entry exposed: %+v hit=%v err=%v", v, hit, err)
					}
				} else if rebind {
					if hit || !errors.Is(err, ErrManifest) {
						t.Fatalf("expected missing binding: hit=%v err=%v", hit, err)
					}
				} else if err != nil || !hit {
					t.Fatalf("accepted entry rejected: hit=%v err=%v", hit, err)
				}
				c, err := NewCoordinator(artifact.StoreConfig{Root: root}, coordinatorRenderer(func(context.Context, render.ShotRenderRequest) (render.ShotRenderResult, error) {
					t.Fatal("existing entry must either be reused or rejected before rendering")
					return render.ShotRenderResult{}, nil
				}), coordinatorValidator(func(context.Context, validation.Request) (domain.ValidationResult, error) {
					t.Fatal("persisted entry must not be silently revalidated")
					return domain.ValidationResult{}, nil
				}))
				if err != nil {
					t.Fatal(err)
				}
				build := BuildRequest{Project: p, Identity: req.Identity, Input: artifact.KeyInput{Quality: req.Identity.Quality, Shot: artifact.ShotInput{Duration: p.Plan.Shots[0].Duration}, Render: artifact.RenderInput{Revision: "r1", Width: 640, Height: 360, FPS: 24}}}
				out, err := c.Build(context.Background(), build)
				if accepted {
					if err != nil || !out.CacheHit || out.View.Revision() == "" {
						t.Fatalf("accepted entry not reused: %+v %v", out, err)
					}
				} else {
					if !errors.Is(err, ErrMedia) || !reflect.DeepEqual(out, BuildResult{}) {
						t.Fatalf("false cache hit from unaccepted entry: %+v %v", out, err)
					}
					if _, err := os.Lstat(filepath.Join(root, "bindings")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("unaccepted entry published a metadata binding")
					}
				}
				loaded, hit, err := r.store.Lookup(context.Background(), req.Identity)
				if err != nil || !hit || !reflect.DeepEqual(original, loaded) {
					t.Fatal("preview changed the persisted entry")
				}
				coordinatorNoWork(t, root)
			})
		}
	}
}
