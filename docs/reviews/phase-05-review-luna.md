# Phase 05 Review — Reviewer 1 (luna / GPT-5.6 Luna)

**Phase:** 05 — Director / Video Planning
**Reviewer:** luna (independent, read-only agent)
**Model:** `amazon-bedrock/openai.gpt-5.6-luna`
**Date:** 2026-10-09
**Commit reviewed:** staged Phase 05 implementation on base `65a5441`;
pre-commit working tree, not a completed phase commit.
**Scope:** `director/*.go`, `project.ValidatePlan`, tests, and Phase 05
architecture/decision documentation.

## Findings and resolutions

### HIGH

1. **L1 — Cancellation lost after interpreter return.**
   `director/fixture.go` checked the context only at entry. An interpreter
   cancelling it and returning nil error could still yield a successful
   plan. Recommended: post-interpreter, per-shot, and pre-return checks;
   regression asserting `errors.Is(err, context.Canceled)`.
   **Resolution:** context checks added after interpreter execution,
   during fallback/construction, and before success. Regression tests
   assert cancellation identity and zero partial results, including
   cancellation before lookup or hint validation.
2. **L2 — Blank interpreter hints create empty content.**
   `director/fixture.go` accepted whitespace-only text hints, producing
   structurally valid but empty Text content or avatar scripts.
   Recommended: trim and reject blank hints, with tests for ordinary,
   avatar, and mixed valid/invalid hints.
   **Resolution:** hint text is trimmed on a local copy and blank text
   rejected with an indexed error. Six malformed-hint cases cover empty,
   whitespace, avatar, and mixed input; ownership tests cover repeated
   calls and normalization without changing interpreter-owned values.

### MEDIUM

1. **L3 — Category can contradict generated intent.**
   `director/fixture.go` could preserve an explicit CODE hint while avatar
   keywords generated a DeveloperAvatar primitive and non-deterministic
   intent. Recommended: normalize category before selecting primitives.
   Lead decision: retain conservative avatar detection and normalize
   avatar-selected shots to AVATAR.
   **Resolution:** category is normalized before primitive selection;
   explicit/keyword avatar intent forces AVATAR. Tests pin category,
   deterministic flag, primitive, asset linkage, and capability tags.
2. **L4 — Milestone test prerequisites incomplete.**
   `director/second_milestone_test.go` checked executable presence but not
   `drawtext` or usable fonts. Minimal supported FFmpeg installations
   would fail instead of skip. Recommended: equivalent prerequisite
   checks to the `render` tests and explicit available-font selection.
   **Resolution:** real text milestone checks `drawtext` and discovered
   font availability. Avatar rejection now asserts the specific unsupported
   primitive error without requiring tools/fonts, including empty PATH.

### Additional regression discovered while fixing

An empty interpreter hint slice with spare capacity could be overwritten
by fallback `append`. Fallback now starts from a separately allocated
slice; `TestPlanFallbackDoesNotMutateInterpreterHintBackingArray` pins
ownership. This is the same Phase 05 interpreter boundary, not new scope.

## Positive observations

Provider-independent interfaces; registry-backed flow/framing/capability
lookup; sorted, deduplicated capability metadata; minimal standalone-plan
validation export; useful deterministic and renderer-boundary coverage.
Reviewer found no obvious secret or local-path leakage in the diff.

## Tests

Reviewer ran `go test ./director ./project`,
`go test -race ./director ./project`, and `git diff --cached --check`:
all PASS. Full baseline build/vet/test/race and formatting results were
supplied by the lead, not independently rerun by this reviewer.

The implementation agent observed cancellation, blank-hint, text-trimming,
category, fallback-ownership, and no-native-tools regressions fail before
the fixes, then pass afterward. The lead inspected the diffs and reran
`go build ./...`, `go vet ./...`, `go test -count=1 ./...`, and
`go test -race -count=1 ./...`: all PASS; formatting clean.

Luna confirmation reran uncached director/project unit and race tests,
`go vet ./director ./project`, and `git diff --check`: all PASS. Confirmed
all four findings and the additional ownership regression resolved.

## Final status

Initial verdict: **REQUEST CHANGES**. Confirmation verdict:
**APPROVE / FINDINGS RESOLVED**. No outstanding findings from Luna.
After Astra's validation and coverage findings were fixed, Luna reviewed
the final code again, approved with no remaining HIGH/MEDIUM issues, and
reran uncached director/project unit/race tests, vet, and staged diff
checks (all PASS). Astra's final confirmation followed sequentially and
also approved. The final lead documentation/diff/hygiene/test gate passed;
this evidence is recorded with the single Phase 05 commit.
