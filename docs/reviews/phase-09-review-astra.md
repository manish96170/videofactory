# Phase 09 — Formal Review: Astra

## Review record

- **Phase:** 09 — Validation / Shot Intelligence
- **Reviewer:** Astra
- **Model:** `amazon-bedrock/us.openai.gpt-6-astra`
- **Date:** 2026-10-11
- **Commit reviewed:** dirty Phase 09 work above `61e078b`
- **Scope:** Phase 09 contracts, validation/native boundaries, intelligence callback
  ownership, provider/resource advice, tests, and current documentation.

## Findings

No BLOCKER or HIGH findings. Astra confirmed P09-A1/A2 were fixed and reported:

| ID / severity | Files | Finding | Resolution |
| --- | --- | --- | --- |
| P09-R1-M1 / MEDIUM | `intelligence/helpers.go:240-248,280-287,319-322` | Planner input retained a requirements pointer used as the provenance baseline. | `clonePlanRequest` deep-copies capabilities for the callback while `matchingPlan` reads the frozen binding. |
| P09-R1-M2 / MEDIUM | `validation/native.go:177-182`; `validation/process.go:31-36` | Child deadline handling could discard permanent media/limit evidence. | Deadline joins preserve permanent markers; parent cancellation remains authoritative. |
| P09-R1-M3 / MEDIUM | `intelligence/helpers.go:357-374` | Binary float rational expansion could add a frame at exact decimal FPS boundaries. | FPS now uses `FormatFloat` plus decimal rational conversion before ceiling. |
| P09-R1-M4 / MEDIUM | `intelligence/helpers.go:222-239` | Router output was not restricted to coherent structure/known diagnostic codes. | Results are copied and validated before output/resource advice. |

LOW P09-R1-L1 (stale checkpoint wording) and P09-R1-L2 (router test-oracle
isolation) were also accepted and resolved before Terra review.

## Resolution and tests rerun

Regressions at `intelligence/intelligence_test.go:258-358` cover planner mutation,
decimal/exact frame counts, overflow, malformed route results, duplicate eligible IDs
with an otherwise-correct fallback tail, and positive IDs with `ErrNoEligibleProviders`.
`validation/native_test.go:228-290` and `validation/validation_test.go:187-200` cover
native permanent-marker precedence. The original A1/A2 acceptance oracle remains.

The lead independently passed scoped intelligence/validation normal/race/vet and full
uncached `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...`.
Astra independently passed scoped normal/race/vet checks during its initial review and
confirmation re-review.

## Final status

**APPROVE.** Astra re-reviewed the fixes. No BLOCKER, HIGH, or MEDIUM findings remain.
