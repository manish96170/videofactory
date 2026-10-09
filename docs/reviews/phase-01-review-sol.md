# Phase 01 Review — Reviewer 1 (sol / GPT-5.6 Sol)

**Phase:** 01 — Project State / Timeline
**Reviewer:** sol
**Model:** `amazon-bedrock/global.openai.gpt-5.6-sol`
**Date:** 2026-10-09
**Commit reviewed:** pre-commit (staged working tree, Phase 01 implementation)
**Scope:** `idgen/*.go`, `project/*.go` (and their `_test.go` files), plus
`ARCHITECTURE.md`/`DECISIONS.md` Phase 01 sections.

## Round 1 findings and resolutions

### BLOCKER
1. **B-01 — Timeline/Asset state not part of the persisted Project.**
   `domain.Project` only embedded `*VideoPlan`; `Timeline`/`Asset` were
   unreachable from a saved project.
   **Resolution:** Added `Timeline *domain.Timeline` and
   `Assets []domain.Asset` to `domain.Project` (DECISIONS.md ADR-010);
   `Validate`/`Marshal`/`FileStore` now cover the whole aggregate.

### HIGH
1. **H-01 — Non-standard ULID bit packing** (padded at the end instead
   of 2 leading zero bits). **Resolution:** rewrote `idgen.encode` to
   use standard ULID padding; pinned with `TestEncodeKnownVector` and
   `TestNewFirstCharacterIsWithinULIDRange`.
2. **H-02 — Silent CSPRNG failure fallback.** **Resolution:** `newAt`
   now returns an error, `New` panics; `randReader` is injectable for
   `TestNewPropagatesEntropyFailure`/`TestNewPanicsOnEntropyFailure`.
3. **H-03 — `Load` didn't verify the loaded project's ID.**
   **Resolution:** added `ErrIdentityMismatch` check; test
   `TestFileStoreLoadRejectsIdentityMismatch`.
4. **H-04 — `ValidateTimeline` too weak.** **Resolution:** added
   non-negative/duplicate/duration checks (see round 2 for the
   element-placement and entry-duration-match additions).
5. **H-05 — Docs overstated behavior.** **Resolution:** `ARCHITECTURE.md`
   /`DECISIONS.md` rewritten to match the code exactly.

### MEDIUM (M-01 through M-08) and LOW (L-01 through L-03)
All resolved — write durability (`fsync` + rename + best-effort
directory `fsync`), cancellation re-check before commit, duplicate/empty
shot-ID rejection and edge dedup in `BuildDependencyGraph`, `CycleError`
reporting only the true cycle, plan/shot/primitive structural checks,
documented last-writer-wins concurrency, typed sentinel errors
(`ErrCorrupt`/`ErrInvalid`/`ErrIdentityMismatch`), `ErrNotFound` context,
and `idgen` as the module-wide ID authority (`project.NewID` is just a
local convenience). See `DECISIONS.md` ADR-009/ADR-011 and the Phase 01
section of `ARCHITECTURE.md`.

## Round 2 (confirmation re-review) — new findings and resolutions

1. **HIGH — `VisualElement` could reference a known shot with no
   `TimelineEntry`**, making it unreachable from
   `EntryForShot`/`AbsoluteStart`. **Resolution:** `ValidateTimeline` now
   requires every element's shot to be placed on the timeline. Tests:
   `TestValidateTimelineRejectsElementOnUnplacedShot`,
   `TestValidateTimelineAcceptsElementOnPlacedShot`.
2. **MEDIUM — `Project.Assets` had no structural validation.**
   **Resolution:** `validateAssets` checks ID presence/uniqueness and
   required `Kind`/`URI`. Tests: `TestValidateRejectsAssetProblems`,
   `TestValidateAcceptsWellFormedAssets`.
3. **MEDIUM — `FlowID`/`FramingID` could be empty.** **Resolution:**
   `validatePlan` now requires both non-empty (not registry membership,
   which stays Phase 04 scope). Test:
   `TestValidateRejectsEmptyFlowOrFramingID`.
4. **MEDIUM — forward dependency edges (`dependsOn`) not deduped**
   (only reverse `dependents` were). **Resolution:** deduped both sides
   in `BuildDependencyGraph`.
5. **LOW — stale ULID test comment/assertion** (claimed 9-character
   timestamp prefix instead of 10). **Resolution:** fixed comment and
   assertion to `a[:10] == b[:10]`.
6. **LOW — ADR-010 incorrectly implied `Asset` has a back-reference.**
   **Resolution:** corrected wording.

Also converged with reviewer 2's parallel finding that `TimelineEntry`'s
placed duration must equal its shot's `Duration` exactly — added that
check (`TestValidateTimelineRejectsEntryDurationMismatch`) and `Load`
re-checking `ctx.Err()` after the file read.

## Final status (after round 3 confirmation)

**READY FOR PHASE COMMIT.** All BLOCKER/HIGH/MEDIUM/LOW findings across
both rounds resolved. `go build/vet/test/test -race` all pass against
the final staged state.
