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
