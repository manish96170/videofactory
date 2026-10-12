package pipeline

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/checkpoint"
	"github.com/manish96170/videofactory/director"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/intelligence"
	"github.com/manish96170/videofactory/preview"
)

const largeFixtureShots = 96

func largeFixture(tb testing.TB, shotCount int) (*Pipeline, Request, *testRenderer, *testComposer, *memoryCheckpoints, *testAnalyzer) {
	tb.Helper()
	root := tb.TempDir()
	plan := domain.VideoPlan{
		ID: "large-fixture-plan", ProjectID: "project-large", FlowID: "code-tutorial", FramingID: "fullscreen", CreatedAt: time.Unix(1000, 0).UTC(),
		Shots: make([]domain.Shot, 0, shotCount),
	}
	for i := 0; i < shotCount; i++ {
		plan.Shots = append(plan.Shots, domain.Shot{
			ID: domain.ID(fmt.Sprintf("large-shot-%03d", i)), PlanID: plan.ID, Index: i,
			Category: domain.ShotCategoryCode, Deterministic: true, Duration: time.Second,
			Primitives: []domain.Primitive{{ID: domain.ID(fmt.Sprintf("large-primitive-%03d", i)), Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#101020"}}},
		})
	}
	renderer := &testRenderer{calls: make(map[domain.ID]int)}
	validator := &testValidator{statuses: make(map[domain.ID]domain.ValidationStatus)}
	coordinator, err := preview.NewCoordinator(artifact.StoreConfig{Root: root}, renderer, validator)
	if err != nil {
		tb.Fatal(err)
	}
	store, err := artifact.NewStore(artifact.StoreConfig{Root: root})
	if err != nil {
		tb.Fatal(err)
	}
	composer := &testComposer{}
	checkpoints := newMemoryCheckpoints()
	analyzer := &testAnalyzer{strategy: intelligence.Deterministic}
	inputs := func(ctx context.Context, _ domain.Project, shot domain.Shot, quality domain.Quality, _ map[domain.ID]artifact.Digest) (artifact.KeyInput, error) {
		if err := ctx.Err(); err != nil {
			return artifact.KeyInput{}, err
		}
		primitives := make([]artifact.PrimitiveInput, 0, len(shot.Primitives))
		for _, primitive := range shot.Primitives {
			color, ok := primitive.Properties["color"].(string)
			if !ok {
				return artifact.KeyInput{}, ErrInvalid
			}
			primitives = append(primitives, artifact.PrimitiveInput{Type: primitive.Type, Parameters: artifact.Parameters{Strings: map[string]string{"color": color}}})
		}
		return artifact.KeyInput{
			Quality: quality,
			Shot:    artifact.ShotInput{Duration: shot.Duration, Primitives: primitives},
			Render:  artifact.RenderInput{Revision: "large-fixture-render-v1", Width: 640, Height: 360, FPS: 24},
			Sources: map[string]artifact.Digest{"fixture": artifact.Digest(sha256.Sum256([]byte("large-fixture-source")))},
		}, nil
	}
	p, err := New(Dependencies{
		Director:     testDirector{plan: director.PlanResult{Plan: plan, Metadata: director.PlanMetadata{DirectorID: "large-fixture", DirectorVersion: "1"}}},
		Intelligence: analyzer, Preview: coordinator, Checkpoints: checkpoints, Artifacts: store,
		Composer: composer, FinalValidator: validator, FinalRoot: root,
	})
	if err != nil {
		tb.Fatal(err)
	}
	req := Request{
		Project: domain.Project{ID: plan.ProjectID, Name: "large deterministic fixture"},
		Prompt:  "Render a deterministic large fixture", Quality: domain.QualityDraft,
		OutputPath: "large-final.mp4", Inputs: inputs,
	}
	return p, req, renderer, composer, checkpoints, analyzer
}

func TestLargeProjectCacheEffectivenessAndMeasuredFixtureBaseline(t *testing.T) {
	p, req, renderer, composer, _, _ := largeFixture(t, largeFixtureShots)

	coldStart := time.Now()
	cold, err := p.Run(context.Background(), req)
	coldDuration := time.Since(coldStart)
	if err != nil || cold.Status != StatusCompleted || len(cold.Shots) != largeFixtureShots {
		t.Fatalf("cold large-project run: status=%s shots=%d err=%v", cold.Status, len(cold.Shots), err)
	}
	if got := renderer.callsForTest(); got != largeFixtureShots {
		t.Fatalf("cold fixture render count=%d, want %d", got, largeFixtureShots)
	}
	for _, shot := range cold.Shots {
		if shot.CacheHit {
			t.Fatalf("cold fixture unexpectedly hit cache: %s", shot.ID)
		}
	}

	req.OutputPath = "large-final-warm.mp4"
	warmStart := time.Now()
	warm, err := p.Run(context.Background(), req)
	warmDuration := time.Since(warmStart)
	if err != nil || warm.Status != StatusCompleted || len(warm.Shots) != largeFixtureShots {
		t.Fatalf("warm large-project run: status=%s shots=%d err=%v", warm.Status, len(warm.Shots), err)
	}
	for _, shot := range warm.Shots {
		if !shot.CacheHit {
			t.Fatalf("warm fixture missed cache: %s", shot.ID)
		}
	}
	if got := renderer.callsForTest(); got != largeFixtureShots {
		t.Fatalf("warm cache performed additional renders: total=%d baseline=%d", got, largeFixtureShots)
	}
	if composer.calls != 2 {
		t.Fatalf("cache should avoid shot rendering but still compose each final: calls=%d", composer.calls)
	}
	t.Logf("deterministic-fixture baseline: shots=%d cold=%s warm=%s renders=%d warm-cache-hits=%d", largeFixtureShots, coldDuration, warmDuration, renderer.callsForTest(), len(warm.Shots))
}

func TestConcurrentRepeatedRunsShareVerifiedArtifacts(t *testing.T) {
	const concurrentRuns = 6
	p, req, renderer, _, _, _ := largeFixture(t, 24)
	start := make(chan struct{})
	type outcome struct {
		result Result
		err    error
	}
	results := make(chan outcome, concurrentRuns)
	for i := 0; i < concurrentRuns; i++ {
		i := i
		go func() {
			<-start
			local := req
			local.OutputPath = fmt.Sprintf("concurrent-%02d.mp4", i)
			result, err := p.Run(context.Background(), local)
			results <- outcome{result: result, err: err}
		}()
	}
	close(start)
	for i := 0; i < concurrentRuns; i++ {
		select {
		case got := <-results:
			if got.err != nil || got.result.Status != StatusCompleted || len(got.result.Shots) != 24 {
				t.Fatalf("concurrent pipeline result: status=%s shots=%d err=%v", got.result.Status, len(got.result.Shots), got.err)
			}
			if info, err := osStatRegular(got.result.FinalPath); err != nil || info.Size() == 0 {
				t.Fatalf("final output not published: info=%v err=%v", info, err)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("concurrent pipeline runs did not finish")
		}
	}

	// A post-contention run has a deterministic cache oracle regardless of which
	// concurrent caller won each initial artifact publication.
	req.OutputPath = "concurrent-warm.mp4"
	warm, err := p.Run(context.Background(), req)
	if err != nil || warm.Status != StatusCompleted {
		t.Fatalf("warm run after contention: status=%s err=%v", warm.Status, err)
	}
	for _, shot := range warm.Shots {
		if !shot.CacheHit {
			t.Fatalf("post-contention cache miss: %s", shot.ID)
		}
	}
	before := renderer.callsForTest()
	req.OutputPath = "concurrent-warm-again.mp4"
	again, err := p.Run(context.Background(), req)
	if err != nil || again.Status != StatusCompleted || renderer.callsForTest() != before {
		t.Fatalf("repeated shared-cache run rendered again: renders=%d before=%d err=%v", renderer.callsForTest(), before, err)
	}
}

func TestLargeProjectCancellationRetainsPrefixAndRecoversFromCache(t *testing.T) {
	const completedBeforeCancel = 19
	p, req, renderer, _, checkpoints, analyzer := largeFixture(t, 48)
	planResult, err := p.deps.Director.Plan(context.Background(), director.PlanRequest{ProjectID: req.Project.ID, Prompt: req.Prompt})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := clonePlanResult(planResult)
	if err != nil {
		t.Fatal(err)
	}
	stable := stablePlan(req.Project.ID, req.Prompt, owned.Plan)
	target := stable.Shots[completedBeforeCancel].ID
	ctx, cancel := context.WithCancel(context.Background())
	p.deps.Intelligence = cancelAfterAnalyzer{inner: analyzer, target: target, cancel: cancel}

	partial, err := p.Run(ctx, req)
	if !errors.Is(err, context.Canceled) || partial.Status != StatusPartial || len(partial.Shots) != completedBeforeCancel || partial.FinalPath != "" {
		t.Fatalf("large cancellation result: status=%s completed=%d final=%q err=%v", partial.Status, len(partial.Shots), partial.FinalPath, err)
	}
	if got := renderer.callsForTest(); got != completedBeforeCancel {
		t.Fatalf("canceled run rendered %d shots, want completed prefix %d", got, completedBeforeCancel)
	}
	cp := checkpoints.only()
	for i := 0; i < completedBeforeCancel; i++ {
		shot, ok := checkpointShot(cp, stable.Shots[i].ID)
		if !ok || shot.State != checkpoint.Complete || shot.ArtifactDigest == (artifact.Digest{}) {
			t.Fatalf("completed prefix evidence lost at %d: %+v, found=%t", i, shot, ok)
		}
	}
	if pending, ok := checkpointShot(cp, target); !ok || pending.State != checkpoint.Pending {
		t.Fatalf("cancel boundary not retained as pending: %+v found=%t", pending, ok)
	}

	p.deps.Intelligence = analyzer
	req.OutputPath = "large-final-recovered.mp4"
	recovered, err := p.Run(context.Background(), req)
	if err != nil || recovered.Status != StatusCompleted || len(recovered.Shots) != 48 {
		t.Fatalf("recovery run: status=%s shots=%d err=%v", recovered.Status, len(recovered.Shots), err)
	}
	for i := 0; i < completedBeforeCancel; i++ {
		if !recovered.Shots[i].CacheHit {
			t.Fatalf("recovery rerendered completed prefix shot %d", i)
		}
	}
	for i := completedBeforeCancel; i < len(recovered.Shots); i++ {
		if recovered.Shots[i].CacheHit {
			t.Fatalf("unbuilt suffix shot %d unexpectedly hit cache", i)
		}
	}
	if got := renderer.callsForTest(); got != 48 {
		t.Fatalf("recovery render total=%d, want one render per project shot", got)
	}
}

type cancelAfterAnalyzer struct {
	inner  ShotAnalyzer
	target domain.ID
	cancel context.CancelFunc
}

func (a cancelAfterAnalyzer) Analyze(ctx context.Context, req intelligence.Request) (intelligence.Result, error) {
	result, err := a.inner.Analyze(ctx, req)
	if err == nil && req.ShotID == a.target {
		a.cancel()
	}
	return result, err
}

func osStatRegular(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("not a regular file")
	}
	return info, nil
}

func BenchmarkPipelineLargeDeterministicWarmCache(b *testing.B) {
	p, req, renderer, _, _, _ := largeFixture(b, largeFixtureShots)
	req.OutputPath = "benchmark-warmup.mp4"
	if out, err := p.Run(context.Background(), req); err != nil || out.Status != StatusCompleted {
		b.Fatalf("warm benchmark fixture: status=%s err=%v", out.Status, err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req.OutputPath = fmt.Sprintf("benchmark-%06d.mp4", i)
		out, err := p.Run(context.Background(), req)
		if err != nil || out.Status != StatusCompleted || len(out.Shots) != largeFixtureShots {
			b.Fatalf("warm cache iteration %d: status=%s err=%v", i, out.Status, err)
		}
	}
	b.StopTimer()
	if got := renderer.callsForTest(); got != largeFixtureShots {
		b.Fatalf("warm benchmark rendered after fixture setup: total calls=%d want=%d", got, largeFixtureShots)
	}
	b.ReportMetric(largeFixtureShots, "shots/op")
	b.ReportMetric(0, "renders/op")
}
