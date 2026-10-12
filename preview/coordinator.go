package preview

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
	"github.com/manish96170/videofactory/render"
	"github.com/manish96170/videofactory/validation"
)

var (
	ErrRender       = errors.New("preview: render failed")
	ErrRenderResult = errors.New("preview: invalid render result")
	ErrMedia        = errors.New("preview: media verification failed")
)

// Coordinator performs one synchronous cache lookup/render/publish/resolve.
// The renderer and validator are trusted, context-cooperative dependencies. They
// must not retain or asynchronously mutate requests/results or output files.
// Concurrent calls require concurrency-safe dependencies; each miss gets its own
// output directory. Concurrent misses may render twice; Store chooses the winner.
type Coordinator struct {
	reader    *Reader
	renderer  render.Renderer
	validator validation.Validator
	root      string
	rootInfo  os.FileInfo
}

// NewCoordinator opens a Store using the same controlled root used for temporary
// rendering. StoreConfig's exclusive-directory and symlink rules apply. The
// validator must inspect media bytes (e.g. validation.MediaChecker with a native
// inspector authorized for this root), rather than trust renderer metadata.
func NewCoordinator(cfg artifact.StoreConfig, renderer render.Renderer, validator validation.Validator) (*Coordinator, error) {
	if coordinatorNil(renderer) || coordinatorNil(validator) {
		return nil, ErrInvalid
	}
	store, err := artifact.NewStore(cfg)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(cfg.Root)
	if err != nil {
		return nil, artifact.ErrUnsafePath
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, artifact.ErrUnsafePath
	}
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil, artifact.ErrUnsafePath
	}
	reader, err := NewReader(store)
	if err != nil {
		return nil, err
	}
	return &Coordinator{reader, renderer, validator, root, info}, nil
}

// BuildRequest supplies authoritative project state and the exact pixel-input
// projection. The shot is selected from Project, never supplied independently.
// Callers attest that Input describes all shot properties, source/dependency
// bytes and configured renderer behavior, with defaults explicitly resolved.
// The coordinator checks the key, quality, duration and primitive types/order;
// it cannot infer a renderer's private settings or semantic parameter names.
// Source paths in the project are trusted renderer inputs, not cache filenames.
// Callers must not mutate these inputs while Build snapshots them.
type BuildRequest struct {
	Project  domain.Project
	Identity artifact.Identity
	Input    artifact.KeyInput
}

type BuildResult struct {
	View     View
	CacheHit bool // true when verified media was reused, including metadata rebinding
}

// Build returns a detached preview. Errors return a zero result. A hit never
// invokes renderer/validator or creates render output directories. A stale visual
// manifest differing only by element IDs publishes a separate metadata binding.
// Other visual changes return artifact.ErrRerenderRequired without a cache hit.
// An existing artifact without accepted persisted shot validation returns ErrMedia.
// A miss renders only the requested shot, checks its manifest against the timeline,
// verifies media, then publishes through Store's integrity/atomicity boundary.
// Observed cancellation before publication prevents it; once Store's atomic
// rename commits, cancellation cannot roll it back (including during Resolve).
func (c *Coordinator) Build(ctx context.Context, req BuildRequest) (BuildResult, error) {
	if ctx == nil || c == nil || c.reader == nil {
		return BuildResult{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return BuildResult{}, err
	}
	input := req.Input.Clone()
	key, err := artifact.BuildKey(input)
	if err != nil || key != req.Identity.Key || input.Quality != req.Identity.Quality {
		return BuildResult{}, ErrInvalid
	}
	// Check raw extension values before JSON snapshotting can erase their types.
	if req.Project.Timeline == nil {
		return BuildResult{}, ErrInvalid
	}
	if _, err := artifact.ManifestDigest(req.Identity, project.ElementsForShot(*req.Project.Timeline, req.Identity.ShotID)); err != nil {
		return BuildResult{}, ErrInvalid
	}
	var p domain.Project
	if coordinatorSnapshot(req.Project, &p) != nil || project.Validate(p) != nil || p.Plan == nil || p.Timeline == nil || p.ID != req.Identity.ProjectID {
		return BuildResult{}, ErrInvalid
	}
	var shot domain.Shot
	for _, candidate := range p.Plan.Shots {
		if candidate.ID == req.Identity.ShotID {
			shot = candidate
			break
		}
	}
	if shot.ID == "" || shot.Duration != input.Shot.Duration || len(shot.Primitives) != len(input.Shot.Primitives) {
		return BuildResult{}, ErrInvalid
	}
	for i, primitive := range shot.Primitives {
		if primitive.Type != input.Shot.Primitives[i].Type {
			return BuildResult{}, ErrInvalid
		}
	}
	requirements := validation.Requirements{Kind: domain.AssetKindVideo, Quality: input.Quality, ExpectedDuration: shot.Duration, Width: input.Render.Width, Height: input.Render.Height, FPS: input.Render.FPS}
	if requirements.Validate() != nil {
		return BuildResult{}, ErrInvalid
	}
	view, hit, err := c.resolveOrBind(ctx, p, req.Identity)
	if err != nil || hit {
		if err != nil {
			return BuildResult{}, err
		}
		return BuildResult{View: view, CacheHit: true}, nil
	}
	if !coordinatorSameDir(c.root, c.rootInfo) {
		return BuildResult{}, artifact.ErrUnsafePath
	}
	work, err := os.MkdirTemp(c.root, ".preview-")
	if err != nil {
		return BuildResult{}, artifact.ErrStoreIO
	}
	workInfo, err := os.Lstat(work)
	if err != nil {
		return BuildResult{}, artifact.ErrStoreIO
	}
	defer func() {
		if coordinatorSameDir(c.root, c.rootInfo) && coordinatorSameDir(work, workInfo) {
			_ = os.RemoveAll(work)
		}
	}()
	// The callback owns a second copy, independent of the authority used below.
	var renderShot domain.Shot
	if coordinatorSnapshot(shot, &renderShot) != nil {
		return BuildResult{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return BuildResult{}, err
	}
	started := time.Now()
	rendered, err := c.renderer.RenderShot(ctx, render.ShotRenderRequest{Shot: renderShot, OutputDir: work})
	elapsed := time.Since(started)
	if ctx.Err() != nil {
		return BuildResult{}, ctx.Err()
	}
	if err != nil {
		return BuildResult{}, &coordinatorError{ErrRender, err}
	}
	// The existing Renderer writes exactly this file in our fresh directory.
	// Do not follow arbitrary result paths or copy external files into the store.
	media := filepath.Join(work, string(shot.ID)+".mp4")
	if !coordinatorSameDir(c.root, c.rootInfo) || !coordinatorSameDir(work, workInfo) || rendered.MediaPath != media || rendered.Manifest.MediaPath != media || rendered.Manifest.ShotID != shot.ID || !sameElements(project.ElementsForShot(*p.Timeline, shot.ID), rendered.Manifest.Elements) {
		return BuildResult{}, ErrRenderResult
	}
	info, err := os.Lstat(media)
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		return BuildResult{}, ErrRenderResult
	}
	checked, err := c.validator.Validate(ctx, validation.Request{ID: shot.ID, TargetID: shot.ID, Asset: domain.Asset{ID: shot.ID, Kind: domain.AssetKindVideo, URI: media}, Requirements: requirements})
	if ctx.Err() != nil {
		return BuildResult{}, ctx.Err()
	}
	if err != nil {
		return BuildResult{}, &coordinatorError{ErrMedia, err}
	}
	if checked.ID != shot.ID || !acceptedValidation(req.Identity, &checked) {
		return BuildResult{}, ErrMedia
	}
	// Persist only the verified verdict/correlation, not adapter extension data.
	verdict := domain.ValidationResult{ID: checked.ID, TargetID: checked.TargetID, Status: checked.Status}
	// Round up to the persisted millisecond resolution, including sub-ms renders.
	durationMs := elapsed.Milliseconds()
	if elapsed%time.Millisecond != 0 {
		durationMs++
	}
	generation := domain.GenerationResult{ID: shot.ID, RequestID: shot.ID, AssetID: shot.ID, DurationMs: durationMs}
	// Renderer has no peak-memory measurement contract; nil records unknown.
	if input.Seed != nil {
		generation.Seed = *input.Seed
	}
	logical := filepath.Base(work) + "/" + string(shot.ID) + ".mp4"
	rendered.Manifest.MediaPath = logical
	_, err = c.reader.store.Publish(ctx, artifact.PublishRequest{
		Identity: req.Identity, MediaPath: logical, Manifest: rendered.Manifest, Generation: &generation, Validation: &verdict,
		Provenance: &artifact.Provenance{Seed: input.Seed, Provider: input.Provider, Model: input.Model, Profile: input.Profile, Sources: input.Sources, Dependencies: input.Dependencies},
	})
	if err != nil {
		return BuildResult{}, err
	}
	view, hit, err = c.resolveOrBind(ctx, p, req.Identity)
	if err != nil {
		return BuildResult{}, err
	}
	if !hit {
		return BuildResult{}, ErrRenderResult
	}
	return BuildResult{View: view}, nil
}

func (c *Coordinator) resolveOrBind(ctx context.Context, p domain.Project, id artifact.Identity) (View, bool, error) {
	view, hit, err := c.reader.Resolve(ctx, p, id)
	if errors.Is(err, artifact.ErrRerenderRequired) || !errors.Is(err, ErrManifest) {
		return view, hit, err
	}
	// Resolve already validated authority and verified the immutable media entry.
	// BindManifest re-verifies it and snapshots metadata before atomic publication.
	if _, err := c.reader.store.BindManifest(ctx, id, project.ElementsForShot(*p.Timeline, id.ShotID)); err != nil {
		return View{}, false, err
	}
	return c.reader.Resolve(ctx, p, id)
}

// Public diagnostics contain safe codes; errors.Is/As retain the original cause.
// Unwrapped dependency errors may contain paths and must not be logged raw.
type coordinatorError struct{ code, cause error }

func (e *coordinatorError) Error() string        { return e.code.Error() }
func (e *coordinatorError) Unwrap() error        { return e.cause }
func (e *coordinatorError) Is(target error) bool { return target == e.code }

func coordinatorNil(v any) bool {
	if v == nil {
		return true
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Pointer, reflect.Func, reflect.Map, reflect.Slice, reflect.Chan, reflect.Interface:
		return reflect.ValueOf(v).IsNil()
	}
	return false
}

func coordinatorSameDir(path string, expected os.FileInfo) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && os.SameFile(expected, info)
}

func coordinatorSnapshot(src, dst any) error {
	data, err := json.Marshal(src)
	if err != nil {
		return ErrInvalid
	}
	if json.Unmarshal(data, dst) != nil {
		return ErrInvalid
	}
	return nil
}
