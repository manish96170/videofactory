package pipeline

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/checkpoint"
	"github.com/manish96170/videofactory/director"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
	"github.com/manish96170/videofactory/intelligence"
	"github.com/manish96170/videofactory/preview"
	"github.com/manish96170/videofactory/render"
	"github.com/manish96170/videofactory/validation"
)

type testDirector struct{ plan director.PlanResult }

func (d testDirector) Plan(ctx context.Context, req director.PlanRequest) (director.PlanResult, error) {
	if err := ctx.Err(); err != nil {
		return director.PlanResult{}, err
	}
	return d.plan, nil
}

type testAnalyzer struct {
	mu       sync.Mutex
	strategy intelligence.Strategy
	called   int
	mutate   bool
}

func (a *testAnalyzer) Analyze(ctx context.Context, req intelligence.Request) (intelligence.Result, error) {
	if err := ctx.Err(); err != nil {
		return intelligence.Result{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.called++
	if a.mutate && len(req.Project.Plan.Shots[0].Primitives) > 0 {
		req.Project.Plan.Shots[0].Primitives[0].Properties["color"] = "#FFFFFF"
	}
	return intelligence.Result{
		ID: req.ID, ShotID: req.ShotID, Strategy: a.strategy,
		ConceptualPossible: intelligence.Yes, RenderedSupport: intelligence.Yes,
		InputsReady: intelligence.Yes,
	}, nil
}

type memoryCheckpoints struct {
	mu    sync.Mutex
	byKey map[string]checkpoint.Checkpoint
}

func newMemoryCheckpoints() *memoryCheckpoints {
	return &memoryCheckpoints{byKey: map[string]checkpoint.Checkpoint{}}
}

func (m *memoryCheckpoints) Load(ctx context.Context, id checkpoint.Identity) (checkpoint.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return checkpoint.Checkpoint{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cp, ok := m.byKey[checkpointKey(id)]
	if !ok {
		return checkpoint.Checkpoint{}, checkpoint.ErrNotFound
	}
	return cp.Clone(), nil
}

func (m *memoryCheckpoints) Save(ctx context.Context, cp checkpoint.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.byKey[checkpointKey(cp.Identity)] = cp.Clone()
	return nil
}

func checkpointKey(id checkpoint.Identity) string {
	return string(id.ProjectID) + "/" + string(id.Quality) + "/" + string(id.ProjectHash[:])
}

func (m *memoryCheckpoints) only() checkpoint.Checkpoint {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cp := range m.byKey {
		return cp.Clone()
	}
	return checkpoint.Checkpoint{}
}

type testRenderer struct {
	mu       sync.Mutex
	calls    map[domain.ID]int
	failShot domain.ID
	failOnce bool
	cancel   context.CancelFunc
}

func (r *testRenderer) RenderShot(ctx context.Context, req render.ShotRenderRequest) (render.ShotRenderResult, error) {
	r.mu.Lock()
	r.calls[req.Shot.ID]++
	shouldFail := r.failOnce && req.Shot.ID == r.failShot
	if shouldFail {
		r.failOnce = false
	}
	var cancel context.CancelFunc
	if req.Shot.ID == r.failShot {
		cancel = r.cancel
	}
	r.mu.Unlock()
	mediaPath := filepath.Join(req.OutputDir, string(req.Shot.ID)+".mp4")
	if err := os.WriteFile(mediaPath, []byte("deterministic-shot-bytes:"+string(req.Shot.ID)), 0o600); err != nil {
		return render.ShotRenderResult{}, err
	}
	if cancel != nil {
		cancel()
	}
	if shouldFail {
		return render.ShotRenderResult{}, errors.New("render fixture failed")
	}
	var elements []domain.VisualElement
	for _, element := range reqProjectElements(req.Shot, r) {
		elements = append(elements, element)
	}
	return render.ShotRenderResult{MediaPath: mediaPath, Manifest: domain.RenderManifest{
		ID: domain.ID("manifest-" + string(req.Shot.ID)), ShotID: req.Shot.ID, MediaPath: mediaPath,
		Elements: elements, CreatedAt: time.Unix(100, 0).UTC(),
	}}, nil
}

// Renderer fixtures need the same predeclared visual element projection that
// pipeline sends in the project. The single background primitive used below is
// intentionally equivalent to render's exported output contract.
func reqProjectElements(shot domain.Shot, _ *testRenderer) []domain.VisualElement {
	elements := make([]domain.VisualElement, 0, len(shot.Primitives))
	for _, primitive := range shot.Primitives {
		elements = append(elements, visualElement(shot, primitive))
	}
	return elements
}

type testValidator struct {
	mu       sync.Mutex
	statuses map[domain.ID]domain.ValidationStatus
	calls    int
}

func (v *testValidator) Validate(ctx context.Context, req validation.Request) (domain.ValidationResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.ValidationResult{}, err
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.calls++
	status := v.statuses[req.TargetID]
	if status == "" {
		status = domain.ValidationPassed
	}
	return domain.ValidationResult{ID: req.ID, TargetID: req.TargetID, Status: status}, nil
}

type testComposer struct {
	mu      sync.Mutex
	calls   int
	seenIDs []domain.ID
	err     error
	mutate  bool
}

type retryPreview struct {
	PreviewBuilder
	mu        sync.Mutex
	failCount int
	callCount int
	cancel    context.CancelFunc
}

func (p *retryPreview) Build(ctx context.Context, req preview.BuildRequest) (preview.BuildResult, error) {
	p.mu.Lock()
	p.callCount++
	if p.failCount > 0 {
		p.failCount--
		cancel := p.cancel
		p.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return preview.BuildResult{}, errors.Join(ErrRetryable, errors.New("temporary fixture failure"))
	}
	p.mu.Unlock()
	return p.PreviewBuilder.Build(ctx, req)
}

func (c *testComposer) Compose(ctx context.Context, timeline domain.Timeline, shots []Shot, output string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	c.seenIDs = nil
	for _, s := range shots {
		if s.View.Revision() == "" || s.ID != s.View.Identity().ShotID {
			return errors.New("unverified shot passed to composer")
		}
		c.seenIDs = append(c.seenIDs, s.ID)
	}
	if c.mutate && len(timeline.Elements) != 0 {
		timeline.Elements[0].EditableProperties["color"] = "#FFFFFF"
	}
	if c.err != nil {
		return c.err
	}
	return os.WriteFile(output, []byte("composed-mp4-fixture"), 0o600)
}

type observedEvents struct {
	mu    sync.Mutex
	items []event.Event
}

func (p *observedEvents) Publish(ctx context.Context, e event.Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items = append(p.items, e)
	return nil
}

func fixture(t *testing.T, strategy intelligence.Strategy) (*Pipeline, Request, *testRenderer, *testComposer, *memoryCheckpoints) {
	t.Helper()
	root := t.TempDir()
	plan := domain.VideoPlan{
		ID: "director-plan", ProjectID: "project-a", FlowID: "code-tutorial", FramingID: "fullscreen", CreatedAt: time.Unix(1000, 0),
		Shots: []domain.Shot{
			{ID: "director-shot-a", PlanID: "director-plan", Index: 0, Category: domain.ShotCategoryCode, Deterministic: true, Duration: 2 * time.Second, Primitives: []domain.Primitive{{ID: "p-a", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#101020"}}}},
			{ID: "director-shot-b", PlanID: "director-plan", Index: 1, Category: domain.ShotCategoryCode, Deterministic: true, Duration: 3 * time.Second, Primitives: []domain.Primitive{{ID: "p-b", Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#101020"}}}},
		},
	}
	renderer := &testRenderer{calls: make(map[domain.ID]int)}
	validator := &testValidator{statuses: make(map[domain.ID]domain.ValidationStatus)}
	coordinator, err := preview.NewCoordinator(artifact.StoreConfig{Root: root}, renderer, validator)
	if err != nil {
		t.Fatal(err)
	}
	store, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	composer := &testComposer{}
	checkpoints := newMemoryCheckpoints()
	inputs := func(ctx context.Context, _ domain.Project, shot domain.Shot, quality domain.Quality, _ map[domain.ID]artifact.Digest) (artifact.KeyInput, error) {
		if err := ctx.Err(); err != nil {
			return artifact.KeyInput{}, err
		}
		primitives := make([]artifact.PrimitiveInput, 0, len(shot.Primitives))
		for _, primitive := range shot.Primitives {
			primitives = append(primitives, artifact.PrimitiveInput{Type: primitive.Type, Parameters: artifact.Parameters{Strings: map[string]string{"color": primitive.Properties["color"].(string)}}})
		}
		return artifact.KeyInput{
			Quality: quality,
			Shot:    artifact.ShotInput{Duration: shot.Duration, Primitives: primitives},
			Render:  artifact.RenderInput{Revision: "fixture-render-v1", Width: 640, Height: 360, FPS: 24},
			Sources: map[string]artifact.Digest{"fixture": artifact.Digest(sha256.Sum256([]byte("fixture-source")))},
		}, nil
	}
	p, err := New(Dependencies{
		Director:     testDirector{plan: director.PlanResult{Plan: plan, Metadata: director.PlanMetadata{DirectorID: "fixture", DirectorVersion: "1"}}},
		Intelligence: &testAnalyzer{strategy: strategy}, Preview: coordinator, Checkpoints: checkpoints, Artifacts: store,
		Composer: composer, FinalValidator: validator, FinalRoot: root,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := Request{
		Project: domain.Project{ID: "project-a", Name: "test project"}, Prompt: "Make a deterministic tutorial", Quality: domain.QualityDraft,
		OutputPath: "final.mp4", Inputs: inputs,
	}
	return p, req, renderer, composer, checkpoints
}

func TestRunDeterministicPipelineAndIncrementalCache(t *testing.T) {
	p, req, renderer, composer, checkpoints := fixture(t, intelligence.Deterministic)
	observed := &observedEvents{}
	p.deps.Events = observed
	first, err := p.Run(context.Background(), req)
	if err != nil || first.Status != StatusCompleted || len(first.Shots) != 2 || filepath.Base(first.FinalPath) != req.OutputPath || first.FinalCheck == nil {
		t.Fatalf("first run: status=%s shots=%d final=%q err=%v", first.Status, len(first.Shots), first.FinalPath, err)
	}
	if renderer.callsForTest() != 2 || composer.calls != 1 {
		t.Fatalf("first run work: renders=%d composes=%d", renderer.callsForTest(), composer.calls)
	}
	if first.Timeline.Entries[1].Start != 2*time.Second || first.Timeline.Entries[1].End != 5*time.Second {
		t.Fatalf("timeline is not sequential: %+v", first.Timeline.Entries)
	}
	for _, s := range first.Shots {
		if s.CacheHit || s.View.Revision() == "" {
			t.Fatalf("initial shot should be newly rendered and verified: %+v", s)
		}
	}
	for _, evt := range observed.items {
		if evt.Type == event.TypeShotInvalidated {
			t.Fatalf("initial cache miss must not invalidate a prior shot: %+v", evt)
		}
	}
	cp := checkpoints.only()
	if len(cp.Shots) != 2 || cp.Shots[0].State != checkpoint.Complete || cp.Shots[1].State != checkpoint.Complete || cp.Shots[0].ArtifactDigest == (artifact.Digest{}) {
		t.Fatalf("completion evidence not checkpointed: %+v", cp.Shots)
	}

	req.OutputPath = "final-resumed.mp4"
	second, err := p.Run(context.Background(), req)
	if err != nil || second.Status != StatusCompleted || renderer.callsForTest() != 2 || composer.calls != 2 {
		t.Fatalf("resume/cache run: status=%s renders=%d compose=%d err=%v", second.Status, renderer.callsForTest(), composer.calls, err)
	}
	for _, s := range second.Shots {
		if !s.CacheHit {
			t.Fatalf("expected a verified cache hit after resume: %+v", s)
		}
	}
}

func TestPartialFailureResumesWithoutRerenderingVerifiedShot(t *testing.T) {
	p, req, renderer, composer, checkpoints := fixture(t, intelligence.Deterministic)
	// Use the stable ID observed by the prepared request to fail the second item.
	firstPlan, err := p.deps.Director.Plan(context.Background(), director.PlanRequest{ProjectID: req.Project.ID, Prompt: req.Prompt})
	if err != nil {
		t.Fatal(err)
	}
	firstPlan, err = clonePlanResult(firstPlan)
	if err != nil {
		t.Fatal(err)
	}
	stable := stablePlan(req.Project.ID, req.Prompt, firstPlan.Plan)
	renderer.failShot = stable.Shots[1].ID
	renderer.failOnce = true
	partial, err := p.Run(context.Background(), req)
	if !errors.Is(err, preview.ErrRender) || partial.Status != StatusPartial || len(partial.Shots) != 1 || partial.FinalPath != "" || composer.calls != 0 {
		t.Fatalf("expected partial failure: shots=%d final=%q compose=%d err=%v", len(partial.Shots), partial.FinalPath, composer.calls, err)
	}
	cp := checkpoints.only()
	if cp.Shots[0].State != checkpoint.Complete || cp.Shots[1].State != checkpoint.Pending {
		t.Fatalf("partial checkpoint lost work state: %+v", cp.Shots)
	}
	firstID, secondID := cp.Shots[0].ShotID, cp.Shots[1].ShotID
	if renderer.calls[firstID] != 1 || renderer.calls[secondID] != 1 {
		t.Fatalf("unexpected initial render counts: %+v", renderer.calls)
	}

	resumed, err := p.Run(context.Background(), req)
	if err != nil || resumed.Status != StatusCompleted || renderer.calls[firstID] != 1 || renderer.calls[secondID] != 2 {
		t.Fatalf("resume should reuse first and rebuild second: %+v calls=%+v err=%v", resumed, renderer.calls, err)
	}
	if !resumed.Shots[0].CacheHit || resumed.Shots[1].CacheHit {
		t.Fatalf("expected first cache hit and second miss: %+v", resumed.Shots)
	}
}

func TestPixelInputChangeInvalidatesOnlyAffectedShot(t *testing.T) {
	p, req, renderer, _, _ := fixture(t, intelligence.Deterministic)
	observed := &observedEvents{}
	p.deps.Events = observed
	first, err := p.Run(context.Background(), req)
	if err != nil || first.Status != StatusCompleted {
		t.Fatalf("initial render: %v", err)
	}
	firstIDs := []domain.ID{first.Shots[0].ID, first.Shots[1].ID}
	base := req.Inputs
	req.Inputs = func(ctx context.Context, proj domain.Project, shot domain.Shot, quality domain.Quality, dependencies map[domain.ID]artifact.Digest) (artifact.KeyInput, error) {
		if len(dependencies) != 0 {
			t.Fatal("unexpected shot dependencies")
		}
		input, err := base(ctx, proj, shot, quality, dependencies)
		if err != nil {
			return artifact.KeyInput{}, err
		}
		if shot.Index == 1 {
			input.Sources["fixture"] = artifact.Digest(sha256.Sum256([]byte("changed-source")))
		}
		return input, nil
	}
	req.OutputPath = "final-changed-input.mp4"
	second, err := p.Run(context.Background(), req)
	if err != nil || second.Status != StatusCompleted {
		t.Fatalf("incremental rebuild: %v", err)
	}
	if renderer.calls[firstIDs[0]] != 1 || renderer.calls[firstIDs[1]] != 2 {
		t.Fatalf("change did not stay scoped to affected shot: %+v", renderer.calls)
	}
	if !second.Shots[0].CacheHit || second.Shots[1].CacheHit {
		t.Fatalf("unexpected incremental cache status: %+v", second.Shots)
	}
	var invalidated []domain.ID
	for _, evt := range observed.items {
		if evt.Type == event.TypeShotInvalidated {
			invalidated = append(invalidated, evt.ShotID)
		}
	}
	if !reflect.DeepEqual(invalidated, []domain.ID{second.Shots[1].ID}) {
		t.Fatalf("expected only the prior completed changed-key shot to be invalidated: %v", invalidated)
	}
}

func TestShotDependencyDigestInvalidatesDownstreamArtifact(t *testing.T) {
	p, req, renderer, _, _ := fixture(t, intelligence.Deterministic)
	observed := &observedEvents{}
	p.deps.Events = observed
	directorFixture := p.deps.Director.(testDirector)
	initialPlan, err := clonePlanResult(directorFixture.plan)
	if err != nil {
		t.Fatal(err)
	}
	initialPlan.Plan.Shots[1].DependsOn = []domain.ID{initialPlan.Plan.Shots[0].ID}
	p.deps.Director = testDirector{plan: initialPlan}
	baseInputs := req.Inputs
	dependencyInputs := 0
	req.Inputs = func(ctx context.Context, proj domain.Project, shot domain.Shot, quality domain.Quality, dependencies map[domain.ID]artifact.Digest) (artifact.KeyInput, error) {
		if shot.Index == 1 {
			upstreamID := proj.Plan.Shots[0].ID
			if dependencies[upstreamID] == (artifact.Digest{}) {
				t.Fatal("downstream input did not receive verified upstream media digest")
			}
			dependencyInputs++
		}
		return baseInputs(ctx, proj, shot, quality, dependencies)
	}
	first, err := p.Run(context.Background(), req)
	if err != nil || first.Status != StatusCompleted || dependencyInputs != 1 {
		t.Fatalf("initial dependent render: status=%s dependencyInputs=%d err=%v", first.Status, dependencyInputs, err)
	}
	oldUpstream, downstream := first.Shots[0].ID, first.Shots[1].ID

	changedPlan, err := clonePlanResult(initialPlan)
	if err != nil {
		t.Fatal(err)
	}
	changedPlan.Plan.Shots[0].Primitives[0].Properties["color"] = "#202020"
	p.deps.Director = testDirector{plan: changedPlan}
	req.OutputPath = "final-changed-dependency.mp4"
	second, err := p.Run(context.Background(), req)
	if err != nil || second.Status != StatusCompleted {
		t.Fatalf("dependent incremental render: %+v err=%v", second, err)
	}
	if second.Shots[0].ID == oldUpstream || second.Shots[1].ID != downstream {
		t.Fatalf("unexpected stable shot identities: first=%+v second=%+v", first.Shots, second.Shots)
	}
	if renderer.calls[oldUpstream] != 1 || renderer.calls[second.Shots[0].ID] != 1 || renderer.calls[downstream] != 2 {
		t.Fatalf("dependency change did not invalidate downstream shot: %+v", renderer.calls)
	}
	var invalidated []domain.ID
	for _, evt := range observed.items {
		if evt.Type == event.TypeShotInvalidated {
			invalidated = append(invalidated, evt.ShotID)
		}
	}
	if len(invalidated) != 0 {
		t.Fatalf("new project revision has no matching prior completed checkpoint keys to invalidate: %+v", invalidated)
	}
}

func TestCancellationPreservesCompletedCheckpointForResume(t *testing.T) {
	p, req, renderer, composer, checkpoints := fixture(t, intelligence.Deterministic)
	firstPlan, _ := p.deps.Director.Plan(context.Background(), director.PlanRequest{ProjectID: req.Project.ID, Prompt: req.Prompt})
	firstPlan, _ = clonePlanResult(firstPlan)
	stable := stablePlan(req.Project.ID, req.Prompt, firstPlan.Plan)
	ctx, cancel := context.WithCancel(context.Background())
	renderer.failShot = stable.Shots[1].ID
	renderer.cancel = cancel
	partial, err := p.Run(ctx, req)
	if !errors.Is(err, context.Canceled) || partial.Status != StatusPartial || len(partial.Shots) != 1 || partial.FinalPath != "" || composer.calls != 0 {
		t.Fatalf("cancellation fabricated or lost partial result: %+v err=%v", partial, err)
	}
	renderer.cancel = nil
	cp := checkpoints.only()
	if cp.Shots[0].State != checkpoint.Complete || cp.Shots[1].State != checkpoint.Pending {
		t.Fatalf("cancellation discarded completed evidence: %+v", cp.Shots)
	}
	resumed, err := p.Run(context.Background(), req)
	if err != nil || resumed.Status != StatusCompleted || !resumed.Shots[0].CacheHit {
		t.Fatalf("canceled run did not resume cleanly: %+v err=%v", resumed, err)
	}
}

func TestLocalCloudAndMixedStrategiesStopAtApprovalBoundary(t *testing.T) {
	for _, strategy := range []ExecutionStrategy{StrategyLocal, StrategyCloud, StrategyMixed} {
		t.Run(string(strategy), func(t *testing.T) {
			p, req, renderer, composer, _ := fixture(t, intelligence.Deterministic)
			req.Strategy = strategy
			req.Inputs = nil
			req.OutputPath = ""
			out, err := p.Run(context.Background(), req)
			if err != nil || out.Status != StatusPendingApproval || len(out.Pending) != 1 || out.Pending[0].Strategy != strategy || !out.Pending[0].ApprovalRequired {
				t.Fatalf("missing typed approval requirement: %+v err=%v", out, err)
			}
			if renderer.callsForTest() != 0 || composer.calls != 0 || out.FinalPath != "" {
				t.Fatal("non-deterministic strategy executed work")
			}
		})
	}
}

func TestIntelligenceNonDeterministicResultIsPendingNotExecuted(t *testing.T) {
	p, req, renderer, composer, _ := fixture(t, intelligence.GenerateVideo)
	out, err := p.Run(context.Background(), req)
	if err != nil || out.Status != StatusPendingApproval || len(out.Pending) != 1 || !out.Pending[0].ApprovalRequired || out.Pending[0].Strategy != StrategyUnspecified {
		t.Fatalf("non-deterministic analysis was not gated: %+v err=%v", out, err)
	}
	if renderer.callsForTest() != 0 || composer.calls != 0 || out.FinalPath != "" {
		t.Fatal("analysis advisory triggered execution")
	}
}

func TestRunRejectsUnverifiedFinalOutput(t *testing.T) {
	p, req, _, composer, _ := fixture(t, intelligence.Deterministic)
	composer.err = errors.New("composition failed")
	out, err := p.Run(context.Background(), req)
	if !errors.Is(err, ErrComposition) || out.FinalPath != "" || out.Status == StatusCompleted {
		t.Fatalf("composition error produced a final artifact: %+v err=%v", out, err)
	}
}

func TestPipelineSnapshotsInjectedInputsAndTimeline(t *testing.T) {
	p, req, _, composer, _ := fixture(t, intelligence.Deterministic)
	composer.mutate = true
	p.deps.Intelligence.(*testAnalyzer).mutate = true
	originalInput := req.Inputs
	req.Inputs = func(ctx context.Context, proj domain.Project, shot domain.Shot, quality domain.Quality, dependencies map[domain.ID]artifact.Digest) (artifact.KeyInput, error) {
		if len(dependencies) != 0 {
			t.Fatal("unexpected shot dependencies")
		}
		if proj.Plan.Shots[0].Primitives[0].Properties["color"] != "#101020" {
			t.Fatal("earlier callback mutated project snapshot")
		}
		return originalInput(ctx, proj, shot, quality, dependencies)
	}
	out, err := p.Run(context.Background(), req)
	if err != nil || out.Status != StatusCompleted {
		t.Fatalf("pipeline failed: %v", err)
	}
	if out.Project.Timeline.Elements[0].EditableProperties["color"] != "#101020" {
		t.Fatal("injected callback mutation escaped its detached project/timeline")
	}
}

func TestEventsAreScalarObservationsAndBestEffort(t *testing.T) {
	p, req, _, _, _ := fixture(t, intelligence.Deterministic)
	observed := &observedEvents{}
	p.deps.Events = observed
	out, err := p.Run(context.Background(), req)
	if err != nil || out.Status != StatusCompleted || len(observed.items) == 0 {
		t.Fatalf("event-enabled run: %v events=%d", err, len(observed.items))
	}
	for _, e := range observed.items {
		if len(e.Payload) != 1 || e.Payload["code"] == nil {
			t.Fatalf("event contains unexpected payload: %+v", e)
		}
	}
	// Mutating an observation must not mutate authoritative pipeline output.
	observed.items[0].Payload["code"] = "changed"
	if out.Status != StatusCompleted || out.Project.ID != req.Project.ID {
		t.Fatal("event payload aliased pipeline state")
	}
}

func TestStablePlanAndCheckpointProjectHashAcrossDirectorRunMetadata(t *testing.T) {
	p, req, _, _, checkpoints := fixture(t, intelligence.Deterministic)
	one, err := p.Run(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	first := checkpoints.only()
	p.deps.Director = testDirector{plan: director.PlanResult{Plan: func() domain.VideoPlan {
		plan := one.Plan.Plan
		plan.CreatedAt = time.Now()
		for i := range plan.Shots {
			plan.Shots[i].ID = domain.ID("ephemeral-shot-" + string(rune('a'+i)))
			plan.Shots[i].PlanID = plan.ID
			for j := range plan.Shots[i].Primitives {
				plan.Shots[i].Primitives[j].ID = domain.ID("ephemeral-primitive")
			}
		}
		return plan
	}()}}
	req.OutputPath = "final-stable-rerun.mp4"
	if _, err := p.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	second := checkpoints.only()
	if first.Identity.ProjectHash != second.Identity.ProjectHash || !reflect.DeepEqual(first.Shots, second.Shots) {
		t.Fatal("equivalent rerun did not preserve deterministic identity/checkpoint keys")
	}
}

func TestNewRejectsNilAndTypedNilDependencies(t *testing.T) {
	var typedNil *testAnalyzer
	p, _, _, _, _ := fixture(t, intelligence.Deterministic)
	deps := p.deps
	deps.Intelligence = typedNil
	_, err := New(deps)
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("typed-nil dependency accepted: %v", err)
	}
}

func TestRetryPolicyRetriesOnlyExplicitTransientFailures(t *testing.T) {
	p, req, renderer, _, _ := fixture(t, intelligence.Deterministic)
	p.deps.Retry = RetryPolicy{MaxAttempts: 2}
	retrying := &retryPreview{PreviewBuilder: p.deps.Preview, failCount: 1}
	p.deps.Preview = retrying
	out, err := p.Run(context.Background(), req)
	if err != nil || out.Status != StatusCompleted || retrying.callCount != 3 || renderer.callsForTest() != 2 {
		t.Fatalf("single explicit transient retry failed: status=%s attempts=%d renders=%d err=%v", out.Status, retrying.callCount, renderer.callsForTest(), err)
	}

	p2, req2, _, _, _ := fixture(t, intelligence.Deterministic)
	p2.deps.Retry = RetryPolicy{MaxAttempts: 3}
	permanent := &permanentPreview{err: errors.New("permanent")}
	p2.deps.Preview = permanent
	_, err = p2.Run(context.Background(), req2)
	if err == nil || errors.Is(err, ErrRetryable) || permanent.calls != 1 {
		t.Fatalf("unmarked permanent error was retried or returned incorrectly: calls=%d err=%v", permanent.calls, err)
	}

	p3, req3, renderer3, _, _ := fixture(t, intelligence.Deterministic)
	p3.deps.Retry = RetryPolicy{MaxAttempts: 2}
	exhausted := &retryPreview{PreviewBuilder: p3.deps.Preview, failCount: 10}
	p3.deps.Preview = exhausted
	_, err = p3.Run(context.Background(), req3)
	if !errors.Is(err, ErrRetryable) || exhausted.callCount != 2 || renderer3.callsForTest() != 0 {
		t.Fatalf("retry attempts were not bounded: calls=%d renders=%d err=%v", exhausted.callCount, renderer3.callsForTest(), err)
	}
}

type permanentPreview struct {
	calls int
	err   error
}

func (p *permanentPreview) Build(context.Context, preview.BuildRequest) (preview.BuildResult, error) {
	p.calls++
	return preview.BuildResult{}, p.err
}

func TestRetryWaitHonorsCancellation(t *testing.T) {
	p, req, _, _, _ := fixture(t, intelligence.Deterministic)
	ctx, cancel := context.WithCancel(context.Background())
	retrying := &retryPreview{PreviewBuilder: p.deps.Preview, failCount: 1, cancel: cancel}
	p.deps.Preview = retrying
	p.deps.Retry = RetryPolicy{MaxAttempts: 3, Delay: time.Second}
	out, err := p.Run(ctx, req)
	if !errors.Is(err, context.Canceled) || out.FinalPath != "" || retrying.callCount != 1 {
		t.Fatalf("retry did not stop on cancellation: calls=%d final=%q err=%v", retrying.callCount, out.FinalPath, err)
	}
}

func TestFinalOutputRejectsTraversalAndSymlinkOverwrite(t *testing.T) {
	p, req, _, composer, _ := fixture(t, intelligence.Deterministic)
	for _, name := range []string{"../escape.mp4", "nested/final.mp4", filepath.Join(t.TempDir(), "outside.mp4"), ".hidden.mp4"} {
		req.OutputPath = name
		if _, err := p.Run(context.Background(), req); !errors.Is(err, ErrInvalid) {
			t.Errorf("output path %q was not rejected: %v", name, err)
		}
	}
	if composer.calls != 0 {
		t.Fatalf("unsafe path reached composer: calls=%d", composer.calls)
	}

	outside := filepath.Join(t.TempDir(), "sentinel.mp4")
	if err := os.WriteFile(outside, []byte("must remain untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(p.finalRoot, "final.mp4")
	if err := os.Symlink(outside, target); err != nil {
		t.Fatal(err)
	}
	req.OutputPath = "final.mp4"
	if _, err := p.Run(context.Background(), req); !errors.Is(err, ErrFinalMedia) {
		t.Fatalf("symlink destination was accepted: %v", err)
	}
	if info, err := os.Lstat(target); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("symlink destination was replaced")
	}
	if got, err := os.ReadFile(outside); err != nil || string(got) != "must remain untouched" {
		t.Fatal("symlink target was overwritten")
	}
}

func TestRenderTimelineComposerProducesRealFFmpegOutput(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg is unavailable")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe is unavailable")
	}
	p, req, _, _, _ := fixture(t, intelligence.Deterministic)
	store := p.deps.Artifacts.(*artifact.Store)
	checker := &testValidator{statuses: make(map[domain.ID]domain.ValidationStatus)}
	renderer := render.NewFFmpegRenderer(render.Settings{Width: 640, Height: 360, FPS: 24})
	coordinator, err := preview.NewCoordinator(artifact.StoreConfig{Root: p.finalRoot}, renderer, checker)
	if err != nil {
		t.Fatal(err)
	}
	composer, err := NewRenderTimelineComposer(store)
	if err != nil {
		t.Fatal(err)
	}
	p.deps.Preview, p.deps.Composer = coordinator, composer
	out, err := p.Run(context.Background(), req)
	if err != nil || out.Status != StatusCompleted {
		t.Fatalf("real FFmpeg pipeline: status=%s err=%v", out.Status, err)
	}
	probe, err := render.Probe(context.Background(), out.FinalPath)
	if err != nil || probe.Width != 640 || probe.Height != 360 || probe.Duration < 4900*time.Millisecond || probe.Duration > 5100*time.Millisecond {
		t.Fatalf("composed media was not a real expected MP4: probe=%+v err=%v", probe, err)
	}
}

func (r *testRenderer) callsForTest() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, n := range r.calls {
		count += n
	}
	return count
}

var _ EventPublisher = (*observedEvents)(nil)
var _ ArtifactLookup = (*artifact.Store)(nil)
