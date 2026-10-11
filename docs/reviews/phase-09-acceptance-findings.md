# Phase 09 Acceptance-Test Findings — Checkpoint

**Phase:** 09 — Validation / Shot Intelligence
**Date:** 2026-10-11
**Base commit:** `61e078b` (completed Phase 08); current unstaged Phase 09 work.
**Origin:** bounded test-engineering agent, then independent lead reproduction.
**Scope:** `intelligence/acceptance_test.go` input/evidence ownership assertions.
**Not a formal phase review:** Astra → Terra reviews have NOT STARTED.

## Unresolved findings

### HIGH — P09-A1: request snapshot reuses caller-owned state

`intelligence/intelligence.go`, `Analyzer.Analyze`, currently calls
`snapshot(req, &req)` (line 141 at this checkpoint). The shallow Request copy
still points to caller-owned Project/Plan/maps/slices. JSON decoding into it
reuses nested state: an original integer primitive property becomes float64,
and planner callback mutation reaches the original model binding.

**Minimal next fix:** decode into a fresh zero-valued `Request`, then assign
that independent snapshot to the local request. Preserve the existing JSON
snapshot semantics, size checks, and caller nonmutation contract.

### MEDIUM — P09-A2: router callback input aliases retained policy

`intelligence/helpers.go`, `Analyzer.route`, currently calls
`a.router.Route(child, policy)` (line 216). The callback receives slices and
pointers also used for downstream trusted binding admission. Mutating a required
tag changes the retained policy and skips valid resource planning.

**Minimal next fix:** pass `policy.Clone()` to the router callback; keep the
trusted policy for subsequent checks. Preserve all constraints and gates.

## Regression evidence

The test-engineering agent added only `intelligence/acceptance_test.go` with
five grouped functions. Normal/race acceptance selections fail only the
`independent-inputs-and-return-evidence` subtest; no race detector diagnostics
were reported. Production source remains unchanged by that test task.

Lead independently ran:

```sh
go test -count=1 -timeout=30s ./intelligence \
  -run '^TestAcceptanceCancellationAndOwnership/independent-inputs-and-return-evidence$'
```

**FAIL**, confirming caller primitive-type mutation, lost downstream resource
admission, original binding mutation, and router/planner calls `2/1` rather than
the expected `2/2`. Assertions are around lines 847, 850, 864, and 871.

The existing four focused checkpoint tests passed before this new acceptance
coverage. That earlier PASS is not a current intelligence/full-phase PASS.
Lead also ran the same focused subtest under `-race` with a 60s timeout:
**FAIL with the same assertions and no race-detector diagnostic**. This is an
ownership/aliasing failure, not a claimed detected data race. `go build ./...`
and `go vet ./...` independently PASS at checkpoint. The full current Phase 09
test/race gate was not rerun after acceptance addition and has not passed.

## Disposition

**Historical checkpoint — superseded on 2026-10-11.** The bounded fix owner retained
these assertions, first reproduced them RED, then corrected A1 with a fresh Request
snapshot and A2 with a router-policy clone. The lead independently verified the
focused normal/race regression, scoped intelligence normal/race/vet, and full
uncached repository test/race/vet/build gates (PASS). Astra formal R1 subsequently
identified four additional MEDIUM boundary issues; those were fixed with regressions
and Astra approved the updated state. This historical record remains distinct from
the formal review evidence. Terra review and phase closeout are still required.
