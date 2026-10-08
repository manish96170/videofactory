# Phase 00 Review — Reviewer 1 (terra / GPT-5.6 Terra)

**Phase:** 00 — Foundation / Contracts
**Reviewer:** terra
**Model:** `amazon-bedrock/openai.gpt-5.6-terra`
**Date:** 2026-10-09
**Commit reviewed:** pre-commit (staged working tree, first Phase 00 pass)
**Scope:** `go.mod`, `domain/*.go`, `event/*.go`, `provider/*.go`,
`decision/*.go`, `resource/*.go`, `cmd/videofactory/main.go`,
`PHASES.md`, `AGENTS.md`, `ARCHITECTURE.md`, `DECISIONS.md`.

## Findings

### BLOCKER
None.

### HIGH
1. `ARCHITECTURE.md` claimed "`All pass as of the Phase 00 commit`" and
   pointed to `docs/phases/phase-00.md` / `docs/reviews/` while no commit
   or evidence files existed yet.
   **Resolution:** Reworded `ARCHITECTURE.md` to describe in-progress
   status accurately and only reference evidence once it exists (see this
   file and `docs/phases/phase-00.md`).

### MEDIUM
1. `time.Duration` fields serialize as nanoseconds with no documented
   convention, inconsistent with `GenerationResult.DurationMs`.
   **Resolution:** Documented the convention in `DECISIONS.md` ADR-007
   and in doc comments on `domain/timeline.go`, `domain/visual.go`,
   `domain/review.go`; added `ARCHITECTURE.md` section.
2. `decision.Policy` silently accepted inverted/invalid thresholds.
   **Resolution:** Added `Policy.Validate() error` plus tests for
   inverted, out-of-range, non-finite, and zero-value policies
   (`decision/decision_test.go`); documented in `DECISIONS.md` ADR-006
   amendment.

### LOW
1. `fakeSubscriber` test double implied full cancellation/close semantics
   it didn't implement.
   **Resolution:** Clarified the doc comment in `event/event_test.go` to
   state explicitly that it is a compile-time shape check only.
2. `fakeLease.Release` didn't restore `fakeManager`'s budget, so the
   tests didn't prove the acquire→release→acquire lifecycle.
   **Resolution:** Rewrote `resource/resource_test.go` so `Release`
   restores capacity, is idempotent, and is covered by
   `TestFakeManagerReleaseRestoresCapacity` /
   `TestFakeLeaseReleaseIsSafeToCallTwice`.

### Architecture concern
`GenerationRequest.ProviderID` could be read as inviting callers to
bypass the future Provider Router.
**Resolution:** Documented on `domain/generation.go` that `ProviderID` is
expected to come from the Phase 07 routing decision, not chosen ad hoc.

### Missing tests (addressed)
Added JSON round-trip tests for `Asset`, `Flow`, `Framing`, `Timeline`,
`RenderManifest`, `ReviewFeedback`, `GenerationRequest`,
`GenerationResult`, `ValidationResult`, `ResourcePlan`,
`HardwareRequirements`, `ModelCapabilities` in
`domain/domain_more_test.go`.

## Positive observations (unchanged by fixes)
- Core domain vocabulary matches PHASES.md section 5.
- Small, context-aware interfaces per package; no god interfaces.
- Provider/model independence preserved via capability tags.
- No hidden daemon/global state; Lambda-compatible shape.
- Explicit non-goals respected (no AI/cloud providers, TUI, inference,
  heavy rendering, Python embedding, scheduler, cloud infra).
- `go build/vet/test/test -race` all pass.

## Final status
All HIGH/MEDIUM/LOW findings resolved. No BLOCKERs raised. Tests rerun
and passing after fixes (see `docs/phases/phase-00.md`).
