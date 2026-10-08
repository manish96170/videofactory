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

These are run before every claim of phase completion. Once Phase 00 is
actually committed, this section will reference the commit hash and the
evidence recorded in `docs/phases/phase-00.md` and `docs/reviews/` —
until then, treat this document as describing in-progress work, not a
completed, committed phase.

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
