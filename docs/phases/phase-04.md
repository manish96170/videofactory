# Phase 04 — Flows / Framing / Primitives

## Goal

Make video composition reusable instead of hard-coded (PHASES.md
"PHASE 04 — FLOWS / FRAMING / PRIMITIVES").

## Implemented

- **`domain` extended** (additive, same pattern as Phases 01/03):
  `Flow` gained `DefaultFramingID`/`RecommendedCategories`; `Framing`
  gained `Slots []FramingSlot`; new `PrimitiveCapabilities` (same
  `Tags`-based capability pattern as `ProviderCapabilities`/
  `ModelCapabilities`).
- **`registry` package:**
  - `FlowRegistry` — seeded with `developer-explainer` and
    `code-tutorial`.
  - `FramingRegistry` — seeded with `fullscreen`, `split-screen`,
    `picture-in-picture`, `developer-circle`, `screen-focused`, each
    with named `Slots` (normalized `[0,1]` bounds).
  - `PrimitiveRegistry` — capability declarations for every
    `domain.PrimitiveType` constant, classified deterministic-vs-AI-
    capable per PHASES.md section 6.
  - `AssignSlots` — the generic framing-composition function: reads
    only `framing.Slots`, no `FramingID`-specific logic anywhere. This
    is the literal mechanism satisfying "ensure framing is not
    embedded into business logic."
  - All three registries are mutex-protected structs (not globals),
    with defensive slice copying on every `Register`/`Get`/`List` so
    the mutex's protection can't be bypassed via aliased slices.

## Design decisions

See `DECISIONS.md` ADR-017 (and its same-day amendment):
- Registries as plain structs, not globals.
- `AssignSlots` as the single composition entry point.
- `PrimitiveCapabilities` reusing the established `Tags` pattern.
- Amendment: defensive slice copying (a real concurrency/encapsulation
  bug both reviewers found), `NaN`/`Inf` bounds rejection, corrected
  `RequiredProperties` for `Background`/`Transition`, and strengthened
  composition "proof" tests.

## Files changed

New: `registry/flow.go`, `registry/framing.go`, `registry/primitive.go`,
`registry/compose.go`, and their `_test.go` files plus
`registry/concurrency_test.go`, `domain/primitivecapability.go`.
Modified: `domain/flow.go`, `domain/domain_more_test.go`,
`ARCHITECTURE.md`, `DECISIONS.md`, `PHASES.md`, `ROADMAP.md`.

## Tests

```text
go build ./...        PASS
go vet ./...           PASS
go test ./...          PASS (all packages)
go test -race ./...    PASS (all packages)
gofmt -l .              (clean, no output)
```

Coverage: registry seeding/lookup/listing for all three registries;
rejection of empty IDs, duplicate registrations, malformed framings
(no slots, empty/duplicate slot names, non-finite or out-of-frame
bounds); `AssignSlots` correctness (exact per-slot bounds across every
built-in framing, a runtime-registered custom framing, unfilled
optional slots, unknown-slot-name rejection, no mutation of caller
input); concurrent registry access under `-race`; and defensive-copy
isolation tests for every slice-typed field (verified to actually
catch the aliasing bug by deliberately reverting one fix and watching
the corresponding test fail).

## Review 1

Reviewer: terra (`amazon-bedrock/openai.gpt-5.6-terra`). Found a real
HIGH concurrency/encapsulation bug (slice aliasing defeating the
registries' mutex protection) plus three MEDIUM findings. See
`docs/reviews/phase-04-review-terra.md`. Final verdict: **READY FOR
PHASE COMMIT**.

## Review 2

Reviewer: sol6 (`amazon-bedrock/global.openai.gpt-6-sol`) — the first
attempt at this model slot failed with a provider error due to a
missing `global.` prefix in the model string; retried successfully.
Converged independently on the same HIGH finding and two MEDIUM
findings, plus two additional LOW findings. One LOW required a second
confirmation pass because the first fix claim was inaccurate (the test
still didn't check full content); fixed properly and reconfirmed. See
`docs/reviews/phase-04-review-sol6.md`. Final verdict: **READY FOR
PHASE COMMIT**.

## Findings resolved

Every finding from both reviewers resolved, including a genuine
concurrency/encapsulation bug (slice aliasing) found independently by
both reviewers and verified fixed by deliberately reverting and
re-applying the fix.

## Known limitations

- `developer-circle`'s overlay slot is a rectangular `Bounds` like
  `picture-in-picture`'s; no circular-mask/clipping rendering support
  exists yet. The registry only guarantees the slot's position, not
  its shape.
- `AssignSlots` doesn't itself validate a `Framing` passed directly
  (only `FramingRegistry.Register` validates); callers bypassing the
  registry are responsible for validity.
- No "override a built-in" mechanism — registering an existing ID
  always errors; a caller wanting different `fullscreen` behavior must
  use a new ID.

## Commit

See the phase commit immediately following this evidence file in the
repository history (`phase(04): implement flow/framing/primitive
registries`).

## Follow-ups

- None blocking. Phase 05 (Director / Video Planning) is a natural
  consumer of `FlowRegistry`/`FramingRegistry` (e.g. resolving a
  Flow's `DefaultFramingID`) and should keep using `AssignSlots` rather
  than reintroducing framing-specific branching.
