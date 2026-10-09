# Phase 01 Review — Reviewer 2 (luna6 / GPT-6 Luna)

**Phase:** 01 — Project State / Timeline
**Reviewer:** luna6
**Model:** `amazon-bedrock/us.openai.gpt-6-luna`
**Date:** 2026-10-09
**Commit reviewed:** pre-commit (staged working tree, Phase 01 implementation)
**Scope:** `idgen/*.go`, `project/*.go` (and their `_test.go` files), plus
`ARCHITECTURE.md`/`DECISIONS.md` Phase 01 sections.

## Round 1 findings and resolutions

### HIGH
1. **Timeline omitted from the persisted `Project`.** **Resolution:**
   `domain.Project` now owns `Timeline`/`Assets` (DECISIONS.md ADR-010).

### MEDIUM
1. **Non-standard ULID encoding.** **Resolution:** standard 2-bit
   leading padding; known-vector and first-character tests added.
2. **`Load` identity mismatch not checked.** **Resolution:**
   `ErrIdentityMismatch` check added with a regression test.
3. **Invalid timeline/shot values accepted** (non-positive duration,
   negative starts, out-of-shot elements). **Resolution:** added the
   corresponding checks to `Validate`/`ValidateTimeline`.

### LOW
1. **Cycle error includes acyclic prefix.** **Resolution:**
   `TopologicalOrder` now slices from the repeated ID's first
   occurrence; test `TestCycleErrorExcludesAcyclicPrefix`.
2. **Cancellation checked only at method entry.** **Resolution:** `Save`
   re-checks before the final rename; `Load` re-checks after the read
   (added in round 2 after this reviewer flagged it was still
   incomplete for `Load`).
3. **Last-writer-wins undocumented.** **Resolution:** documented
   explicitly on the `Store` interface; added
   `TestFileStoreConcurrentSaveLoadDistinctProjects` under `-race`.

## Round 2 (confirmation re-review) — new findings and resolutions

1. **MEDIUM — `Project.Assets` persisted but not validated.**
   **Resolution:** `validateAssets` added (ID presence/uniqueness,
   required `Kind`/`URI`).
2. **MEDIUM — `TimelineEntry` duration could disagree with its shot's
   `Duration`.** **Resolution:** `ValidateTimeline` now requires
   `entry.End - entry.Start == shot.Duration` exactly. Test:
   `TestValidateTimelineRejectsEntryDurationMismatch`.
3. **LOW — ADR-010 incorrectly described an `Asset` back-reference.**
   **Resolution:** corrected.
4. **LOW — stale ULID test comment.** **Resolution:** fixed (first 10
   characters are timestamp-only with standard padding).
5. **Procedural note — `Load`'s cancellation check was still
   entry-only.** **Resolution:** added a second `ctx.Err()` check
   immediately after the file read.
6. **Procedural note — fixes were present only in the unstaged working
   tree, not `git diff --cached`, at the time of the first confirmation
   pass.** **Resolution:** re-staged (`git add -A`) before the final
   confirmation; verified via `git status` showing a clean,
   fully-staged diff.

Also converged with reviewer 1's parallel finding that `VisualElement`s
must be placed on a `TimelineEntry` to validate, that `FlowID`/
`FramingID` must be non-empty, and that `BuildDependencyGraph` should
dedupe forward edges too — all were applied in the same round.

## Final status (after round 3 confirmation)

**READY FOR PHASE COMMIT**, contingent on `PHASES.md`/`ROADMAP.md` being
synchronized to the final state and this evidence being recorded before
the commit — both done as part of closing out this phase.
`go build/vet/test/test -race` all pass against the final staged state.
