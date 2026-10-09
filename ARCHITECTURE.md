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
