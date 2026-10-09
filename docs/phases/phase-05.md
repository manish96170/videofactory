# Phase 05 — Director / Video Planning

> **DONE (2026-10-09), recorded in the Phase 05 commit.** Both independent reviewers
> approved the final code sequentially; all findings are resolved. Final
> lead build/vet/uncached test/race gate and formatting checks pass.
> Evidence, documentation, final staged diff, and hygiene checks pass.

## Goal

Prompt → `VideoPlan`, not rendering (`PHASES.md` Phase 05 and §29 milestone).

## Implemented

- **Planning contracts:** `Director.Plan(ctx, PlanRequest)` returns a
  `PlanResult` containing the plan, prompt/director/version/timestamp
  metadata, asset requirements, and required capability tags.
- **Fixture:** offline/model-free; explicit Phase 04 registry dependencies.
- **Flow selection:** request override → interpreter suggestion →
  avatar-keyword heuristic (`developer-explainer`, else `code-tutorial`).
  **Framing selection:** request override → flow default → `fullscreen`.
  Both selections use registry lookup and reject unknown IDs.
- **Shot decomposition:** one shot per sentence split on `.`, `!`, `?`,
  unless an interpreter supplies a nonempty hint list. Trim text locally;
  blank hints reject the entire list, not trigger fallback. Empty lists
  still permit fallback; interpreter values/backing arrays are never mutated.
- **Duration:** fixed reading pace of 2.5 words/second, with a 1.5-second
  floor. Shots receive fresh IDs and ordered indexes.
- **Generation intent:** ordinary shots use deterministic Background +
  Text; avatar intent uses `DeveloperAvatar`, marks the shot
  non-deterministic, and declares a `generated-video` requirement. AVATAR
  hints or avatar keywords normalize category to AVATAR over other hints.
- **Capabilities:** primitive-registry tags are deduplicated and sorted;
  no provider is selected or invoked.
- **Validation:** `project.ValidatePlan(projectID, plan)` requires nonempty
  expected project ID and matching plan linkage, then reuses structural
  checks. No storage lookup; validation runs before returning a plan.
- **Optional LLM seam:** `PromptInterpreter` proposes a flow and/or
  `ShotHint` values; core owns shot construction and validation, with
  heuristic fallback when suggestions are absent. No vendor adapter exists.
- **Cancellation:** checks at entry, after interpretation, during fallback/
  construction, and before success; wrapped interpreter errors retain identity.

## Design decisions

See `DECISIONS.md` **ADR-018 and review amendment** (2026-10-09):
model-neutral hints/heuristics, existing capability tags and Phase 01
validation; asset fulfillment/routing remain later-phase work. Fresh IDs/
timestamps mean structural determinism; framing selection is metadata-only.

## Files changed

- New implementation: `director/director.go`, `director/interpreter.go`,
  `director/decompose.go`, `director/fixture.go`.
- New tests: `director/decompose_test.go`, `director/fixture_test.go`,
  `director/second_milestone_test.go`.
- Modified validation: `project/validate.go`, `project/validate_test.go`.
- Supporting evidence/design: `ARCHITECTURE.md`, `DECISIONS.md`,
  `docs/handoffs/next-session-phase-05.md`, and this file.
- Reviews: `docs/reviews/phase-05-review-luna.md`,
  `docs/reviews/phase-05-review-astra.md`; tracker/status docs are lead-owned.

## Tests

Results below are lead/implementer reports, not documentation-agent runs:
- **After Luna fixes:** lead `go build ./...`, `go vet ./...`,
  `go test -count=1 ./...`, `go test -race -count=1 ./...` PASS;
  formatting clean. Real milestone executed, not skipped.
- **After Astra fixes and final sequential approvals:** lead reran
  `go build ./...`, `go vet ./...`, `go test -count=1 ./...`, and
  `go test -race -count=1 ./...`: all PASS; `gofmt -l .` clean.
- Implementer observed first-round regressions red before fixes, green
  afterward. Empty expected ID with linked plan and both IDs empty also
  failed pre-fix assertions; now all five linkage cases pass.

Final lead diff/whitespace checks pass. A scan of all 98 tracked files found
no matching credential/private-key/home-path patterns or unexpected
generated media/model/environment artifacts; no oversized files found.

Coverage: splitting/keywords/categories/durations, invalid inputs/selection,
cancellation identity/no partial results, hints/ownership/avatar consistency,
metadata and validation. Full normalized `PlanResult` comparison canonicalizes
only generated IDs and timestamps, preserving reference linkage. Seventeen
sensitivity cases detect content/order/metadata/requirement changes. Exact
sorted/deduplicated mixed capabilities, runtime custom flow/framing, empty/
dangling defaults, override precedence, request/result JSON round-trips,
and 24 concurrent callers with a stateless safe interpreter are covered.

## Second milestone and provenance

`TestSecondMilestonePromptToRenderedMP4` exercises prompt → fixture plan
→ sequential timeline → rendered shots → composed MP4 → FFprobe. It checks
three shots, valid ordering, 320×240 output, and duration within ±150 ms.
The companion test verifies avatar rejection at the deterministic renderer.

The **prior-session handoff** reports a manual visual smoke test:
extracted frames showed each sentence as its own text shot and the final
video beginning with shot 1. Temporary artifacts were removed; that visual
inspection was not performed by this documentation agent.

Lead final uncached verbose milestone run and Astra confirmation both
executed and passed (not skipped). Text render checks tools, `drawtext`, and
discovered font; unavailable prerequisites skip. Avatar rejection asserts
the specific unsupported-primitive error without native tools, including
empty PATH. The final full gate passed independently of these focused tests.

## Review 1

Luna — `amazon-bedrock/openai.gpt-5.6-luna`: two HIGH and two MEDIUM
findings fixed; **APPROVE / FINDINGS RESOLVED** on the final post-Astra-fix
code. Luna reran uncached director/project unit/race tests, vet, and staged
diff checks. See `docs/reviews/phase-05-review-luna.md`.

## Review 2

Astra — `amazon-bedrock/us.openai.gpt-6-astra`: reviewed sequentially after
Luna fixes/retests; no BLOCKER/HIGH, one MEDIUM and one LOW. Both fixes and
extra coverage implemented; **APPROVE / ALL FINDINGS RESOLVED** after
Luna's final approval. Uncached unit/race tests, vet, and diff checks pass.
See `docs/reviews/phase-05-review-astra.md`; original verdict REQUEST CHANGES.

## Findings resolved

- Luna: lost interpreter cancellation, blank/whitespace hints, conflicting
  avatar category, and drawtext/font test portability fixed. Additional
  empty-slice backing-array aliasing fixed without changing interfaces.
- Astra: public validator rejects empty expected project ID and requires
  matching plan linkage; full-result/capability assertions strengthened.
  Source fixes and scoped regressions verified; both reviewers closed findings.

## Known limitations

- Sentence/keyword rules are not NLP; ambiguous prompts may classify
  poorly, unpunctuated input becomes one shot, and duration has no cap.
- Non-avatar CODE/TERMINAL categories still render as Background + Text,
  not specialized code-editor/terminal primitives.
- No production interpreter, AI generation, asset fulfillment, or provider
  routing exists here. Avatar plans are intentional but not yet renderable.
- Framing selection is metadata-only, without `AssignSlots` or bounds.
- Concurrent use requires a concurrency-safe caller-supplied interpreter;
  core does not serialize calls or forcibly stop a noncooperative adapter.

## Commit and follow-ups

Base: `65a5441` (completed Phase 04). Single phase commit:
`phase(05): implement director / video planning` (the commit containing
this evidence; resolve its hash from repository history). No automatic
push. Next phase: Phase 06 — Decision Engine, using bounded implementation,
documentation, and independent review agents with lead-owned integration.
