# Phase 02 Review — Reviewer 1 (astra / GPT-6 Astra)

**Phase:** 02 — Event System
**Reviewer:** astra
**Model:** `amazon-bedrock/us.openai.gpt-6-astra`
**Date:** 2026-10-09
**Commit reviewed:** pre-commit (staged working tree, Phase 02 implementation)
**Scope:** `event/event.go` (added `Seq`), `event/bus.go` (new),
`event/bus_test.go` (new), plus `ARCHITECTURE.md`/`DECISIONS.md`
Phase 02 sections.

## Round 1 findings and resolutions

### BLOCKER / HIGH
None. Verdict: "the bus’s internal synchronization and drop-newest
implementation are sound."

### MEDIUM
1. **M1 — cancellation ignored while waiting for the mutex.** A caller
   whose `ctx` was cancelled while blocked on `b.mu.Lock()` could still
   get an event dispatched / a subscription registered.
   **Resolution:** `Publish`/`Subscribe` re-check `ctx.Err()` (and
   `Bus.closed`) immediately after acquiring the mutex.
2. **M2 — mutable payloads shared without an ownership contract.**
   **Resolution:** documented explicitly on `Event` and `Bus`: `Payload`
   is shared by reference, must be treated as immutable after `Publish`.
3. **M3 — concurrent-ordering test verified uniqueness, not order.**
   `TestBusConcurrentPublishersPreserveTotalOrder` only rejected
   duplicate `Seq` values; it never checked that delivery order matched
   publish order, so a reordering bug would have passed.
   **Resolution:** both ordering tests now assert the exact `Seq`
   sequence `1..n`; the concurrent test uses a start barrier
   (`close(start)`) for real contention.
4. **M4 — watcher-leak test never observed watcher termination.**
   Observing only that a channel closed doesn't prove the watcher
   goroutine exited, since `Close` closes channels directly regardless.
   **Resolution:** rewrote the test to count live goroutines
   (`runtime.NumGoroutine()`) before/after `Close`.
   **Round 2 follow-up:** the first fix's tolerance (`before + n/2`)
   would still pass with up to half of `n` goroutines leaked. Tightened
   to a fixed `leakTolerance = 10`. Verified by deliberately
   reintroducing a leak (removing the `closedCh` watch arm): the test
   failed with "before=2, after=202 (leaked watcher goroutines)" as
   expected, then passed again once reverted.
5. **M5 — concurrent subscriber test relied on scheduling and called
   `t.Fatalf` from a non-test goroutine.** This is an actual violation
   of the `testing` package's contract (`Fatal`/`FailNow` must only be
   called from the goroutine running the test).
   **Resolution:** split `drain` (returns `error`, safe from any
   goroutine) from `mustDrain` (calls `t.Fatalf`, used only in the main
   test goroutine); replaced the fixed sleep with an explicit
   "subscriber ready" channel barrier.

### LOW
1. **L1 — backpressure test could hang on cleanup** if `Publish` ever
   regressed to blocking while holding the mutex, since the deferred
   `Close()` would then also block. **Resolution:** removed the
   `defer`, documented why.
2. **L2 — doc clarifications and a wrong section citation** ("no
   uncontrolled goroutines" is PHASES.md section 4.10, not 4.9).
   **Resolution:** fixed the citation; added zero-value/async-
   cancellation/non-joining-Close/error-precedence clarifications to
   `Bus`'s doc comment.

## Final status (after two confirmation rounds)

**READY FOR PHASE COMMIT.** All findings resolved, including the
tightened leak-detection tolerance, verified by deliberately
reintroducing and then fixing the exact leak the test is meant to
catch. `go build/vet/test/test -race` (including `-race -count=20` on
`event/...`) all pass.
