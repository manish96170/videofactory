package preview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
)

func TestCoordinatorValidNonIDChangesRequireRerender(t *testing.T) {
	mutations := map[string]func(*domain.VisualElement){
		"bounds":           func(e *domain.VisualElement) { e.Bounds.Width = 0.5 },
		"bounds-removed":   func(e *domain.VisualElement) { e.Bounds = nil },
		"start":            func(e *domain.VisualElement) { e.Start++ },
		"end":              func(e *domain.VisualElement) { e.End-- },
		"type":             func(e *domain.VisualElement) { e.Type = "Image" },
		"property-added":   func(e *domain.VisualElement) { e.EditableProperties["text"] = "changed" },
		"property-removed": func(e *domain.VisualElement) { delete(e.EditableProperties, "nested") },
		"nested-property": func(e *domain.VisualElement) {
			e.EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "changed"
		},
	}
	for name, mutate := range mutations {
		for _, rename := range []bool{false, true} {
			label := name
			if rename {
				label += "-and-id"
			}
			t.Run(label, func(t *testing.T) {
				c, req, root, renders, validations := coordinatorFixture(t)
				first, err := c.Build(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				base, _, _ := c.reader.store.Lookup(context.Background(), req.Identity)
				mutate(&req.Project.Timeline.Elements[0])
				if rename {
					req.Project.Timeline.Elements[0].ID = "new-text"
				}
				if err := project.Validate(req.Project); err != nil {
					t.Fatalf("regression must use valid authority: %v", err)
				}
				if _, err := artifact.ManifestDigest(req.Identity, req.Project.Timeline.Elements); err != nil {
					t.Fatalf("regression must use valid metadata: %v", err)
				}
				before, _ := json.Marshal(req.Project)
				v, hit, err := c.reader.Resolve(context.Background(), req.Project, req.Identity)
				if hit || !reflect.DeepEqual(v, View{}) || !errors.Is(err, artifact.ErrRerenderRequired) || !errors.Is(err, ErrManifest) {
					t.Fatalf("reader must require rerender: %+v %v %v", v, hit, err)
				}
				out, err := c.Build(context.Background(), req)
				if !reflect.DeepEqual(out, BuildResult{}) || !errors.Is(err, artifact.ErrRerenderRequired) || !strings.Contains(err.Error(), "rerender required") {
					t.Fatalf("non-ID change rebound: %+v %v", out, err)
				}
				if *renders != 1 || *validations != 1 {
					t.Fatal("mismatched metadata invoked renderer/validator")
				}
				if _, err := os.Lstat(filepath.Join(root, "bindings")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("non-ID change published binding")
				}
				after, _ := json.Marshal(req.Project)
				loaded, hit, err := c.reader.store.Lookup(context.Background(), req.Identity)
				if string(before) != string(after) || err != nil || !hit || !reflect.DeepEqual(base, loaded) || !reflect.DeepEqual(first.View.Manifest(), base.Manifest) {
					t.Fatal("rejected rebind changed caller or base artifact")
				}
				coordinatorNoWork(t, root)
			})
		}
	}
}

func TestCoordinatorIDOnlyEditRebindsVerifiedMedia(t *testing.T) {
	c, req, root, renders, validations := coordinatorFixture(t)
	req.Project.Plan.Shots[0].Primitives = []domain.Primitive{{ID: "text", Type: domain.PrimitiveTypeText}}
	req.Input.Shot.Primitives = []artifact.PrimitiveInput{{Type: domain.PrimitiveTypeText}}
	req.Identity.Key, _ = artifact.BuildKey(req.Input)
	inputBefore, _ := json.Marshal(req.Input)
	first, err := c.Build(context.Background(), req)
	if err != nil || first.CacheHit {
		t.Fatalf("initial build: %+v %v", first, err)
	}
	entryBefore, _, _ := c.reader.store.Lookup(context.Background(), req.Identity)
	filesBefore := make(map[string]string)
	for _, name := range []string{entryBefore.Media.Name, entryBefore.ManifestFile.Name, filepath.ToSlash(filepath.Join(filepath.Dir(entryBefore.Media.Name), "entry.json"))} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		filesBefore[name] = string(data)
	}
	req.Project.Plan.Shots[0].Primitives[0].ID = "new-text"
	req.Project.Timeline.Elements[0].ID = "new-text"
	if _, hit, err := c.reader.Resolve(context.Background(), req.Project, req.Identity); hit || !errors.Is(err, ErrManifest) {
		t.Fatalf("unbound authority must not resolve: %v %v", hit, err)
	}
	second, err := c.Build(context.Background(), req)
	if err != nil || !second.CacheHit || *renders != 1 || *validations != 1 {
		t.Fatalf("ID-only edit rendered instead of rebinding: %+v %v renders=%d validations=%d", second, err, *renders, *validations)
	}
	inputAfter, _ := json.Marshal(req.Input)
	key, _ := artifact.BuildKey(req.Input)
	if string(inputBefore) != string(inputAfter) || key != req.Identity.Key || second.View.Identity() != first.View.Identity() || second.View.MediaName() != first.View.MediaName() {
		t.Fatal("ID-only edit changed pixel input, key or media identity")
	}
	if second.View.Revision() == first.View.Revision() || second.View.ManifestName() == first.View.ManifestName() {
		t.Fatal("metadata version did not change")
	}
	if _, ok := second.View.Element("new-text"); !ok {
		t.Fatal("new element missing")
	}
	if _, ok := second.View.Element("text"); ok {
		t.Fatal("old element exposed")
	}
	if _, ok := first.View.Element("text"); !ok {
		t.Fatal("old snapshot changed")
	}
	entryAfter, _, _ := c.reader.store.Lookup(context.Background(), req.Identity)
	if !reflect.DeepEqual(entryBefore, entryAfter) {
		t.Fatal("immutable entry changed")
	}
	for name, want := range filesBefore {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(data) != want {
			t.Fatal("published bytes changed")
		}
	}
	// Restart, reordered authority, and returned nested mutations cannot select a
	// stale version or alter the persisted metadata binding.
	store, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	reader, _ := NewReader(store)
	req.Project.Timeline.Elements[0], req.Project.Timeline.Elements[1] = req.Project.Timeline.Elements[1], req.Project.Timeline.Elements[0]
	reloaded := resolve(t, reader, req.Project, req.Identity)
	if reloaded.Revision() != second.View.Revision() {
		t.Fatal("binding not stable across restart/order")
	}
	// Multiple authorities for identical pixels coexist; selecting an older
	// authoritative project never follows a mutable latest-version pointer.
	req.Project.Timeline.Elements[1].ID = "third-text"
	req.Project.Plan.Shots[0].Primitives[0].ID = "third-text"
	third, err := c.Build(context.Background(), req)
	if err != nil || !third.CacheHit || *renders != 1 || third.View.Revision() == second.View.Revision() {
		t.Fatalf("third authority: %+v %v", third, err)
	}
	req.Project.Timeline.Elements[1].ID = "new-text"
	req.Project.Plan.Shots[0].Primitives[0].ID = "new-text"
	if v := resolve(t, reader, req.Project, req.Identity); v.Revision() != second.View.Revision() {
		t.Fatal("newer binding displaced requested authority")
	}
	m := second.View.Manifest()
	m.Elements[0].Bounds.Width = 0.5
	m.Elements[0].EditableProperties["nested"].(map[string]any)["values"].([]any)[0] = "mutated"
	req.Project.Timeline.Elements[1].Bounds.Width = 0.25
	e, _ := reloaded.Element("new-text")
	if e.Visual.Bounds.Width != 1 || e.Visual.EditableProperties["nested"].(map[string]any)["values"].([]any)[0] != "original" {
		t.Fatal("binding view aliases caller/output")
	}
	coordinatorNoWork(t, root)
}

func TestCoordinatorMalformedAuthorityCannotBind(t *testing.T) {
	for _, mode := range []string{"duplicate", "interval", "outside-shot", "bounds", "wrong-shot", "path-id", "raw-context", "secret", "path"} {
		t.Run(mode, func(t *testing.T) {
			c, req, root, renders, _ := coordinatorFixture(t)
			if _, err := c.Build(context.Background(), req); err != nil {
				t.Fatal(err)
			}
			e := &req.Project.Timeline.Elements[0]
			e.ID = "new-text"
			switch mode {
			case "duplicate":
				e.ID = req.Project.Timeline.Elements[1].ID
			case "interval":
				e.End = e.Start
			case "outside-shot":
				e.End = req.Project.Plan.Shots[0].Duration + 1
			case "bounds":
				e.Bounds.Width = 2
			case "wrong-shot":
				e.ShotID = "unknown"
			case "path-id":
				e.ID = "../secret"
			case "raw-context":
				e.EditableProperties["live"] = context.Background()
			case "secret":
				e.EditableProperties["apiToken"] = "private"
			case "path":
				e.EditableProperties["location"] = "/private/file"
			}
			if out, err := c.Build(context.Background(), req); err == nil || !reflect.DeepEqual(out, BuildResult{}) || *renders != 1 {
				t.Fatalf("malformed authority accepted/rendered: %+v %v count=%d", out, err, *renders)
			}
			if _, err := os.Lstat(filepath.Join(root, "bindings")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("malformed authority wrote bindings")
			}
		})
	}
}

func TestCoordinatorDamagedBindingAndCancellationPreserveMedia(t *testing.T) {
	c, req, root, renders, _ := coordinatorFixture(t)
	first, err := c.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.Project.Timeline.Elements[0].ID = "new-text"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Build(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "bindings")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled rebind published")
	}
	second, err := c.Build(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, filepath.FromSlash(second.View.ManifestName()))
	if err := os.WriteFile(name, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, hit, err := c.reader.Resolve(context.Background(), req.Project, req.Identity); hit || !errors.Is(err, ErrManifest) {
		t.Fatalf("corrupt binding resolved: %v %v", hit, err)
	}
	if _, err := c.Build(context.Background(), req); !errors.Is(err, artifact.ErrEntryConflict) || *renders != 1 {
		t.Fatalf("corrupt binding rendered/replaced: %v count=%d", err, *renders)
	}
	if data, err := os.ReadFile(name); err != nil || string(data) != "damaged" {
		t.Fatal("damaged binding replaced")
	}
	req.Project.Timeline.Elements[0].ID = "text"
	if v := resolve(t, c.reader, req.Project, req.Identity); v.Revision() != first.View.Revision() {
		t.Fatal("original metadata damaged")
	}
	coordinatorNoWork(t, root)
}

type bindingHookContext struct {
	context.Context
	hook func()
}

func (c bindingHookContext) Err() error { c.hook(); return c.Context.Err() }

func TestCoordinatorCancellationAfterBindingCommit(t *testing.T) {
	c, req, root, renders, _ := coordinatorFixture(t)
	if _, err := c.Build(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	req.Project.Timeline.Elements[0].ID = "new-text"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	committed := false
	hooked := bindingHookContext{ctx, func() {
		_ = filepath.WalkDir(filepath.Join(root, "bindings"), func(name string, d os.DirEntry, err error) error {
			if err == nil && d.Name() == "entry.json" && !strings.HasPrefix(filepath.Base(filepath.Dir(name)), ".stage-") {
				committed = true
				cancel()
			}
			return err
		})
	}}
	if got, err := c.Build(hooked, req); !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, BuildResult{}) || !committed {
		t.Fatalf("post-commit cancellation: %+v %v committed=%v", got, err, committed)
	}
	if _, hit, err := c.reader.store.LookupManifest(context.Background(), req.Identity, req.Project.Timeline.Elements); err != nil || !hit {
		t.Fatalf("committed binding rolled back: %v %v", hit, err)
	}
	if got, err := c.Build(context.Background(), req); err != nil || !got.CacheHit || *renders != 1 {
		t.Fatalf("retry did not reuse binding: %+v %v renders=%d", got, err, *renders)
	}
	coordinatorNoWork(t, root)
}
