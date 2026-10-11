# ARCHITECTURE.md

This document describes the **as-built** architecture of Video Factory. It
is updated as implementation progresses. The full plan, phase scope, and
process rules live in `PHASES.md`, which is the execution authority for
this project (see `AGENTS.md`) — this document records what has actually
been built and why, not the aspirational design.

## Module

```text
module github.com/manish96170/videofactory
go 1.23
```

Repository and intended GitHub remote: `github.com/manish96170/videofactory`
(a standalone repository, not part of any other workspace/monorepo).

## Phase 00 — Foundation / Contracts

### Package layout

```text
videofactory/
├── domain/     core domain contracts (pure data, PHASES.md section 5)
├── event/      event vocabulary + Publisher/Subscriber contracts
├── provider/   Provider contract (capability-based routing)
├── decision/   Decision Provider contract + pure threshold Policy
├── resource/   Resource Request/Lease/Manager contract
└── cmd/
    └── videofactory/   minimal entrypoint, proves the module builds
```

### Why one `domain` package for data, and separate packages for behavior

PHASES.md section 5 ("Core Domain Model") lists all core data types —
`Project`, `VideoPlan`, `Shot`, `Timeline`, `Asset`, `Primitive`, `Flow`,
`Framing`, `DecisionRequest`, `DecisionResult`, `ProviderCapabilities`,
`GenerationRequest`, `GenerationResult`, `ValidationResult`,
`ResourcePlan`, `ModelCapabilities`, `VisualElement`, `RenderManifest`,
`ReviewFeedback`, `ChangeSet` — as one shared vocabulary. They live
together in `domain` so every package references the same types by the
same names, with no risk of duplicate/divergent definitions.

Behavior — the small interfaces that *consume* those data types — lives in
its own package per concern, per PHASES.md section 19 ("small interfaces,
explicit dependencies... split interfaces by capability"):

- `provider.Provider` — capability-based provider lookup.
- `decision.Provider` — decision classification.
- `resource.Manager` / `resource.Lease` — resource acquisition/release.
- `event.Publisher` / `event.Subscriber` — event observability.

This keeps `domain` free of behavior and keeps each behavioral package
small and independently testable, without stuttering package-qualified
names (e.g. `provider.Provider`, not `provider.ProviderProvider`).

### Open vocabularies, not closed enums

`domain.ShotCategory`, `domain.PrimitiveType`, `domain.AssetKind`, and
provider/model capability tags are typed strings with a set of exported
"recommended" constants, not closed Go enums. This mirrors the
pluggability principles in PHASES.md sections 4.1/4.2/7: new shot
categories, primitives, flows, framings, or capability tags can be
introduced by later phases/providers without modifying `domain`.

### Deferred to later phases (explicit non-goals for Phase 00)

- ID generation strategy (ULID/UUID/content-hash) — Phase 01.
- Event bus implementation (ordering, backpressure, cancellation
  propagation) — Phase 02.
- Resource Manager enforcement (actual memory/CPU/GPU budgeting,
  queueing, process monitoring) — Phase 08.
- Decision Engine provider registry, fallback/escalation policy — Phase 06.
- Any AI/cloud provider implementation, TUI, renderer, or Python/process
  worker boundary.

### Build / test gate (Phase 00)

```text
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Committed as `727d66c` ("phase(00): establish foundation and contracts").
Evidence: `docs/phases/phase-00.md`, `docs/reviews/phase-00-review-terra.md`,
`docs/reviews/phase-00-review-astra.md`.

### Serialized duration and timing conventions

`time.Duration` fields in `domain` serialize via `encoding/json`'s default
behavior: an integer number of **nanoseconds**. This applies to
`Shot.Duration`, `TimelineEntry.Start`/`End`, `VisualElement.Start`/`End`,
and `ReviewFeedback.Timestamp`/`Duration`.

This is deliberately different from `GenerationResult.DurationMs`, which
records a real-world wall-clock measurement (how long a generation attempt
took) crossing a future process/worker boundary where millisecond
precision is sufficient and an explicit field name avoids ambiguity. See
`DECISIONS.md` ADR-007.

Time-offset semantics (see doc comments in `domain/timeline.go` and
`domain/visual.go` for the normative text):

- `TimelineEntry.Start`/`End` are **project-timeline-relative**.
- `VisualElement.Start`/`End` are **shot-relative** (0 = start of the shot
  named by `ShotID`), matching the per-shot `RenderManifest` convention
  (`shot-017.mp4` / `shot-017.render.json`). Add the shot's
  `TimelineEntry.Start` to get a project-relative timestamp.
- `ReviewFeedback.Timestamp` is shot-relative when `ShotID` is set,
  otherwise project-relative.
- All intervals are end-exclusive: `[Start, End)`.
- `Bounds` are normalized `[0,1]` coordinates relative to the full output
  frame, origin `(0,0)` at the top-left.

### Capability vocabularies: two different patterns, by design

`domain.ShotCategory`, `domain.PrimitiveType`, and `domain.AssetKind` are
each a dedicated string type with an exported set of "recommended"
constants (open to extension, but with named values for the common cases).

`ProviderCapabilities.Tags` and `ModelCapabilities.Tags`, by contrast, are
plain `[]string` with **no** constants at all: capability tags are
genuinely open-ended and provider/model-specific (e.g.
`"semantic-decision"`, `"realistic-human-motion"`), so this package does
not attempt to enumerate them.

## Phase 01 — Project State / Timeline

### Package layout

```text
videofactory/
├── idgen/      stable, time-sortable ID generation (ULID-shaped)
└── project/    persistence, validation, dependency graph, timeline
                helpers — the behavior layer over domain's data contracts
```

### `idgen`: ID generation strategy (resolves Phase 00 ADR-005)

IDs are standard ULIDs (https://github.com/ulid/spec): a 48-bit
millisecond timestamp + 80 bits of `crypto/rand` randomness, encoded as
26 characters of Crockford Base32 with the standard 2 leading zero
padding bits (so the first character is always `0`-`7`). This makes IDs
lexicographically sortable by creation time, collision-resistant, and
interoperable with any standard ULID decoder, using only the standard
library (PHASES.md section 4.9, "no unnecessary dependencies"). A failed
read from the OS CSPRNG is a fatal, surfaced error (`New` panics;
`newAt` returns an error) rather than a silent predictable fallback,
since the latter would defeat the collision-resistance guarantee.
`idgen` has no dependency on `domain` or any other package, so any
future package that needs to mint an ID (events, generation requests,
decisions, ...) should call `idgen.New()` directly; `project.NewID()` is
only a convenience wrapper for this package's own constructors.

### `project`: the behavior layer over `domain`

Phase 00 deliberately kept `domain` free of behavior (ADR-003). Phase 01
is the first package to add real behavior over those contracts. It also
extends `domain.Project` itself (ADR-010) to own `Timeline` and `Assets`
alongside `Plan`, so the whole project aggregate — not just its plan —
is one persisted consistency boundary.

- **Validation** (`project.Validate`, `project.ValidateTimeline`):
  structural integrity — required fields, `Project`/`VideoPlan`/`Shot`/
  `Timeline`/`Asset` linkage, shot-ID/primitive-ID/index/asset-ID
  uniqueness, positive shot durations, required `FlowID`/`FramingID`
  references, dependency referential integrity and acyclicity, and
  `Timeline`/`VisualElement` consistency: unknown-shot references,
  non-negative and well-formed `[Start, End)` intervals, at most one
  `TimelineEntry` per shot (whose placed duration must equal the shot's
  own `Duration`), unique `VisualElement` IDs, every element's shot
  actually being placed on the timeline (not just a known shot — see
  ADR-012), and an element's shot-relative `End` never exceeding its
  shot's declared `Duration`. Issues are aggregated with `errors.Join`
  rather than failing fast on the first problem.
- **Dependency graph** (`project.DependencyGraph`,
  `BuildDependencyGraph`, `TopologicalOrder`, `Dependents`,
  `AffectedBy`): built from `Shot.DependsOn` (PHASES.md section 4.6,
  "incremental rebuild"). `BuildDependencyGraph` itself rejects empty or
  duplicate shot IDs and dangling dependency references, and dedupes
  repeated dependency edges, so it's safe to call directly without going
  through `Validate` first. `TopologicalOrder` detects cycles and
  reports only the cycle itself in `CycleError.Cycle`, not any acyclic
  path that led into it. `AffectedBy` computes the transitive closure of
  dependents a future rebuild step would need to invalidate; Phase 01
  only builds the graph and the query, not caching/hashing/scheduling
  (Phase 10). (Known limitation: traversal is recursive and could
  exhaust the goroutine stack on an adversarially deep/huge dependency
  chain; acceptable for Phase 01's locally-authored-project scope.)
- **Timeline/element helpers** (`project.AbsoluteStart`,
  `AbsoluteEnd`, `ElementsForShot`, `EntryForShot`): the first concrete
  code implementing the shot-relative/project-relative timing convention
  documented in ADR-007, rather than leaving every caller to re-derive
  it with raw arithmetic.
- **Persistence** (`project.Store`, `project.FileStore`,
  `project.Marshal`/`Unmarshal`): one JSON file per project (including
  its `Timeline` and `Assets`), wrapped in a versioned envelope
  (`schemaVersion` + `project`), written atomically and durably
  (temp file, write, `fsync`, close, rename, best-effort directory
  `fsync`) so a crash mid-write cannot corrupt an existing file. `Save`
  and `Load` both run `Validate`, and `Load` additionally rejects a file
  whose decoded `Project.ID` doesn't match the requested ID — this
  package never persists or returns a structurally invalid or
  misattributed `Project` silently (PHASES.md Phase 01, "Corrupt-state/
  error handling"; ADR-011). `Store`'s concurrency contract is
  documented, not just implied: concurrent `Save`s for the *same*
  project ID are last-writer-wins (no built-in optimistic concurrency);
  concurrent operations on *distinct* IDs are fully safe, including
  under `-race` (see `project.TestFileStoreConcurrentSaveLoadDistinctProjects`).

### Security: path traversal in `FileStore`

A `domain.Project.ID` is, in principle, caller-supplied data. `FileStore`
derives a filename directly from it, so it validates every ID against a
conservative allow-list (`^[A-Za-z0-9_-]+$`) before touching the
filesystem, rejecting anything containing path separators or `..`
segments. See `DECISIONS.md` ADR-009.

### Known limitations (Phase 01)

- Local filesystem only: `FileStore` does not defend against a
  maliciously writable store directory (e.g. symlink substitution) —
  acceptable for the current single-user, local-first scope. A future,
  untrusted/multi-tenant `Store` implementation would need to.
- No explicit project size/complexity limits (e.g. extremely large
  dependency chains); see the `DependencyGraph` recursion note above.

### Build / test gate (Phase 01)

```text
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Committed as `9f60d63` ("phase(01): implement project state and
timeline"). Evidence: `docs/phases/phase-01.md`,
`docs/reviews/phase-01-review-sol.md`,
`docs/reviews/phase-01-review-luna6.md`.

## Phase 02 — Event System

### Package layout

Phase 02 adds to the existing `event` package rather than a new one:

```text
event/
├── event.go   Type vocabulary, Event (now with Seq), Publisher/Subscriber
└── bus.go     Bus: the concrete in-process Publisher+Subscriber
```

### `event.Bus`: the concrete pub/sub implementation

`Bus` is a single mutex-guarded in-process event bus. It is intentionally
simple — "a pragmatic event-driven architecture," not "a complicated
event-sourcing framework" (PHASES.md section 10) — and makes no attempt
to be a durable log: core project/timeline state remains the source of
truth, `Bus` exists purely for observability.

- **Ordering:** `Publish` assigns a bus-wide, strictly increasing `Seq`
  to every event under its mutex before dispatch, and re-checks
  `ctx.Err()`/closed-state after acquiring that mutex (not just at entry)
  so a caller cancelled while waiting for the lock can't still get an
  event dispatched. A subscriber's delivery order for the types it
  asked for always matches `Seq` order. `Seq`, not wall-clock
  `OccurredAt`, is the authoritative order, because clock resolution/
  monotonicity across goroutines isn't guaranteed. Tested by
  `TestBusOrderingGuarantee` (single publisher, asserts the exact
  sequence `1..n`, not just uniqueness) and
  `TestBusConcurrentPublishersPreserveTotalOrder` (many concurrent
  publishers released together via a start barrier, asserting the exact
  `1..total` sequence with no duplicates, gaps, or reordering).
- **Payload ownership:** `Event.Payload` is handed to every matching
  subscriber by reference, not deep-copied per subscriber. Callers must
  treat a published `Payload` as immutable afterward; this is a
  documented contract (`Event`'s and `Bus`'s doc comments), not enforced
  by copying, to keep dispatch cheap.
- **Backpressure:** dispatch to each subscriber channel is non-blocking
  (`select`/`default`). A full channel causes that event to be dropped
  for that subscriber only (drop-newest) and increments
  `Bus.TotalDropped()`; `Publish` itself never blocks on a slow
  consumer. This matches PHASES.md section 11's requirement that "the
  TUI must remain responsive while workers run" — a slow TUI/log
  subscriber can't stall generation.
- **Cancellation:** a subscription's channel closes when its own `ctx`
  is done, *or* when `Bus.Close()` runs — whichever first. A dedicated
  `closedCh`, closed exactly once by `Close`, wakes every subscription's
  watcher goroutine even if that subscription's own `ctx` (e.g.
  `context.Background()`) never cancels on its own, preventing a
  goroutine leak (PHASES.md section 4.10, "no uncontrolled goroutines").
  Tested by `TestBusCloseDoesNotLeakBackgroundContextWatchers`, which
  counts live goroutines before/after rather than only checking that a
  channel closed (a channel can close via `Close`'s own direct loop
  even if its watcher goroutine leaked).
- **Event metadata:** `Publish` auto-stamps `evt.ID` (via `idgen.New()`)
  and `evt.OccurredAt` (`time.Now()`) when the caller leaves them zero,
  so callers aren't required to hand-stamp every event — but a caller
  that sets them explicitly (e.g. for deterministic tests) is respected.

### Build / test gate (Phase 02)

```text
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Committed as `c05f2fd` ("phase(02): implement event system").
Evidence: `docs/phases/phase-02.md`, `docs/reviews/phase-02-review-astra.md`,
`docs/reviews/phase-02-review-gpt-oss-120b.md`.

## Phase 03 — Deterministic Renderer

### Package layout

```text
render/
├── render.go       Settings, Renderer interface, FFmpegRenderer.RenderShot
├── ffmpeg.go       os/exec process execution (cancellation, stderr capture)
├── filtergraph.go  Primitive -> ffmpeg filter_complex graph construction
├── properties.go   typed Properties extraction, color/bounds/escaping helpers
├── probe.go        ffprobe-based output validation
├── manifest.go     RenderManifest/VisualElement derivation + persistence
└── timeline.go     sequential Timeline construction + concat-demuxer composition
```

Also: `domain.PrimitiveTypeBackground` was added in `domain/shot.go`
(Phase 03's "basic background primitive" isn't in PHASES.md section 6's
list, but the Phase 03 TODO explicitly requires it, and `PrimitiveType`
is an open vocabulary precisely so this kind of addition doesn't need a
new phase).

### FFmpeg integration strategy (ADR-015)

Core logic (deciding *what* to render) stays in Go; the external
`ffmpeg`/`ffprobe` binaries do the actual pixel/encoding work, invoked
via `os/exec` with `context.Context` for cancellation — not cgo/libav
bindings. `ffmpeg`/`ffprobe` are a system dependency, not a Go module
dependency (see `README.md`).

### Shot composition: layered primitives, one filter graph per shot

`buildFilterGraph` composes a Shot's Primitives into one ffmpeg filter
graph:

- At most one `Background` primitive (solid color, default black) is
  always the base layer, regardless of its position in the Primitives
  list.
- Any number of `Image`/`Text` primitives are composited bottom-to-top
  in list order: images via `scale`+`overlay` (looped-still-image
  inputs), text via `drawtext` directly on the running composite.
- At most one `Transition` primitive applies a whole-shot `fade`
  (in or out) as the final stage — a transition is its own short Shot
  (typically `ShotCategoryTransition`) bridging two content shots on
  the Timeline, not a cross-fade between two shots' pixels (deferred;
  see Known limitations).
- Any other `PrimitiveType` (CodeEditor, Terminal, DeveloperAvatar,
  ...) is an explicit error, not a silently-skipped no-op: those need
  AI-assisted or other future-phase rendering this package doesn't
  attempt yet.

Bounds (`Primitive.Properties["bounds"]`) reuse `domain.Bounds`'
normalized `[0,1]`, top-left-origin convention from Phase 00 (ADR-007);
`boundsToPixelBox` converts to pixels and rounds width/height up to the
nearest even number (required for `yuv420p` 4:2:0 chroma subsampling).

### `drawtext` text/font-path handling (ADR-016)

Text content and `Settings.FontPath` are never embedded directly into
the `filter_complex` string: text is written to a temp file and
referenced via `textfile=`, and the font is copied (by content) to a
temp file with a Go-generated safe name, referenced via `fontfile=`.
An earlier escaping-based approach *parsed without error* but silently
rendered empty text for any input containing an apostrophe — caught
only once pixel-level assertions were added to the integration tests,
not by "ffmpeg exited 0." See ADR-016 for the full story and why
escaping-in-place was abandoned in favor of controlled temp files.
`filterPlan.cleanup` holds the resulting temp-file removal functions,
run by `RenderShot` after the ffmpeg command finishes either way.

### Render manifest / stable element identity

Each rendered shot gets a sidecar `<shotID>.render.json`
(`shot-017.mp4` / `shot-017.render.json`, PHASES.md section 4.6.1),
versioned like `project`'s persistence (`MarshalManifest`/
`UnmarshalManifest`). Every `Primitive` becomes one `VisualElement`
that **reuses the Primitive's own ID directly** as its element ID —
Primitive IDs are already stable (assigned by whatever built the
`Shot`), so this satisfies "stable rendered element identity" without
a separate content-hashing scheme. Every element spans
`[0, shot.Duration)`: Phase 03 doesn't yet support a primitive active
for only part of its shot. `validateShotPrimitiveIDs` rejects an empty
or duplicate `Primitive.ID` before rendering, so this identity
guarantee can't be silently violated; `validateSettings` similarly
rejects a non-positive `Width`/`Height`/`FPS` before it reaches ffmpeg
as a confusing invocation failure.

### Timeline composition

`BuildSequentialTimeline` places a `VideoPlan`'s shots back-to-back in
`Index` order (a default placement strategy, not the only possible
one). `ComposeTimeline` concatenates their rendered clips via ffmpeg's
concat demuxer with a stream copy (`-c copy`, no re-encoding, since
every clip shares the same `Settings`). Every entry's interval must be
well-formed (`End > Start`), and entries must be contiguous
(`entry[i].End == entry[i+1].Start`, first `Start == 0`); gap-filling
(e.g. inserting black filler for a deliberate gap) is deferred —
`ComposeTimeline` returns a clear error for a malformed or
non-contiguous timeline rather than attempting it silently.

### FFprobe validation and pixel-level verification

`Probe` parses `ffprobe`'s JSON output (duration, resolution, codec,
frame rate); `ValidateOutput` compares it against the `Settings` and
expected duration a render was supposed to produce (with a
`durationTolerance` for encoder/container rounding). `RenderShot` runs
this automatically after every render, so an `ffmpeg` invocation that
exits `0` but produces something unexpected is still caught — though
"unexpected" here only covers container/stream-level metadata, not
pixel content; see the next section for why that distinction mattered
in practice.

`SamplePixel`/`RGB.CloseTo` extract and compare actual rendered pixel
colors (via `-f rawvideo -pix_fmt rgb24` output), used by
`render_test.go` to verify background color, image placement (inside
vs. outside its bounds), text presence, and fade brightness change —
not just "ffmpeg exited 0 and ffprobe likes the result." This exists
*because* a real bug (ADR-016) parsed and probed successfully while
rendering completely empty text; metadata-only checks would never have
caught it.

### Deterministic test fixtures

The end-to-end milestone test (`TestEndToEndThreeShotMilestone`, see
PHASES.md section 29's First Milestone: "Manual VideoPlan → 3
deterministic shots → Deterministic renderer → MP4") generates its
image fixture at test time via `image/png` rather than committing a
binary PNG to the repository.

### Known limitations (Phase 03)

- No cross-fade between two different shots' pixels — a Transition
  primitive is a whole-shot fade-to/from-black, not a true two-clip
  blend. True cross-fades need overlapping clip ranges and `xfade`,
  deferred to a later phase/refinement.
- `ComposeTimeline` requires contiguous entries; gap-filling (black
  filler for deliberate gaps) isn't implemented.
- Every `VisualElement` spans its whole shot; a primitive active for
  only part of a shot isn't supported yet.
- No audio track (Phase 11 owns audio/timestamp integration).

### Build / test gate (Phase 03)

```text
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Committed as `5bb3c68` ("phase(03): implement deterministic
renderer"). Evidence: `docs/phases/phase-03.md`,
`docs/reviews/phase-03-review-luna.md`,
`docs/reviews/phase-03-review-gpt-oss-20b.md`.

## Phase 04 — Flows / Framing / Primitives

### Package layout

```text
registry/
├── flow.go             FlowRegistry (built-in: developer-explainer, code-tutorial)
├── framing.go          FramingRegistry (built-in: 5 named framings + their Slots)
├── primitive.go        PrimitiveRegistry (capability declarations for every PrimitiveType)
└── compose.go          AssignSlots — the framing/business-logic decoupling proof
```

Also: `domain.Flow` gained `DefaultFramingID`/`RecommendedCategories`;
`domain.Framing` gained `Slots []FramingSlot`; new
`domain.PrimitiveCapabilities` (same capability-tag pattern as
`ProviderCapabilities`/`ModelCapabilities`). All additive, following
the same pattern Phases 01/03 used to extend `domain`.

### Registries: small, mutex-protected, seeded, open to extension

`FlowRegistry`/`FramingRegistry`/`PrimitiveRegistry` are each a
mutex-protected map (safe for concurrent use, tested under `-race`)
constructed via `NewXRegistry()`, pre-seeded with built-in entries, and
open to runtime `Register()` of more. None are a package-level global
singleton — callers hold and pass around their own instance, matching
this module's existing style (`project.FileStore`, `event.Bus`, ...).

- **Flows:** `developer-explainer` (→ default framing
  `developer-circle`) and `code-tutorial` (→ default framing
  `screen-focused`), each with a `RecommendedCategories` hint for a
  future Director (Phase 05) — advisory, not enforced.
- **Framings:** `fullscreen`, `split-screen`, `picture-in-picture`,
  `developer-circle`, `screen-focused` — each a named set of
  `domain.FramingSlot{Name, Bounds}` using the same normalized `[0,1]`
  convention as `VisualElement` (ADR-007). `developer-circle` is
  "main" (fullscreen) + "overlay" (small corner box) like
  `picture-in-picture`; the "circular" presentation PHASES.md section 7
  describes is a rendering detail for whatever draws the overlay slot's
  content, not something the registry enforces — consistent with
  "developer-circle must NOT become a hard-coded global architecture...
  it is simply one reusable framing."
- **Primitives:** every `domain.PrimitiveType` constant gets a
  `PrimitiveCapabilities` entry (deterministic-vs-AI-capable per
  PHASES.md section 6, `SupportsBounds`, `RequiredProperties`, `Tags`).
  This is forward-looking metadata independent of what `render`
  (Phase 03) actually implements today; `RequiredProperties` lists
  only properties with *no* renderer-supplied default (so `render`
  errors if they're absent) — `Background`'s `color` and
  `Transition`'s `style` both default in `render/filtergraph.go`, so
  neither is listed as required, which
  `TestPrimitiveRegistryMatchesRenderPackageExpectations` pins
  explicitly (an earlier version of this got that backwards — see
  DECISIONS.md ADR-017's amendment).

### Registry entries are defensively copied in and out

`Register`/`Get`/`List` all copy slice-typed fields
(`Flow.RecommendedCategories`, `Framing.Slots`,
`PrimitiveCapabilities.RequiredProperties`/`Tags`) rather than storing
or returning values that alias a caller's backing array. Without this,
the mutex only protects the map itself — a caller mutating a value
returned by `Get` (or mutating the value they passed to `Register`,
after the call) could silently corrupt the registry's internal state,
racily, despite the lock. Confirmed by review and verified by
deliberately reverting the fix and watching
`TestFlowRegistryIsolatesSliceFieldsFromCallerMutation` fail, then
restoring it. See DECISIONS.md ADR-017's amendment.

### `AssignSlots`: proof that framing isn't embedded in business logic

PHASES.md Phase 04 explicitly requires "ensure framing is not embedded
into business logic." `registry.AssignSlots(framing, content)` is the
one function anything composing content onto a Framing should call: it
reads only `framing.Slots`, contains no reference to any specific
`FramingID`, and returns each matched primitive with
`Properties["bounds"]` set to its slot's bounds (the same key
`render`'s Image/Text primitives already read).

`TestAssignSlotsWorksForARuntimeRegisteredFramingWithNoCodeChanges`
registers a brand-new "triple-split" framing (not one of the five
built-ins) at runtime and runs it through the unmodified `AssignSlots`
— the concrete demonstration, not just an assertion, that adding a
framing never requires touching this function. Separately,
`render` (Phase 03) was grepped for any Framing-related
conditional logic: none exists — Phase 03 operates purely at the
Shot/Primitive level and never branches on a `FramingID`.

The composition tests assign bounds-aware `Image` primitives (not
`Background`, which `render` ignores bounds for) and assert every
slot's *exact* bounds, index-aligned against `framing.Slots` — not
just that a `"bounds"` key is present — so a bug that mixed up which
slot's bounds went to which primitive would be caught.

### Known limitations (Phase 04)

- `developer-circle`'s overlay slot is a rectangular `Bounds`, like
  `picture-in-picture`'s; there is no circular-mask/clipping data or
  rendering support yet. The "circular" presentation PHASES.md section
  7 describes is left to a future renderer capability — this phase
  only guarantees the slot's *position*, not its shape.
- `AssignSlots` does not validate a `Framing` passed to it directly
  (only `FramingRegistry.Register` validates slot well-formedness);
  callers that bypass the registry and construct a `domain.Framing` by
  hand are responsible for its validity.
- The registries' `Register` rejects re-registering an existing ID
  outright; there is no "override a built-in" mechanism — a caller
  that wants different `fullscreen` behavior must register it under a
  new ID.

### Build / test gate (Phase 04)

```text
go build ./...
go vet ./...
go test ./...
go test -race ./...
```

Committed as `65a5441` ("phase(04): implement flow/framing/primitive
registries"). Evidence: `docs/phases/phase-04.md`,
`docs/reviews/phase-04-review-terra.md`,
`docs/reviews/phase-04-review-sol6.md`.

## Phase 05 — Director / Video Planning

### Package layout

```text
director/
├── director.go              Director interface, PlanRequest/PlanResult, AssetRequirement
├── interpreter.go           PromptInterpreter — the optional LLM adapter seam
├── decompose.go             fixed keyword/reading-pace heuristics (no AI)
└── fixture.go               FixtureDirector.Plan — the deterministic implementation
```

Also: `project.ValidatePlan(projectID, plan)` was exported from Phase
01's `project` package so a Director can validate a `VideoPlan` before
attachment to a `Project`. The public wrapper requires a nonempty
expected project ID and matching `plan.ProjectID`, then reuses the
existing internal structural checks. It does not look up project storage.

### Pipeline: Prompt → Director → VideoPlan

`FixtureDirector.Plan` implements PHASES.md Phase 05's pipeline
end to end, with no AI model:

1. **Flow selection:** an explicit `PlanRequest.FlowID` wins; else a
   `PromptInterpreter`'s suggestion (if configured); else a simple
   keyword check over the whole prompt (`mentionsAvatar` →
   `developer-explainer`, else `code-tutorial` — both from Phase 04's
   `FlowRegistry`).
2. **Framing selection:** an explicit `PlanRequest.FramingID` wins;
   else the selected Flow's `DefaultFramingID` (Phase 04); else
   `fullscreen`.
3. **Shot decomposition:** the prompt is split into sentences
   (`splitSentences`); a `PromptInterpreter`'s `Shots` hints are used
   instead if nonempty. Empty lists permit fallback; nonempty lists
   containing blank/whitespace-only Text are rejected, not silently
   discarded or replaced. Text is trimmed on a local hint copy; neither
   supplied hints nor empty-list backing arrays are mutated by fallback.
4. **Deterministic-vs-AI intent:** an AVATAR hint or avatar keyword selects
   `DeveloperAvatar`, normalizes category to AVATAR, and sets
   `Shot.Deterministic = false`, even if another category was suggested.
   Other shots use `Background`+`Text` with deterministic intent; missing
   categories are inferred, and explicit non-avatar categories retained.
5. **Shot duration planning:** a fixed reading-pace heuristic
   (`estimateDuration`, 2.5 words/sec, 1.5s floor) — not a model
   estimate.
6. **Asset requirements:** every AI-capable shot gets an
   `AssetRequirement` naming what it needs (e.g. "generated-video");
   deterministic shots need none. Later phases (Shot Intelligence/
   Phase 09, Provider Router/Phase 07) are expected to resolve these —
   the Director only names them.
7. **Provider capability requirements:** every primitive's `Tags` are
   looked up in Phase 04's `PrimitiveRegistry` and aggregated (deduped,
   sorted) into `PlanResult.RequiredCapabilities` — direct reuse of the
   Phase 00/04 capability-tag pattern, not a parallel one.
8. **Plan validation:** `project.ValidatePlan` runs before `Plan`
   returns, so a structurally broken plan is caught immediately rather
   than discovered later.

Context is checked at entry, after interpreter execution, during fallback
and shot construction, and before returning success. Adapter errors retain
their identity through wrapping. Determinism describes planning structure:
generated IDs and timestamps are intentionally fresh for each call.

### The optional LLM adapter seam

PHASES.md Phase 05's "Important" note — "Do not tightly couple the
Director to Claude, Qwen, Gemini, or any single model" — is satisfied
by `PromptInterpreter`: the *only* seam `FixtureDirector` uses for
LLM-assisted hints (a suggested Flow and/or shot segmentation). No
production implementation exists yet; `FixtureDirector` works
correctly with none configured (`WithPromptInterpreter` is optional),
which is also how its own tests exercise both paths
(`TestPlanFallsBackToHeuristicsWhenInterpreterHasNoShots`).

The Director does not serialize interpreter calls. For concurrent use,
the caller must supply a concurrency-safe interpreter and keep its returned
values stable while consumed. No interface change or adapter infrastructure
is introduced here; tests exercise 24 callers with a stateless interpreter.

### Second Milestone: Prompt → Director → VideoPlan → Deterministic renderer → MP4

`TestSecondMilestonePromptToRenderedMP4` is PHASES.md section 29's
Second Milestone, literally: a prompt goes through `FixtureDirector`,
`render.BuildSequentialTimeline`, `render.FFmpegRenderer.RenderShot`
(Phase 03), and `render.ComposeTimeline`, producing one real, probed
MP4 — with no avatar keywords, so every shot is deterministic and
actually renders today. A companion test,
`TestSecondMilestoneRejectsNonDeterministicPlanAtRenderTime`, confirms
the boundary this phase deliberately leaves open: the Director *can*
plan a `DeveloperAvatar` shot (deciding AI should be used), but nothing
in this module can render it yet, and `RenderShot` fails loudly rather
than silently skipping or faking it — AI provider implementations are
Phase 14/15.

The text milestone checks FFmpeg/FFprobe, `drawtext`, and a discovered
default font, skipping if prerequisites are unavailable. Avatar rejection
asserts the unsupported-primitive error before native execution and also
runs with an empty PATH; it needs no native tools or fonts. The final lead
gate executed the real milestone uncached (not skipped).
Prior-session manual frame inspection is recorded in the historical handoff,
not claimed as a new documentation-agent visual test.

### Known limitations (Phase 05)

- `splitSentences`/keyword matching are simple, fixed rules — not NLP,
  not an AI classifier. A prompt with no `.`/`!`/`?` becomes one shot;
  highly ambiguous prompts may categorize imprecisely. This is by
  design for a "deterministic fixture Director," not a limitation to
  fix later with more string-matching cleverness — a real
  `PromptInterpreter` is the intended upgrade path.
- No production `PromptInterpreter` implementation exists; model-backed
  assistance remains later-phase work behind the same seam.
- `AssetRequirements`/`RequiredCapabilities` are produced but not acted
  on by anything yet (Phase 07's Provider Router, Phase 09's Shot
  Intelligence).
- Durations have a floor but no cap. Non-avatar CODE/TERMINAL categories
  still use Background+Text, not specialized editor/terminal visuals.
- Framing selection is metadata only: the Director does not call
  `AssignSlots`, apply bounds, or promise visible framing in this milestone.
- Concurrent use requires a concurrency-safe caller-supplied interpreter;
  fresh IDs/timestamps mean structural, not byte-identical, determinism.

### Build / test gate (Phase 05)

```text
go build ./...
go vet ./...
go test -count=1 ./...
go test -race -count=1 ./...
```

Final lead build/vet/uncached unit and integration/race gate: PASS;
formatting clean. Luna and Astra approved the final code sequentially
after all findings were fixed and retested. Coverage includes full normalized result
comparison with 17 sensitivity cases, exact mixed capability ordering/
deduplication, runtime registry selection/defaults/precedence, planning JSON
round-trips, concurrent callers, and standalone project-linkage rejection.
Evidence: `docs/phases/phase-05.md`,
`docs/reviews/phase-05-review-luna.md`,
`docs/reviews/phase-05-review-astra.md`. Phase commit:
`phase(05): implement director / video planning` (see repository history).

## Phase 06 — Decision Engine

**Phase 06 complete, recorded by its phase commit:** all fixes are lead-inspected;
full uncached build/vet/test/race gate passes after the latest fix. Terra final
APPROVE then Luna6 final APPROVE were sequential, with no outstanding findings.
Final lead post-confirmation build/vet/uncached test/race gate and ten repeated
decision runs pass; formatting, final staged diff, and hygiene checks pass.

### Package layout and unchanged contracts

Phase 06 extends `decision`: `engine.go` (Config/Engine), `registry.go`
(Resolver/Registry/CapabilityAdapter), `errors.go` (safe errors/outcomes),
`fixture.go` (offline per-kind provider), and `audit.go` (event publication).
Tests are in `engine_test.go`, `registry_test.go`, and `audit_test.go`.
Existing `decision.Provider` and domain request/result signatures and JSON
fields are unchanged; comments clarify score/ownership semantics. `event`
adds four decision event types, not a new bus or event schema.

### Explicit configuration and deterministic authority

`NewEngine(resolver Resolver, cfg Config) (*Engine, error)` validates without
resolving/calling providers, copies `cfg.Providers`, and never implicitly
defaults zero values. Start with `DefaultConfig(providers ...string)`:

| Setting | Recommended default / constraint |
| --- | --- |
| Providers | Explicit ordered 1–8 distinct nonblank handles; hard cap 8 |
| Policy | `DefaultPolicy()`: YES >= 0.85, NO <= 0.30, otherwise UNCERTAIN |
| AttemptTimeout | 5s; must be positive |
| MaxAttempts | 2 including initial call; allowed 1–3, hard cap 3 |
| RetryDelay | Fixed 100ms; zero allowed, negative rejected |
| EscalateUncertain | true |
| Publisher / AuditTimeout | Optional nil publisher / 200ms positive timeout |

Defaults are configurable; hard caps are fixed. `AuditTimeout` is validated
even with audit disabled. A zero-value Policy is valid and yields YES for all
valid scores; equal thresholds are valid and YES wins ties. `Engine.Decide`
requires nonblank request ID/kind and JSON-serializable input. Known verdicts
and finite `[0,1]` affirmative scores are validated, then core Policy recomputes
the verdict. Empty result Provider is filled; mismatched provider identities
are rejected. Arbitrary `ReasonCode` is returned as metadata, never authority
or audit content. No raw kind or adapter error/outcome text is logged by core.

### Decision-local registry and future routing seam

`Resolver.Resolve(ctx context.Context, id string) (Provider, error)` is a
context-aware error-returning lookup, not a boolean lookup. Custom resolvers
own capability admission. `Registry` implements it; its zero value is usable
and `NewRegistry()` is empty. `Register(ctx, CapabilityAdapter) error` admits
adapters implementing both existing `decision.Provider` and `provider.Provider`.
It rejects nil/typed-nil adapters, blank/unstable IDs, mismatching
`Capabilities.ProviderID`, missing `semantic-decision`, and duplicate IDs.
Capability calls run outside the map lock; Resolve returns adapter references
without invoking them. No implicit built-ins, replacement, cost/resource
policy, or Phase 07 router exists. Caller owns adapter/resolver lifecycles,
stable identities, and concurrency safety; engine does not serialize calls.

### Bounded synchronous attempts and input ownership

Each attempt freshly decodes one JSON snapshot of caller Input (ADR-008,
numbers decode as float64), isolating nested values across retries/fallback.
Caller must not mutate during snapshotting; adapters must not retain or
asynchronously mutate attempt input after return. Resolver lookup and provider
call share the child AttemptTimeout context and run synchronously, with
identity checks before/after the call. Late success after child timeout is
rejected; a resolver returning a provider after child expiry never invokes it.
Post-call adapter ID is checked even when Decide returns an error; mismatch
preserves the original causes and beats ErrTransient, while caller cancellation
and child deadline retain precedence. Results accompanying errors are ignored.
Cooperative timeouts cannot forcibly stop an uncooperative dependency.
No detached goroutine hides work. Snapshotting, audits, and retry delays are
additional work, not part of that per-attempt timeout.

Only child timeouts and `ErrTransient`-marked failures retry, with a
cancellation-aware fixed delay; malformed/provenance errors take precedence
over transient markers. Other failures advance through the explicit chain.
Valid UNCERTAIN escalates without retry by default; disabling escalation
returns it immediately. Preserve the last valid UNCERTAIN when later providers
fail/abstain. Otherwise all-failed execution returns `ErrExhausted` with at most
24 attempt causes. The terminal cleanup rechecks parent context after
terminal publication, clears the result, and returns a cancellation Error retaining
any previous failure. Certain/saved-uncertain/exhausted regressions failed before
the fix and pass afterward; code and regression fixes are reviewed and retested.

`decision.Error` formats only an engine-owned Outcome code. `Outcome()` exposes
that code; `Unwrap() []error` returns a defensive copy preserving all causes
for `errors.Is/As`. Raw causes require caller redaction, not direct logging;
untrusted adapter Outcome text never becomes the core audit classification.

### Fixture and best-effort audit

`NewFixtureProvider(id, map[string]domain.DecisionResult)` copies/validates an
immutable per-kind scalar table. It ignores Input, supports concurrent reads,
and returns `ErrUnsupportedKind` for unknown kinds. Capabilities return the
matching ID, `Local: true`, and a fresh `semantic-decision` tag slice. It calls
no model, network, or process; no Director/pipeline integration is added.

Nil/typed-nil publishers disable audit. Accepted requests use `DECISION_STARTED`,
`DECISION_ATTEMPT_COMPLETED`, and a terminal `DECISION_COMPLETED`/`DECISION_FAILED`.
Every event has a new ID/time and an engine-owned scalar-only immutable payload:
request/provider handles, global attempt ordinal, core outcome/action, thresholds,
and valid score/verdict when available; project/shot handles are envelope fields.
Omit input, raw kind, reason, and error text; all handles must be nonsecret.
Started uses ordinal 0; attempts start at 1 including failed resolution; terminal
records use the final ordinal. `lastProvider` is set to the configured handle
when the ordinal starts, before resolution. FAILED uses this last attempted handle
(empty only if no attempt); COMPLETED uses the valid result's provider, including
saved uncertainty from an earlier provider. No raw adapter identity is added.
Start-publication cancellation still attempts one FAILED record with ordinal 0;
the challenged missing-terminal claim was disproven and withdrawn, not fixed by
moving the defer. Publishers/subscribers must respect payload immutability.

Publication is synchronous/cooperative, bounded by AuditTimeout; failures or
late publication success increment atomic `AuditFailures()` without substituting
a classification error. Failed terminal cleanup uses a separately bounded
`context.WithoutCancel` context. The single attempted terminal event describes
state selected when publication starts. Parent cancellation during publication
controls the eventual method result at the final context check; no second terminal
event revises the first. Publication and cancellation are not atomic; the
certain/saved-uncertain/exhausted regression cases pass after the fix.
Publish success is not delivery: `event.Bus` can drop
subscriber records while `AuditFailures()` stays zero; drops use `TotalDropped()`.
Caller owns publisher/Close; no queue, persistence, replay, or compliance promise.

Evidence: `docs/phases/phase-06.md` and both Phase 06 review records. Terra final
scoped normal/race/vet and Luna6 final full uncached build/vet/test/race plus
staged diff check pass. Final lead post-confirmation gate and hygiene pass.
The single `phase(06): implement decision engine` commit records completion;
resolve its hash from repository history. Phase 07 is NOT STARTED.

## Phase 07 — Provider System / Router

**Phase 07 complete (2026-10-09), recorded by its phase commit.** Implementation/fixes are
lead-inspected; full uncached test/race/build/vet after the latest fix PASS,
gofmt clean. Luna6 final APPROVE then Astra final APPROVE sequentially;
no outstanding findings. Final post-confirmation full gate and ten repeated
provider/decision runs PASS; formatting, final staged diff, and hygiene checks
pass. Earlier phase sections retain their historical snapshots.
Policy authority: `PHASES.md` §0.2/Phase 07 and `DECISIONS.md` ADR-020.

### Metadata and unchanged provider interface

`provider.Provider` remains `ID() string` plus
`Capabilities(ctx context.Context) (domain.ProviderCapabilities, error)`.
`domain/provider.go` adds `CostUnit string` and `EstimatedLatencyMs *int64`;
`domain/provider_health.go` defines separate `ProviderHealth` states UNKNOWN,
HEALTHY, UNHEALTHY, DISABLED. `provider.Snapshot` serializes only Capabilities
and Health, never an adapter. Classification reuses `Local` and `deterministic`.

`CostPerUnit` must be finite/nonnegative; nonzero requires a nonblank unit.
A labelled zero is explicitly free; unlabelled zero is unknown. Cost units match
exactly without trimming/conversion, including when free. Latency is nonnegative
milliseconds; nil is unknown, pointer-to-zero known. Estimates are declarations,
not bills, measurements, or SLAs; unknown/mismatched estimates fail applicable
inclusive caps, but not uncapped routing. This is a Phase 07 metadata extension,
not a retrospective redesign of Phase 06 request/result schemas or engine policy.

### Registry API, ownership, and publication

`provider/registry.go` implements a zero-value-usable mutex-protected Registry;
`NewRegistry() *Registry` creates an empty registry, with no implicit providers.

| Method | Result / behavior |
| --- | --- |
| `Register(ctx, adapter Provider) error` | Discover/validate before atomic insertion; duplicate IDs rejected; initial UNKNOWN health |
| `Refresh(ctx, id string) error` | Publish validated capabilities only; preserve current health |
| `SetHealth(ctx, id string, state domain.ProviderHealth) error` | Explicit state update without adapter calls |
| `Get(ctx, id string) (Provider, Snapshot, error)` | Adapter reference plus one copied snapshot; not eligibility |
| `List(ctx) ([]Snapshot, error)` | One coherent whole-registry snapshot, lexical provider-ID order |

All methods take `context.Context`. Tags, Hardware, and latency pointers are
copied on discovery/read; adapters remain caller-owned references. Discovery
is synchronous/cooperative outside locks. No registry polling, expiry, removal,
replacement, hidden timeout, or global state. Failed refresh publishes nothing;
concurrent valid refreshes are last-publication-wins at the publish lock, not
start-order freshness. Publication preserves intervening explicit health updates.

**Verified self-review correction:** always check adapter ID
before/after capability discovery, including on error. Only a successful
capability result must have matching ProviderID; payloads returned with errors
are ignored. Stable adapter errors classify as `CodeDiscoveryFailed` with
`ErrInvalidProvider`, not false identity mismatch; real adapter-ID drift retains
`ErrIdentityMismatch` precedence. Parent cancellation retains precedence/causes.
Six regressions were RED before/GREEN after, with failed-refresh nonpublication
guards. This lead self-review correction was the production fix; formal review
findings concerned test oracles only, not production defects.

### Router API and deterministic admission

`provider/router.go` exposes `NewRouter(registry *Registry) (*Router, error)`;
nil registry is invalid. `RouteRequest` contains RequiredTags, PreferredIDs,
Locality, `MaxCost *CostLimit`, `MaxLatencyMs *int64`, and `Hardware HardwareFacts`.
`CostLimit` has Amount/Unit; HardwareFacts contains `AvailableMemoryGB *float64`
and `HasGPU *bool`. Nil facts are unknown; zero/false pointers are known facts.
`RouteRequest.Clone()` copies all slices/nested pointers; `Validate()` rejects
invalid locality, empty/blank tags, blank/duplicate preferences, and invalid
limits/facts. Caller mutation during copying is not supported.

`Route(ctx, req RouteRequest) (RouteResult, error)` filters one List snapshot
without calling any adapter method, including ID. Require ALL tags with exact
case-sensitive, untrimmed matching; locality zero/`LocalOnly` is local-only,
`RemoteOnly`/`AnyLocality` explicitly opt in to remote eligibility. Only HEALTHY
passes. Apply every gate before eligible preferences (caller order), then
deterministic providers (including opted-in remote deterministic), local
nondeterministic, and remaining remote providers; lexical ID breaks tier ties.
Unknown/ineligible preferences are ignored, never a substitute for eligibility.

`RouteResult.EligibleIDs` contains first choice and all eligible alternatives;
`FallbackIDs` independently copies the tail. Rejections are lexical-ID ordered,
with owned code slices in health/tags/locality/cost/latency/hardware order.
When none are eligible, Route returns details with `ErrNoEligibleProviders`.
Fallback never executes, retries, or relaxes constraints.

Local Hardware nil is unknown/ineligible; explicit empty HardwareRequirements
means no minimum. Positive MinMemoryGB requires known sufficient memory;
RequiresGPU requires known true HasGPU. Unknown caller facts matter only for
positive requirements. Remote requirements are not compared to local facts.
These checks do not detect resources, reserve memory, or implement Phase 08 leases.

`Resolve(ctx, id string, req RouteRequest) (Provider, error)` admits exactly id
from a fresh Get snapshot, then checks adapter ID outside locks; no fallback
substitution or capability discovery. This is point-in-time admission, not a
lease: health/capabilities can change after capture and before execution.

### Decision-side bridge and fixture

`decision/provider_resolver.go` imports provider (not vice versa).
`NewProviderResolver(router *provider.Router, policy provider.RouteRequest)`
returns `(*ProviderResolver, error)`, deep-copies policy, and adds mandatory
`semantic-decision` to ALL-required-tags (even with initially empty tags).
Its `Resolve(ctx, id) (decision.Provider, error)` performs exact router admission,
asserts a real decision.Provider implementation, and rechecks identity/context.
It maps permanent provider identity/not-found/admission errors to Phase 06
sentinels while retaining causes, preventing embedded transient markers from
retrying invalid providers. The engine and its eight-provider cap are unchanged:
callers explicitly pass chosen EligibleIDs to `DefaultConfig(ids...)`.

`decision/fixture.go` now declares local semantic-decision + deterministic,
explicit empty HardwareRequirements, and free `USD/decision`; latency stays nil
(unknown). No billable service, model, network, or process is invoked; electricity
cost is not estimated. No Director/shot-intelligence/pipeline wiring is added.

### Lifecycle, diagnostics, and evidence

IDs must be stable, nonsecret, fast/nonblocking (ID has no context). Caller owns
safe concurrent adapter lifecycle and stable metadata during copying; discovery
and execution are not serialized by registry locks or detached goroutines.
`provider/errors.go` supplies safe `Error.Code()`/core-only strings and copied
`Unwrap() []error` causes for `errors.Is/As`; callers must redact raw causes.
Routing rejection records expose nonsecret handles/core codes, not raw metadata.
No generation, installation, retries, polling, resource enforcement, or leases.
Evidence: `docs/phases/phase-07.md`; lead-owned formal records:
`docs/reviews/phase-07-review-luna6.md`, `docs/reviews/phase-07-review-astra.md`.
Luna6 (`amazon-bedrock/us.openai.gpt-6-luna`) final scoped provider unit/race/vet
PASS and APPROVE preceded Astra (`amazon-bedrock/us.openai.gpt-6-astra`) final
provider+decision unit/race/vet/diff-check PASS and APPROVE.

Both LOW findings were closed by test-only hardening with mutation proofs:
independent literal snapshot expectations reject a shallow-clone mutant that
passed the original test; clearing ALL returned Unwrap causes now rejects an
aliased-cause mutant in all six cases while preserving permanent sentinels and
original typed causes. Original production copying was restored in both proofs;
the lead verified empty production diffs. No new product policy was introduced.
Final post-confirmation gate and 120-tracked-file hygiene scan pass. Single
commit: `phase(07): implement provider registry and routing` (resolve its hash
from repository history).
The lead continues automatically to Phase 08 after §14 and that single local
commit under continuing owner authority; no fresh prompt or automatic push.

## Phase 08 — Model Manager / Resource Planner (as-built draft)

**Phase 08 complete (2026-10-11), recorded by its phase commit.** Started
2026-10-10 above `dbc5ace`. Resource/domain, worker, and model source is present;
Model APIs are source-inspected; no active code/test writers. Scoped PASS is reported evidence.
Main uncached whole gate PASS; Terra then Luna formally APPROVED sequentially.
Terra's LOW stale-gate text finding is closed; no outstanding formal findings.
Final post-review lead full uncached test/race/build/vet gate PASS; formatting
and whitespace clean. Final staged diff and hygiene checks pass.
Authority: `PHASES.md` §0.2/Phase 08 and ADR-021; no renewed routine prompt/auto-push.

### Source boundaries and resource API

- `resource/{config,math,manager}.go`: explicit budget validation, exact decimal
  arithmetic, integer-byte RAM and CPU/GPU micro-units, atomic FIFO admission.
- `resource/{profiles,estimate,history,planner}.go`: revisioned profile validation,
  conservative estimates, bounded copied evidence, advisory local/remote planning.
- `domain/model_requirements.go`, `domain/model.go`, `domain/resourceplan.go`:
  workload/envelope/profile declarations, requirements, and estimate provenance.
- `worker/{runner,protocol,monitor,platform_unix,platform_other,peak,peak_darwin,peak_linux,peak_other}.go`:
  bounded direct-exec/process ownership.
- `model/{model,manager,lifecycle,handle,errors}.go`: explicit model state/use ownership,
  frozen admission, cooperative loader lifecycle, quarantine/refund, safe diagnostics.

Existing `resource.Manager.Acquire(ctx context.Context, req Request) (Lease, error)`
and Lease `ID() string`/`Release() error` remain. Request adds Heavy and Backend;
RAMBytes/CPU/GPU/Exclusive are retained. `NewManager(cfg Config) (*BudgetManager, error)`
constructs the manager (zero value unusable); `Config() Config`, `Snapshot() Snapshot`,
and `Snapshot.AvailableNow(r Request) (bool, error)` expose copied/advisory state.
Concrete release is bounded, concurrency-safe, idempotent; no Close/lease expiry.

`ReferenceConfig(cpuCapacity, workerCPU float64, accelerator AcceleratorFacts,
workerGPU float64, hardwareClass string) Config` requires supplied facts.
Reference decimal GB=10^9: 16 - 3 system - 4 user - 1.5 Director = 7.5 worker GB;
MaxParallel=2, MaxHeavy=1, queue=64, heavy threshold=2 GB. Requests ceil and
capacities floor to 10^6 micro-units using exact round-trippable decimal math.
Heavy is explicit OR positive GPU OR threshold RAM; false flags cannot bypass it.
RAM/CPU/GPU/slot/heavy/exclusive grants are atomic; permanent failures never queue.
FIFO head blocking is intentional; cancel removes/wakes, observed pre-handoff
cancel rolls back. Successful handoff stays owned until explicit Release.
Metal != CUDA; GPU shares are logical, not measured VRAM caps. Unified memory
counts in RAM; no kernel RAM/CPU/GPU containment or ambient hardware detection.

### Frozen profiles, high-water history, advisory planning

ModelRequirements version 1 requires explicit model/profile revisions. Profiles
declare trusted known load/resident/workspace bounds and supported precision,
dimensions/frames/quality/offload/chunk envelopes, including cleanup workspace.
Unknown/untrusted bounds cannot admit locally. Legacy MinMemoryGB is a floor;
peak=max(load,resident+workspace,ceil(legacyGB*GB),matching observed peak), then
`ApplySafetyMargin(bytes, percent int64) (int64, error)` (reference 25%).
No model-name/weights-to-RAM formula. Clone helpers isolate nested declarations.

`NewHistory(limit int) (*History, error)` uses 256 when limit=0; Record/Snapshot/
Lookup copy exact provider/model/revisions/profile/hardware-class/workload keys.
Full history returns ErrHistoryFull for new keys, never evicts learned maxima;
existing updates may raise but not erase peaks. Snapshot Version/Hash/Entries
provides deterministic provenance, not ambient freshness or automatic persistence.
FamilyRSS and OS single-process peak remain separately labelled; nil is unknown.
`EstimateRequest(...) (Estimate, error)` uses frozen declarations/config/history,
never an LLM-edited ResourcePlan. Exact signature reference: `docs/phases/phase-08.md`.
`NewPlanner(snapshots SnapshotSource, history HistorySource, cfg PlannerConfig)`
returns `(*Planner, error)`; `Plan(ctx context.Context, req PlanRequest) (PlanResult, error)`
distinguishes local_ready/wait/local_ineligible/remote_available. Explicit remote
policy requires Phase 07 RemoteOnly gates; PreferRemote never permits spending.

### Model API, ownership, and stopped-proof accounting

`model/model.go`: Key{ProviderID, ModelID, Revision}; `Key.Validate() error`.
LoadSpec owns exact Key/Profile/Workload. Loader: `Identity() Key`,
`Load(context.Context, LoadSpec) error`, `Unload(context.Context) (stopped bool, err error)`.
Budget embeds resource.Manager plus `Config() resource.Config`; History exposes
`Snapshot() resource.HistorySnapshot`, `Record(resource.HistoryKey, resource.Measurement) error`.
Config has LoadTimeout/UnloadTimeout/SafetyMarginPercent/Now; `DefaultConfig() Config` gives
cooperative 30s/5s/25%/parent clock. Explicit Config validated, not silently defaulted.

`model/manager.go`: `NewManager(budget Budget, history History, cfg Config) (*Manager, error)`;
`Register(ctx context.Context, caps domain.ModelCapabilities, loader Loader) error`;
`Acquire(ctx context.Context, key Key, profileID string, workload domain.Workload) (*UseHandle, error)`;
`Snapshot() Snapshot`. Register freezes revision-matching copies, never loads/replaces;
Acquire recomputes current budget/history, not LLM plans; busy transitions refuse, not queue.
Full peak RAM/CPU/GPU/slot/heavy/exclusive reservation precedes Load and survives
use release/cancellation. Idle reuse requires exact profile/workload and sufficient
held reservation; history-raised need returns ErrNeedsUnload, with no nested Acquire.

`model/lifecycle.go`: `Unload(parent context.Context, key Key) error` only refunds
after stopped proof with matching pre/post identity AND successful lease Release.
Loader/budget/history/clock callbacks outside mutex, including reentry; identity checked
on every loader return/error. Failed/late/canceled Load uses fresh bounded cleanup/quarantine.
Identity-valid Unload true can refund despite callback error/timeout, while reporting
the error. Failed lease Release retains token/accounting/stopped proof in quarantine;
explicit Unload retry performs bookkeeping only, not a second loader Unload.

`model/handle.go`: `UseHandle.Key() Key`, `Spec() LoadSpec`, `Estimate() resource.Estimate`;
`Release() error` is bounded/idempotent/concurrent-safe and only clears use ownership.
Private shared tokens prevent copied/stale handles from clearing a subsequent owner.
`RecordMeasurement(measurement resource.Measurement) error`: trusted parent/exact key, stale refused;
`ObservationError() error`: advisory load-history failure, successful Acquire retains valid handle.
Owned lexical Models: state/spec/estimate/reservation/StoppedVerified/Observations/HistoryFailures/LastCode;
no loaders/contexts/tokens/raw diagnostics. States unloaded/reserving/loading/in_use/loaded_idle/unloading/quarantined.
`model/errors.go` exposes core codes and copied unwrap causes; raw causes need redaction.

### Stable worker API, protocol, and trusted runtime boundary

`worker.NewRunner(c Config) (*Runner, error)`: nil Monitor→NativeMonitor, nil Clock→parent time,
zero CleanupTimeout→5s; typed-nil monitor pointers/functions rejected, not absent configuration.
`Runner.Run(ctx context.Context, spec CommandSpec, request Request, limits Limits)`
returns `(RunResult, error)`. RunResult has Reply, resource.Measurement, Stopped,
Cleanup, StderrCapturedBytes, StderrTruncated; raw stderr is never returned.
CommandSpec.Exe is absolute; Argv/CWD/Env are trusted runtime config excluded
from JSON, never GenerationParams. Nil Env means explicitly empty, not ambient.
Limits supply positive RAMBytes, optional Timeout, and SampleInterval (zero=100ms).

Protocol version 1 supports only generate; Header has request/provider/model/
model-revision/profile/profile-revision handles, each 1–128 restricted bytes.
Request/Reply use bounded RawMessage with existing domain generation DTO codecs.
Stdin/stdout limit=1 MiB each, stderr capture=8 KiB with excess drained/discarded.
Wire size is checked before envelope allocation; valid payload bytes are retained.
Exact-key token scanning rejects aliases/duplicate decoded keys (including escaped
equivalents) at known DTO boundaries. Extension maps allow case-distinct keys,
not exact duplicates; error payloads remain opaque/ignored. Successful headers
must equal the request, and result RequestID must match. Only fixed core diagnostics.

### Stop proof and parent measurements

Unix Setpgid owns a group; one Wait reaps the direct child, owned I/O/monitor tasks
are joined, and remaining live descendants are killed even on normal leader exit.
NativeMonitor uses bounded contextual ps queries; failures fail closed, not absence.
Workers must not daemonize/escape; this is trusted execution, not a malicious-code sandbox.
`CleanupHandle.RetryStop(ctx context.Context) (bool, error)` requires a deadline.
Only proven no-live-group/reap/join returns true; false retains a quarantine handle
and all remaining tasks for explicit retry without duplicate pending query/Wait.
**True can accompany ErrLimitExceeded**: stopped proof and sticky RAM breach are
separate facts. Caller retains its lease on uncertain stop; worker never acquires/
refunds leases. Descendant zombies are distinguished; adoptive parent reaps them.

Run and cleanup observations share high-water/breach accounting. Cleanup-only
breaches retain partial evidence, suppress Reply, and still finish stop proof.
Active monitor failures after leader reap remain errors; intentional canceled
observations ignored. Family RSS, OS process peak, and parent milliseconds have distinct
source/completeness; missing metrics nil. macOS peak bytes/Linux KiB converted; no instant wall.

### Reviewed implementation — final checks and phase commit

Lead reports post-history-correction targeted/scoped normal/race/vet PASS and
fresh worker/resource/domain normal, worker race/vet PASS; typed-nil red/green
and lead rerun PASS. Supplementary Astra audit began 2026-10-10; M1–M3 MEDIUM
and L1 LOW are closed, component APPROVE on 2026-10-11, not formal phase Review 1/2.
Independent original five overlay regressions/race/native-helper/scoped checks PASS.
Lead Linux/arm64 and Windows/amd64 cross-builds PASS; worker-owner Darwin/Linux
arm64 test-compilation PASS is reported. macOS helper/pipe cleanup unit/race PASS;
Linux runtime unverified, Windows Run explicitly unsupported.
Original model writer canceled before tests; five production files preserved, not
rewritten (formatting only). Bounded replacement supplied three manager smoke tests;
lead independent model normal/race/vet PASS. Peer test-only lifecycle/ownership
files each add five tests, targeted normal/race/vet PASS reported; no production fix needed.
Main `go test -count=1 -timeout=120s ./...`, `go test -race -count=1 -timeout=180s ./...`,
`go build ./...`, `go vet ./...`: PASS by lead execution and Terra's independent
formal review rerun. Luna independently passed tests/race/vet/Linux+Windows builds/diff checks.
Final post-review lead rerun PASS; formatting/whitespace, staged diff, and
157-tracked-file hygiene checks pass. Single commit:
`phase(08): implement resource and model management` (resolve hash from history).
No tests run by documentation owner; no real ML/inference/network/installs introduced.
Formal evidence: `docs/reviews/phase-08-review-terra.md`, `docs/reviews/phase-08-review-luna.md`;
Astra worker audit remains supplementary. Phase evidence/resume: `docs/phases/phase-08.md`,
`docs/handoffs/next-session-phase-08.md`. Planned commit: `phase(08): implement resource and model management`.

## Phase 09 — Validation / Shot Intelligence (as-built, closeout pending)

**IN PROGRESS — REVIEW/CLOSEOUT PENDING (2026-10-11).** Above `61e078b`; no Phase 09
commit. The historical ownership bugs are fixed, the lead full gate passes, and Astra
approved; Terra evidence confirmation remains. No active code/test writers. This
section is as-built documentation, not phase completion.

### Measured validation API and result boundary

- `validation/validation.go`: `Quality = domain.Quality`, with shared draft/final
  constants. `Requirements` explicitly supplies Kind/Quality/ExpectedDuration/FPS/
  Width/Height, AudioRequired, ImageMinimumDimensions, and WarnUniformFrame.
  `Requirements.Validate()` checks kind-specific applicable fields and finite bounds.
- `NewMediaChecker(inspector Inspector, cfg Config) (*MediaChecker, error)`;
  `Validate(ctx context.Context, req Request) (domain.ValidationResult, error)`.
  Request has ID/TargetID/Asset/Requirements; domain result/envelope is unchanged.
  `DefaultConfig()` is 5s, ±150ms duration, ±0.01 FPS; no label-derived resolution.
  `GenerationResult.DurationMs` is generation wall time, never media duration.
- `Inspector.Inspect(context.Context, InspectionRequest) (InspectionFacts, error)`;
  request URI/Kind, measured Kind/Duration/Width/Height/FPS/CodecName, AudioTracks/
  SampleRate/Channels, VisualDecoded/AudioDecoded, optional UniformFrame.
  Asset.Metadata is neither trusted nor passed to the inspector.
- Invalid dependencies/requirements/nil context are errors; parent cancellation
  wins and late/error payloads are ignored. Media failures return failed results;
  `Details` contains quality plus bounded core `Issue{Code, Severity}` values.
  Status is passed/failed/warning. Safe errors unwrap causes for `errors.Is/As`;
  raw causes require redaction. Retry advice is false unless explicitly transient/
  child timeout; permanent source/media/runtime/limit markers override retry.

### Native boundary and narrow renderer changes

- `validation/native.go`: `DefaultNativeConfig(roots ...string) NativeConfig`,
  `NewManagedNativeInspector(cfg NativeConfig) (*ManagedNativeInspector, error)`;
  `Inspect(ctx, InspectionRequest) (InspectionFacts, error)`.
  Trusted config owns roots/RelativeRoot/WorkingDir/executable selection/timeout.
  Empty roots are rejected; regular authorized local files only, no protocol URIs
  or symlink/root escapes. Opened-file identity is checked, not a hostile-FS sandbox.
- Source <=1,000,000,000 bytes; <=8192 per dimension and <=16,777,216 pixels.
  PNG/JPEG/GIF use DecodeConfig guards then image.Decode (not all GIF animation frames).
  Video/audio header selects forced mov/matroska/wav/flac/mp3 demuxer; file-only
  protocols, MOV external references disabled, decoder stdin disabled, one thread.
- Strict bounded native JSON rejects unknown/duplicate keys and invalid numbers;
  audio tracks count independently of stream order, every audio stream's facts
  checked. Decode one 8x8 midpoint thumbnail and first audio stream's first 20ms.
  Uniform frame is opt-in warning only; flat/static/dark/fade content remains valid.
- `validation/process.go`: direct contextual exec, no shell; stdout <=64 KiB,
  stderr <=8 KiB, breach cancels, raw stderr omitted. Timeout defaults 5s.
  Per-allocation/native guards are not aggregate/kernel RAM/CPU/GPU containment.
- `render/probe.go` counts `ProbeResult.AudioTracks` across all streams;
  `render.SupportsPrimitive` in `render/support.go` reports Background/Image/Text/
  Transition only, not properties, tool availability, or conceptual registry support.

### Advisory intelligence API and current limitations

- `intelligence/{intelligence,helpers}.go`: `New(primitives Primitives,
  validator validation.Validator, router Router, decider Decider, planner Planner)
  (*Analyzer, error)`; `Analyze(ctx context.Context, req Request) (Result, error)`.
  Primitives/validator/router required; optional typed-nil decider/planner absent.
- Request supplies authoritative Project/ShotID/ID, explicit VIDEO requirements
  with shot-matching duration, <=8 distinct Candidates and <=16 trusted Bindings.
  Candidate has AssetID and typed Suitable/ImageSufficient/AnimationSuitable verdicts;
  <=8 distinct AudioRequiredTags explicitly attest requested composition capability,
  not arbitrary route tags. Association never means asset IDs in Shot.DependsOn.
- Strategies are Deterministic/ReuseVideo/StaticImage/AnimateImage/GenerateVideo/
  OpNone, all advice. Result separates ConceptualPossible/RenderedSupport/InputsReady,
  MissingAudioReadiness, source/judgment/validation evidence, routing/resource status.
  Known cheap facts bypass classification; unsupported deterministic intent stays
  deterministic. Only associated project assets are measured; suitability must be positive.
- Optional structured Decider (including decision.Engine) receives a bounded scalar
  descriptor: duration/count/candidate presence/dimensions/FPS/audio, no prose or
  primitive-type detail. This limits semantic usefulness; unknown/uncertain/errors
  cannot manufacture a choice. Callbacks have cooperative 5s bounds, no retries.
- Routing adds exact capability/audio tags without relaxing Phase 07 policy.
  Optional resource advice checks selected provider/model/profile/revisions/workload/
  provenance. Local-only matching evidence preserves local_ready/wait/local_ineligible.
  Any/Remote routing can be explicitly requested, but resource evidence stays unknown:
  RouteResult cannot attest locality. No implemented remote_available bridge is claimed.
- `Analyze` snapshots JSON-compatible input into a fresh Request before callbacks;
  router and planner callbacks receive independent policy/request copies while the
  analyzer retains the trusted binding baseline for provenance checks. Router output
  is copied and shape/vocabulary checked before advice is exposed. Frame counts use
  round-trippable decimal FPS values. Native deadline handling preserves permanent
  media/limit markers for retry classification while parent cancellation remains
  authoritative. These are advisory safety boundaries, not execution permission.
- No generation/render execution, resource acquisition/model loading, cache/player,
  speech/timestamps, provider installs/downloads, paid calls, or new domain schema.

The historical acceptance RED checkpoint is retained in phase evidence. After the
ownership and Astra R1 boundary fixes, the lead full uncached test/race/vet/build gate
passes and Astra approved. Terra review and phase closeout remain pending.
Evidence: `docs/phases/phase-09.md`; resume: `docs/handoffs/next-session-phase-09.md`.
