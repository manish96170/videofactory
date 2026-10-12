package changeset_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manish96170/videofactory/changeset"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
)

var now = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

func fixture() domain.Project {
	return domain.Project{ID: "project", Name: "Demo", CreatedAt: now, UpdatedAt: now,
		Plan: &domain.VideoPlan{ID: "plan", ProjectID: "project", FlowID: "flow", FramingID: "frame", CreatedAt: now,
			Shots: []domain.Shot{
				{ID: "a", PlanID: "plan", Index: 0, Category: domain.ShotCategoryUI, Deterministic: true, Duration: 2 * time.Second,
					Primitives: []domain.Primitive{
						{ID: "title", Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": "Old", "color": "#ffffff", "fontSize": 48, "bounds": &domain.Bounds{Width: 1, Height: 1}}},
						{ID: "bg", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "000000"}},
					}},
				{ID: "b", PlanID: "plan", Index: 1, Category: domain.ShotCategoryUI, Duration: 2 * time.Second, DependsOn: []domain.ID{"a"}},
				{ID: "c", PlanID: "plan", Index: 2, Category: domain.ShotCategoryUI, Duration: 2 * time.Second, DependsOn: []domain.ID{"b"}},
				{ID: "d", PlanID: "plan", Index: 3, Category: domain.ShotCategoryUI, Duration: 2 * time.Second},
			}},
		Timeline: &domain.Timeline{ID: "timeline", ProjectID: "project", Entries: []domain.TimelineEntry{
			{ShotID: "a", Start: 0, End: 2 * time.Second}, {ShotID: "b", Start: 2 * time.Second, End: 4 * time.Second},
			{ShotID: "c", Start: 4 * time.Second, End: 6 * time.Second}, {ShotID: "d", Start: 6 * time.Second, End: 8 * time.Second}},
			Elements: []domain.VisualElement{{ID: "title", ShotID: "a", Type: "Text", Start: 0, End: 2 * time.Second,
				Bounds: &domain.Bounds{Width: 1, Height: 1}, EditableProperties: map[string]any{"text": "Old", "color": "#ffffff", "fontSize": 48}}}},
		Assets: []domain.Asset{{ID: "asset", Kind: domain.AssetKindImage, URI: "image.png", Metadata: map[string]any{
			"nested": map[string]any{"values": []any{"keep", map[string]string{"key": "value"}}}, "large": int64(9007199254740993)}}},
	}
}

func editText(s string) domain.Change {
	return domain.Change{TargetID: "title", Op: changeset.OpSetText, Payload: map[string]any{"shotId": "a", "text": s}}
}

func proposal(t *testing.T, p domain.Project, changes ...domain.Change) changeset.Proposal {
	t.Helper()
	h, err := changeset.ProjectHash(p)
	if err != nil {
		t.Fatal(err)
	}
	return changeset.Proposal{BaseHash: h, ChangeSet: domain.ChangeSet{ID: "edit", ProjectID: p.ID, Source: "manual", Changes: changes, Status: domain.ChangeSetProposed, CreatedAt: now}}
}

func validated(t *testing.T, p domain.Project, changes ...domain.Change) *changeset.Validated {
	t.Helper()
	v, err := changeset.Validate(context.Background(), p, proposal(t, p, changes...))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestApplyAtomicCopiedTransformationAndInvalidation(t *testing.T) {
	p := fixture()
	original, _ := json.Marshal(p)
	v := validated(t, p, editText("New"), domain.Change{TargetID: "bg", Op: changeset.OpSetColor, Payload: map[string]any{"shotId": "a", "color": "#123456"}},
		domain.Change{TargetID: "title", Op: changeset.OpSetFontSize, Payload: map[string]any{"shotId": "a", "fontSize": float64(64)}})
	preview, err := v.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if preview.ChangeSet.Status != domain.ChangeSetProposed || !reflect.DeepEqual(preview.ChangedRoots, []domain.ID{"a"}) ||
		!reflect.DeepEqual(preview.InvalidatedShots, []domain.ID{"a", "b", "c"}) || preview.Impact.ResourceEstimateKnown {
		t.Fatalf("bad preview: %+v", preview)
	}
	r, err := changeset.Apply(context.Background(), p, v)
	if err != nil {
		t.Fatal(err)
	}
	if project.Validate(r.Project) != nil || r.Project.Plan.Shots[0].Primitives[0].Properties["text"] != "New" ||
		r.Project.Plan.Shots[0].Primitives[1].Properties["color"] != "#123456" ||
		r.Project.Plan.Shots[0].Primitives[0].Properties["fontSize"] != float64(64) ||
		r.Project.Timeline.Elements[0].EditableProperties["text"] != "New" {
		t.Fatalf("incorrect applied project: %+v", r.Project)
	}
	if r.Project.Assets[0].Metadata["large"] != int64(9007199254740993) {
		t.Fatal("integer precision/type lost")
	}
	h, _ := changeset.ProjectHash(r.Project)
	if h != preview.ResultHash || r.Preview.ChangeSet.Status != domain.ChangeSetApplied {
		t.Fatal("result hash/status")
	}
	if !reflect.DeepEqual(r.Project.Plan.Shots[3], p.Plan.Shots[3]) {
		t.Fatal("unaffected shot changed")
	}
	after, _ := json.Marshal(p)
	if string(after) != string(original) {
		t.Fatal("caller mutated")
	}
	// Egress ownership includes maps, nested slices, typed maps and pointers.
	r.Project.Plan.Shots[0].Primitives[0].Properties["bounds"].(*domain.Bounds).Width = .25
	r.Project.Timeline.Elements[0].Bounds.Width = .25
	r.Project.Assets[0].Metadata["nested"].(map[string]any)["values"].([]any)[1].(map[string]string)["key"] = "bad"
	r.Preview.ChangeSet.Changes[0].Payload["text"] = "bad"
	r.Preview.InvalidatedShots[0] = "bad"
	after, _ = json.Marshal(p)
	if string(after) != string(original) {
		t.Fatal("result aliases caller")
	}
	saved, _ := v.Snapshot()
	if saved.ChangeSet.Changes[0].Payload["text"] != "New" || saved.InvalidatedShots[0] != "a" {
		t.Fatal("result aliases handle")
	}
	if _, err := changeset.Apply(context.Background(), p, v); !errors.Is(err, changeset.ErrState) {
		t.Fatalf("repeat apply: %v", err)
	}
	if err := v.Cancel(context.Background()); !errors.Is(err, changeset.ErrState) {
		t.Fatalf("cancel applied: %v", err)
	}
}

func TestProjectNameHasNoRenderInvalidation(t *testing.T) {
	p := fixture()
	v := validated(t, p, domain.Change{TargetID: p.ID, Op: changeset.OpSetProjectName, Payload: map[string]any{"name": "Renamed"}})
	r, err := changeset.Apply(context.Background(), p, v)
	if err != nil {
		t.Fatal(err)
	}
	if r.Project.Name != "Renamed" || len(r.Preview.InvalidatedShots) != 0 || r.Preview.Impact.ChangedShots != 0 {
		t.Fatal("name edit invalidates render")
	}
}

func TestValidationRejectsUnsafeAndFutureMutationsAtomically(t *testing.T) {
	tests := []struct {
		name   string
		change domain.Change
		want   error
	}{
		{"future-provider", domain.Change{TargetID: "a", Op: "set-provider", Payload: map[string]any{"provider": "cloud"}}, changeset.ErrUnsupported},
		{"arbitrary-path", domain.Change{TargetID: "title", Op: "set-property", Payload: map[string]any{"assetPath": "remote"}}, changeset.ErrUnsupported},
		{"timeline-retime", domain.Change{TargetID: "title", Op: "shift-element", Payload: map[string]any{"delta": -1}}, changeset.ErrUnsupported},
		{"extra-key", domain.Change{TargetID: "title", Op: changeset.OpSetText, Payload: map[string]any{"shotId": "a", "text": "X", "command": "exec"}}, changeset.ErrInvalid},
		{"missing-shot", domain.Change{TargetID: "title", Op: changeset.OpSetText, Payload: map[string]any{"text": "X"}}, changeset.ErrInvalid},
		{"wrong-shot", domain.Change{TargetID: "title", Op: changeset.OpSetText, Payload: map[string]any{"shotId": "b", "text": "X"}}, changeset.ErrInvalid},
		{"wrong-type", domain.Change{TargetID: "bg", Op: changeset.OpSetText, Payload: map[string]any{"shotId": "a", "text": "X"}}, changeset.ErrUnsupported},
		{"metadata-only-target", domain.Change{TargetID: "ghost", Op: changeset.OpSetText, Payload: map[string]any{"shotId": "a", "text": "X"}}, changeset.ErrInvalid},
		{"invalid-color", domain.Change{TargetID: "bg", Op: changeset.OpSetColor, Payload: map[string]any{"shotId": "a", "color": "red;exec"}}, changeset.ErrInvalid},
		{"too-large-font", domain.Change{TargetID: "title", Op: changeset.OpSetFontSize, Payload: map[string]any{"shotId": "a", "fontSize": 513}}, changeset.ErrInvalid},
		{"nan", domain.Change{TargetID: "title", Op: changeset.OpSetFontSize, Payload: map[string]any{"shotId": "a", "fontSize": math.NaN()}}, changeset.ErrInvalid},
		{"empty-text", editText("   "), changeset.ErrInvalid},
		{"huge-text", editText(strings.Repeat("x", changeset.MaxTextBytes+1)), changeset.ErrInvalid},
		{"nul-text", editText("bad\x00value"), changeset.ErrInvalid},
		{"invalid-utf8", editText(string([]byte{0xff})), changeset.ErrInvalid},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture()
			p.Timeline.Elements = append(p.Timeline.Elements, domain.VisualElement{ID: "ghost", ShotID: "a", Type: "Text", Start: 0, End: time.Second})
			before, _ := json.Marshal(p)
			v, err := changeset.Validate(context.Background(), p, proposal(t, p, editText("First valid edit"), tc.change))
			if !errors.Is(err, tc.want) || v != nil {
				t.Fatalf("got %v, %v", v, err)
			}
			after, _ := json.Marshal(p)
			if string(after) != string(before) {
				t.Fatal("partial mutation leaked")
			}
		})
	}
	p := fixture()
	for _, cs := range [][]domain.Change{nil, {editText("Old")}, {editText("First"), editText("Second")}} {
		if _, err := changeset.Validate(context.Background(), p, proposal(t, p, cs...)); !errors.Is(err, changeset.ErrInvalid) {
			t.Fatalf("empty/no-op/duplicate accepted: %v", err)
		}
	}
}

func TestStaleWholeProjectAndForgedLifecycle(t *testing.T) {
	for _, status := range []domain.ChangeSetStatus{domain.ChangeSetApproved, domain.ChangeSetApplied, domain.ChangeSetRejected, "future"} {
		p := fixture()
		prop := proposal(t, p, editText("New"))
		prop.ChangeSet.Status = status
		if _, err := changeset.Validate(context.Background(), p, prop); !errors.Is(err, changeset.ErrInvalid) {
			t.Fatalf("forged status: %v", err)
		}
	}
	mutators := map[string]func(*domain.Project){
		"name":       func(p *domain.Project) { p.Name = "Changed" },
		"asset":      func(p *domain.Project) { p.Assets[0].URI = "other.png" },
		"metadata":   func(p *domain.Project) { p.Assets[0].Metadata["new"] = true },
		"timestamp":  func(p *domain.Project) { p.UpdatedAt = p.UpdatedAt.Add(time.Second) },
		"dependency": func(p *domain.Project) { p.Plan.Shots[3].DependsOn = []domain.ID{"a"} },
		"visual":     func(p *domain.Project) { p.Timeline.Elements[0].Bounds.Width = .5 },
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			p := fixture()
			prop := proposal(t, p, editText("New"))
			v := validated(t, p, editText("New"))
			mutate(&p)
			if _, err := changeset.Validate(context.Background(), p, prop); !errors.Is(err, changeset.ErrStale) {
				t.Fatalf("validation not stale: %v", err)
			}
			r, err := changeset.Apply(context.Background(), p, v)
			if !errors.Is(err, changeset.ErrStale) || r.Project.ID != "" {
				t.Fatalf("stale apply returned result: %v", err)
			}
			s, _ := v.Snapshot()
			if s.ChangeSet.Status != domain.ChangeSetProposed {
				t.Fatal("stale apply consumed proposal")
			}
			if _, err := changeset.Apply(context.Background(), fixture(), v); err != nil {
				t.Fatal(err)
			}
		})
	}
	var zero changeset.Validated
	for _, v := range []*changeset.Validated{nil, &zero} {
		if _, err := changeset.Apply(context.Background(), fixture(), v); !errors.Is(err, changeset.ErrInvalid) {
			t.Fatal(err)
		}
	}
}

func TestProposalAndPreviewOwnership(t *testing.T) {
	p := fixture()
	prop := proposal(t, p, editText("New"))
	v, err := changeset.Validate(context.Background(), p, prop)
	if err != nil {
		t.Fatal(err)
	}
	prop.ChangeSet.Changes[0].Payload["text"] = "Mutated"
	p.Plan.Shots[0].Primitives[0].Properties["text"] = "Mutated"
	s, _ := v.Snapshot()
	s.ChangeSet.Changes[0].Payload["text"] = "Mutated snapshot"
	s.ChangedRoots[0] = "bad"
	r, err := changeset.Apply(context.Background(), fixture(), v)
	if err != nil {
		t.Fatal(err)
	}
	if r.Project.Plan.Shots[0].Primitives[0].Properties["text"] != "New" || r.Preview.ChangedRoots[0] != "a" {
		t.Fatal("ingress or preview aliases state")
	}
}

func TestCanonicalHashAndUnsupportedProjectValues(t *testing.T) {
	p := fixture()
	p.Assets[0].Metadata = map[string]any{"a": 1, "b": 2}
	q := fixture()
	q.Assets[0].Metadata = map[string]any{"b": float64(2), "a": float64(1)}
	q.CreatedAt = q.CreatedAt.In(time.FixedZone("offset", 3600))
	q.Plan.CreatedAt = q.Plan.CreatedAt.In(time.FixedZone("offset", 3600))
	h, err := changeset.ProjectHash(p)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := changeset.ProjectHash(q)
	if err != nil || h != h2 {
		t.Fatalf("not canonical: %s %s %v", h, h2, err)
	}
	data, err := project.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	round, err := project.Unmarshal(data)
	if err != nil {
		t.Fatal(err)
	}
	h3, err := changeset.ProjectHash(round)
	if err != nil || h != h3 {
		t.Fatalf("roundtrip hash: %v", err)
	}
	cyclic := map[string]any{}
	cyclic["cycle"] = cyclic
	for _, v := range []any{math.Inf(1), func() {}, make(chan int), cyclic} {
		bad := fixture()
		bad.Assets[0].Metadata["bad"] = v
		if _, err := changeset.ProjectHash(bad); !errors.Is(err, changeset.ErrInvalid) {
			t.Fatalf("unsupported value: %v", err)
		}
	}
	bad := fixture()
	bad.Name = string([]byte{0xff})
	if _, err := changeset.ProjectHash(bad); !errors.Is(err, changeset.ErrInvalid) {
		t.Fatal("UTF8 collision accepted")
	}
}

func TestProjectHashSurvivesPersistenceWithLargeExtensionIntegers(t *testing.T) {
	for _, value := range []int64{1<<53 + 1, math.MaxInt64} {
		p := fixture()
		p.Assets[0].Metadata["large"] = value
		p.Plan.Shots[0].Primitives[0].Properties["extension"] = map[string]any{"nested": []int64{value}}
		p.Timeline.Elements[0].EditableProperties["extension"] = value
		prop := proposal(t, p, editText("New"))
		v := validated(t, p, editText("New"))
		data, err := project.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		round, err := project.Unmarshal(data)
		if err != nil {
			t.Fatal(err)
		}
		h, err := changeset.ProjectHash(round)
		if err != nil || h != prop.BaseHash {
			t.Fatalf("marshal/unmarshal changed hash for %d: %s != %s (%v)", value, h, prop.BaseHash, err)
		}
		store, err := project.NewFileStore(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Save(context.Background(), p); err != nil {
			t.Fatal(err)
		}
		loaded, err := store.Load(context.Background(), p.ID)
		if err != nil {
			t.Fatal(err)
		}
		h, err = changeset.ProjectHash(loaded)
		if err != nil || h != prop.BaseHash {
			t.Fatalf("save/load changed hash for %d: %s != %s (%v)", value, h, prop.BaseHash, err)
		}
		if _, err := changeset.Validate(context.Background(), loaded, prop); err != nil {
			t.Fatalf("persistence made proposal stale: %v", err)
		}
		result, err := changeset.Apply(context.Background(), loaded, v)
		if err != nil {
			t.Fatalf("persistence prevented apply: %v", err)
		}
		if result.Project.Plan.Shots[0].Primitives[0].Properties["text"] != "New" {
			t.Fatal("edit not applied after reload")
		}
		h, err = changeset.ProjectHash(result.Project)
		if err != nil || h != result.Preview.ResultHash {
			t.Fatalf("result hash mismatch after reload: %v", err)
		}
	}
}

func TestCancellationAndConcurrentLifecycle(t *testing.T) {
	p := fixture()
	v := validated(t, p, editText("New"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := changeset.Validate(ctx, p, proposal(t, p, editText("New"))); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := changeset.Apply(ctx, p, v); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := v.Cancel(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := changeset.Validate(nil, p, proposal(t, p, editText("New"))); !errors.Is(err, changeset.ErrInvalid) {
		t.Fatal(err)
	}
	if err := v.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	copyHandle := *v
	if err := copyHandle.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := changeset.Apply(context.Background(), p, &copyHandle); !errors.Is(err, changeset.ErrState) {
		t.Fatalf("cancel bypass through copy: %v", err)
	}

	for iteration := 0; iteration < 10; iteration++ {
		v = validated(t, p, editText("Concurrent"))
		var wg sync.WaitGroup
		var applied atomic.Int32
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				copyHandle := *v
				if i == 0 {
					_ = copyHandle.Cancel(context.Background())
					return
				}
				if _, err := changeset.Apply(context.Background(), p, &copyHandle); err == nil {
					applied.Add(1)
				} else if !errors.Is(err, changeset.ErrState) {
					t.Error(err)
				}
				_, _ = v.Snapshot()
				_, _ = v.Audit()
			}(i)
		}
		wg.Wait()
		if applied.Load() > 1 {
			t.Fatal("applied more than once")
		}
		s, _ := v.Snapshot()
		if s.ChangeSet.Status == domain.ChangeSetProposed {
			t.Fatal("lifecycle never completed")
		}
	}
}

func TestBoundedSafeAudit(t *testing.T) {
	p := fixture()
	v := validated(t, p, editText("SECRET prompt payload"))
	stale := fixture()
	stale.Name = "stale"
	for i := 0; i < changeset.MaxAuditRecords+5; i++ {
		_, _ = changeset.Apply(context.Background(), stale, v)
	}
	audit, dropped := v.Audit()
	if len(audit) != changeset.MaxAuditRecords || dropped != 6 {
		t.Fatalf("bounds: %d %d", len(audit), dropped)
	}
	for i, a := range audit {
		if a.Code != "STALE" || a.Sequence != uint64(i)+dropped+1 {
			t.Fatalf("record %+v", a)
		}
	}
	data, _ := json.Marshal(audit)
	if strings.Contains(string(data), "SECRET") {
		t.Fatal("audit leaked payload")
	}
	audit[0].Code = "bad"
	again, _ := v.Audit()
	if again[0].Code != "STALE" {
		t.Fatal("audit aliases state")
	}
}

// This cooperative context deterministically cancels at the final checkpoint,
// exercising cancellation after transformation/copying without timing sleeps.
type checkpointContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *checkpointContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestCancellationBeforeCommitReturnsNoPartialResult(t *testing.T) {
	p := fixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Validate checks ingress, each mutation, then the final completed snapshot.
	v, err := changeset.Validate(&checkpointContext{Context: ctx, cancel: cancel, remaining: 3}, p, proposal(t, p, editText("New")))
	if !errors.Is(err, context.Canceled) || v != nil {
		t.Fatalf("late validate: %v %v", v, err)
	}
	v = validated(t, p, editText("New"))
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	r, err := changeset.Apply(&checkpointContext{Context: ctx2, cancel: cancel2, remaining: 3}, p, v)
	if !errors.Is(err, context.Canceled) || r.Project.ID != "" {
		t.Fatalf("late apply: %+v %v", r, err)
	}
	s, _ := v.Snapshot()
	if s.ChangeSet.Status != domain.ChangeSetProposed {
		t.Fatal("late cancellation consumed proposal")
	}
	if p.Plan.Shots[0].Primitives[0].Properties["text"] != "Old" {
		t.Fatal("cancelled transformation leaked")
	}
	if _, err := changeset.Apply(context.Background(), p, v); err != nil {
		t.Fatal(err)
	}
}
