# DECISIONS.md

Lightweight architecture decision records for Video Factory. `PHASES.md`
is the execution authority; this file records decisions made while
executing it, especially ones not fully specified by the plan itself.

---

## ADR-001: Standalone git repository, isolated from an unrelated parent workspace

**Date:** 2026-10-09
**Status:** Accepted

**Context:** This project's working directory was initially located beneath an unrelated
parent workspace containing unrelated material.
PHASES.md requires this project to become its own public GitHub repository
with clean, phase-based commit history and strict secret/PII hygiene.

**Decision:** Run `git init` inside the project directory itself. This
project's history is fully independent of the parent workspace.

**Consequence:** Phase commits here never touch or risk exposing anything
from the parent workspace.

---

## ADR-002: Go module path / repository name

**Date:** 2026-10-09
**Status:** Accepted

**Decision:** The Go module path and intended GitHub repository name are
`github.com/manish96170/videofactory`, chosen explicitly by the project
owner rather than reused from the working-directory folder name
(`leo-deterministic-video-generator-lvg`).

---

## ADR-003: One `domain` package for core data contracts; small per-concern packages for behavior

**Date:** 2026-10-09
**Status:** Accepted

**Context:** PHASES.md section 5 lists all core data types together under
one "Core Domain Model" heading. PHASES.md section 19 separately asks for
small, capability-specific interfaces rather than one large interface.

**Decision:**
- All pure data contracts from section 5 live in `domain`.
- Behavioral interfaces that consume `domain` types live in their own
  small packages: `provider`, `decision`, `resource`, `event`.

**Consequence:** No duplicate/divergent type definitions across packages;
no "god interface"; package-qualified names stay idiomatic
(`provider.Provider`, not `provider.ProviderProvider`).

See `ARCHITECTURE.md` for the full rationale.

---

## ADR-004: Open string vocabularies instead of closed enums for categories/capabilities

**Date:** 2026-10-09
**Status:** Accepted

**Context:** PHASES.md repeatedly requires provider/model/flow/framing
independence (sections 4.1, 4.2, 7) and explicitly says shot categories in
section 6 are "recommended," not exhaustive.

**Decision:** `ShotCategory`, `PrimitiveType`, `AssetKind`, and capability
tags (`ProviderCapabilities.Tags`, `ModelCapabilities.Tags`) are typed
strings with exported constants for the known/recommended values, not
closed Go enums (`iota`-based types restricted to a fixed set).

**Consequence:** Later phases/providers can introduce new categories,
primitives, or capability tags without modifying `domain`. The tradeoff is
weaker compile-time exhaustiveness checking, which is acceptable given the
explicit pluggability requirement.

---

## ADR-005: ID generation strategy deferred to Phase 01

**Date:** 2026-10-09
**Status:** Accepted

**Decision:** Phase 00 defines `domain.ID` as a plain `string`-based type
with no generation helper. Phase 01 ("Project State / Timeline") owns the
decision of how stable IDs are actually produced (ULID, UUID, content
hash, etc.), per its own TODO item "Stable IDs."

---

## ADR-006: `decision.Policy` included in Phase 00 as a pure value type, not a decision engine

**Date:** 2026-10-09
**Status:** Accepted

**Context:** PHASES.md section 8 gives a concrete, deterministic threshold
example (`score >= 0.85 -> YES`, `score <= 0.30 -> NO`, otherwise
`UNCERTAIN`) as part of describing decision *contracts*. Phase 06 owns the
full Decision Engine (provider registry, fallback/escalation policy).

**Decision:** Phase 00 includes `decision.Policy` / `decision.Evaluate` as
a small, pure, provider-agnostic value type satisfying the "decision
contracts" TODO item, without implementing provider routing, registries,
or escalation — that scope stays in Phase 06.

**Amendment (same date, prompted by Phase 00 review #1/#2):** `Evaluate`
alone silently accepted inverted/out-of-range/non-finite thresholds
(e.g. `YesThreshold < NoThreshold`), producing a plausible-looking but
wrong verdict instead of a detectable error. Added `Policy.Validate()
error`, which callers constructing a `Policy` from configuration must
call; `Evaluate` itself remains a pure, side-effect-free function and
still does not validate (so it stays trivially composable), but its doc
comment now says so explicitly.

---

## ADR-007: Duration/timing serialization and relativity conventions

**Date:** 2026-10-09
**Status:** Accepted
**Prompted by:** Phase 00 review #1 (terra) and #2 (astra) — both flagged
undocumented duration units and ambiguous timing relativity.

**Context:** `domain` has two different kinds of time values: positions
on the video timeline (`Shot.Duration`, `TimelineEntry.Start/End`,
`VisualElement.Start/End`, `ReviewFeedback.Timestamp`) and real-world
operation timings (`GenerationResult.DurationMs`). Left undocumented,
`time.Duration`'s default JSON encoding (integer nanoseconds) and the
relativity of each timestamp (shot-relative vs. project-relative) were
ambiguous across packages.

**Decision:**
- Video-timeline positions use `time.Duration`, serialized by
  `encoding/json` as integer nanoseconds.
- Real-world operation timings use explicit `...Ms int64` fields
  (`GenerationResult.DurationMs`) instead, since they cross future
  process/worker boundaries where millisecond precision is sufficient
  and an explicit field name avoids re-deriving units.
- `TimelineEntry.Start`/`End` are project-timeline-relative.
- `VisualElement.Start`/`End` are shot-relative (0 = start of the shot
  named by `ShotID`), matching the per-shot `RenderManifest` convention.
- `ReviewFeedback.Timestamp` is shot-relative when `ShotID` is set,
  otherwise project-relative.
- All intervals are end-exclusive: `[Start, End)`.
- `Bounds` are normalized `[0,1]` coordinates, origin top-left.

These conventions are recorded as doc comments on the relevant types in
`domain/timeline.go`, `domain/visual.go`, and `domain/review.go`, and
exercised by `domain/domain_test.go`.

---

## ADR-008: `map[string]any` extension fields use plain JSON values; no schema in Phase 00

**Date:** 2026-10-09
**Status:** Accepted
**Prompted by:** Phase 00 review #2 (astra) — flagged that
`Properties`/`Payload`/`Input`/`Metadata` fields have no defined,
lossless JSON contract (e.g. large integers losing precision through
`float64`).

**Context:** Several contracts (`Primitive.Properties`,
`Change.Payload`, `DecisionRequest.Input`, `Asset.Metadata`, event
`Payload`) use `map[string]any` for open-ended extension data.
`encoding/json` decodes all JSON numbers in `any` fields as `float64`,
which loses precision for integers outside `float64`'s exact range.

**Decision:** For Phase 00, these fields are documented as holding
JSON-compatible values only, with the standard `encoding/json`
`float64`-for-numbers decoding behavior as the accepted default. No
schema validation or alternate numeric decoding (`json.Number`,
`UseNumber`, typed payloads) is introduced yet.

**Consequence / follow-up:** If a later phase needs lossless large
integers or typed payload validation (e.g. the Phase 02 event-worker
protocol, or Phase 13 ChangeSet mutation semantics), it must introduce
that handling explicitly at that boundary; callers should not assume
exact integer precision through these fields today.
