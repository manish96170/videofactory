# Phase 00 Review — Reviewer 2 (astra / GPT-6 Astra)

**Phase:** 00 — Foundation / Contracts
**Reviewer:** astra
**Model:** `amazon-bedrock/us.openai.gpt-6-astra`
**Date:** 2026-10-09
**Commit reviewed:** pre-commit (staged working tree, first Phase 00 pass)
**Scope:** `go.mod`, `domain/*.go`, `event/*.go`, `provider/*.go`,
`decision/*.go`, `resource/*.go`, `cmd/videofactory/main.go`,
`PHASES.md`, `AGENTS.md`, `ARCHITECTURE.md`, `DECISIONS.md`.

## Verification performed
`go test ./...`, `go test -race ./...`, `go vet ./...`, `go build ./...`,
`go run ./cmd/videofactory` all passed. Verified on Go 1.27.1,
darwin/arm64.

## Findings

### BLOCKER / HIGH
None reported by this reviewer.

### MEDIUM
1. **M1 — Threshold evaluation silently accepts contradictory policies**
   (`decision/decision.go`).
   **Resolution:** Added `Policy.Validate() error`; documented that
   `Evaluate` itself remains unvalidated/pure by design; added tests for
   inverted, zero-value, out-of-range, and non-finite thresholds.
2. **M2 — Extension payloads (`map[string]any`) lack a defined, lossless
   JSON contract.**
   **Resolution:** Documented the accepted `float64`-decoding tradeoff in
   `domain/doc.go` and `DECISIONS.md` ADR-008; strengthened
   `TestChangeSetJSONRoundTrip` to assert the decoded payload value and
   type explicitly instead of only checking key presence.
3. **M3 — Visual timing and coordinate systems were unspecified**
   (shot-relative vs. project-relative, bounds coordinate space,
   interval inclusivity).
   **Resolution:** Documented the convention in `DECISIONS.md` ADR-007
   and on `domain/timeline.go`/`domain/visual.go`/`domain/review.go`;
   added `TestShotRelativeAndProjectRelativeTimingCompose` in
   `domain/domain_more_test.go` demonstrating composition with a
   nonzero project offset, as requested.

### LOW
1. **L1 — `ARCHITECTURE.md`/`DECISIONS.md` wording implied capability
   tags had a typed-constant vocabulary like `ShotCategory`.**
   **Resolution:** Reworded `ARCHITECTURE.md` to distinguish the two
   patterns explicitly (typed enums with constants vs. open `[]string`
   tags with none).
2. **L2 — Progress/evidence claims in docs were ahead of the actual
   (uncommitted) repository state.**
   **Resolution:** Same fix as reviewer 1's HIGH finding — reworded
   `ARCHITECTURE.md` to describe in-progress status accurately.
3. **L3 — Resource release lifecycle guarantees (idempotency, concurrent
   release, nil-lease guarantee) were underspecified.**
   **Resolution:** Documented on `resource/resource.go` that a
   successful `Acquire` returns a non-nil `Lease`/nil error (and vice
   versa), that `Release` should be safe to call once with concrete
   idempotency guarantees deferred to the Phase 08 implementation, and
   why `Release` deliberately takes no `context.Context`.

### Missing tests / evidence limits (addressed)
- Added full round-trip coverage for remaining contract types
  (`domain/domain_more_test.go`).
- Strengthened the `ChangeSet` payload assertion.
- Rewrote the resource test double so release/budget tests exercise real
  lifecycle behavior rather than only interface shape.
- Clarified in comments that fakes prove interface ergonomics only, not
  production enforcement — consistent with Phase 00 scope.

## Positive observations (unchanged by fixes)
- Core domain fidelity to PHASES.md section 5.
- Small interfaces, explicit dependencies, no god provider.
- Provider/model independence preserved.
- Flow/Framing kept separate, not hard-coded.
- Cancellation boundaries present on all potentially blocking interfaces.
- No goroutines, caches, or heavy allocations in production code.
- Lambda/control-plane compatible: no daemon, no mandatory filesystem
  state, no global runtime service.
- No secrets, absolute paths, or `unsafe` code.
- Scope discipline: no Phase 00 non-goal was implemented.

## Final status
All MEDIUM/LOW findings resolved. No BLOCKER/HIGH findings from this
reviewer. Tests rerun and passing after fixes (see
`docs/phases/phase-00.md`).
