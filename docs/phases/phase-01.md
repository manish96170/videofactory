# Phase 01 — Project State / Timeline

## Goal

Implement project persistence and the fundamental video data model
(PHASES.md "PHASE 01 — PROJECT STATE / TIMELINE").

## Implemented

- **`idgen` package:** standard ULID generation (48-bit ms timestamp +
  80-bit `crypto/rand` randomness, Crockford Base32, standard 2-bit
  leading padding). Entropy failures are surfaced, not silently
  degraded.
- **`domain.Project` extended** to own `Timeline *domain.Timeline` and
  `Assets []domain.Asset` alongside `Plan`, so the whole project
  aggregate persists as one consistency boundary.
- **`project` package:**
  - `New`, `NewID` — project construction with stable IDs.
  - `Validate`, `ValidateTimeline`, `validateAssets` — structural
    integrity: required fields, Project/Plan/Shot/Timeline/Asset
    linkage, shot/primitive/asset ID uniqueness, positive shot
    durations, required Flow/Framing references, dependency
    referential integrity and acyclicity, Timeline/Element consistency
    (unknown-shot references, well-formed intervals, one placement per
    shot, placed duration matching shot duration, every element's shot
    actually placed on the timeline, element bounds within its shot's
    duration).
  - `DependencyGraph`, `BuildDependencyGraph`, `TopologicalOrder`,
    `Dependents`, `AffectedBy` — the shot dependency graph, with cycle
    detection and deduplicated edges.
  - `AbsoluteStart`, `AbsoluteEnd`, `ElementsForShot`, `EntryForShot` —
    concrete shot-relative/project-relative timing helpers (ADR-007).
  - `Store`, `FileStore`, `Marshal`/`Unmarshal` — versioned, atomic,
    durable, path-traversal-safe JSON persistence with typed sentinel
    errors (`ErrNotFound`, `ErrUnsupportedSchemaVersion`, `ErrCorrupt`,
    `ErrInvalid`, `ErrIdentityMismatch`).

## Design decisions

See `DECISIONS.md` ADR-009 through ADR-012:
- ADR-009: standard ULID IDs via `idgen`; path-traversal guard in
  `FileStore`.
- ADR-010: `domain.Project` owns `Timeline`/`Assets`.
- ADR-011: typed sentinel errors, identity verification, write
  durability, documented concurrency semantics.
- ADR-012: second review round — element-placement requirement,
  entry-duration-must-match-shot invariant, asset validation,
  Flow/Framing presence, forward-edge dedup, `Load` cancellation
  re-check, test-comment/doc corrections.

## Files changed

New: `idgen/idgen.go`, `idgen/idgen_test.go`, `project/project.go`,
`project/validate.go`, `project/validate_test.go`,
`project/dependency.go`, `project/dependency_test.go`,
`project/timeline.go`, `project/timeline_test.go`, `project/store.go`,
`project/store_test.go`. Modified: `domain/project.go`,
`ARCHITECTURE.md`, `DECISIONS.md`, `ROADMAP.md`.

## Tests

```text
go build ./...        PASS
go vet ./...           PASS
go test ./...          PASS (all packages)
go test -race ./...    PASS (all packages)
gofmt -l .              (clean, no output)
```

Coverage highlights: ULID known-vector and first-character-range tests;
CSPRNG-failure injection; full project (plan+timeline+assets) round
trip and modify-then-resave lifecycle; path-traversal rejection on both
`Load`/`Save`; corrupt-file and schema-version-mismatch rejection;
identity-mismatch rejection; concurrent distinct-project save/load under
`-race`; dependency cycle detection excluding acyclic prefixes;
duplicate/dangling/empty shot-ID rejection; full `ValidateTimeline`
invariant coverage (placement, duration match, bounds, duplicates).

## Review 1

Reviewer: sol (`amazon-bedrock/global.openai.gpt-5.6-sol`). Three
rounds (initial review, fix confirmation, final confirmation). See
`docs/reviews/phase-01-review-sol.md`. Final verdict: **READY FOR PHASE
COMMIT**.

## Review 2

Reviewer: luna6 (`amazon-bedrock/us.openai.gpt-6-luna`). Three rounds.
See `docs/reviews/phase-01-review-luna6.md`. Final verdict: **READY FOR
PHASE COMMIT** (contingent on this evidence + checklist sync, now done).

## Findings resolved

Every BLOCKER/HIGH/MEDIUM/LOW finding from both reviewers, across all
rounds, was resolved — none deferred. See the two review files for the
full finding → resolution mapping.

## Known limitations

- `DependencyGraph` traversal (`TopologicalOrder`, `AffectedBy`) is
  recursive; an adversarially deep/huge dependency chain could exhaust
  the goroutine stack. Acceptable for Phase 01's local, trusted-input
  scope; worth revisiting if projects become untrusted/very large.
- `FileStore` assumes a trustworthy local store directory (no symlink-
  substitution defense). A future untrusted/multi-tenant `Store`
  implementation would need to add that.
- Flow/Framing validation only checks presence of an ID, not that it
  resolves against a registry — registries are Phase 04 scope.
- `Store` has no built-in optimistic concurrency; same-project-ID
  concurrent `Save`s are last-writer-wins by design (documented on the
  `Store` interface).

## Commit

See the phase commit immediately following this evidence file in the
repository history (`phase(01): implement project state and timeline`).

## Follow-ups

- None blocking. Phase 02 (Event System) and Phase 03 (Deterministic
  Renderer) should keep the `Timeline`/`VisualElement` invariants
  established here (placement, duration matching, shot-relative
  timing) in mind when producing real timelines/renders.
