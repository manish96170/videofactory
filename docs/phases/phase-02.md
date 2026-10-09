# Phase 02 — Event System

## Goal

Create the event backbone for observable execution (PHASES.md
"PHASE 02 — EVENT SYSTEM").

## Implemented

- **`event.Event` extended** with `Seq uint64` — a bus-assigned,
  strictly increasing sequence number used as the authoritative
  delivery order (independent of wall-clock `OccurredAt`).
- **`event.Bus`** — a mutex-guarded, in-process `Publisher`+`Subscriber`
  implementation:
  - **Ordering:** `Seq` assigned and dispatched under one mutex;
    re-checks `ctx.Err()`/closed-state after acquiring the mutex (not
    just at entry).
  - **Backpressure:** non-blocking per-subscriber dispatch, drop-newest
    on a full channel, `TotalDropped()` diagnostic; `Publish` never
    blocks on a slow consumer.
  - **Cancellation:** each subscription's channel closes on its own
    `ctx.Done()` or on `Bus.Close()` (via a shared `closedCh`), with no
    leaked watcher goroutines even for `context.Background()`
    subscriptions.
  - **Event metadata:** auto-stamps `ID`/`OccurredAt` when the caller
    leaves them zero; respects explicit caller-supplied values.
  - **Payload ownership:** documented contract (shared by reference,
    treat as immutable after `Publish`), not enforced by copying.
  - `WithBufferSize`, `TotalDropped`, `Close` — small, justified
    additions to the `Publisher`/`Subscriber` interfaces.

## Design decisions

See `DECISIONS.md` ADR-013/ADR-014:
- ADR-013: ordering/backpressure/cancellation design and the
  alternatives rejected (blocking publish, ctx-only watchers).
- ADR-014: review-round fixes — cancellation re-check after lock
  acquisition, payload-ownership documentation, and several real
  test-suite correctness bugs (see below).

## Files changed

New: `event/bus.go`, `event/bus_test.go`,
`docs/reviews/phase-02-review-astra.md`,
`docs/reviews/phase-02-review-gpt-oss-120b.md`. Modified:
`event/event.go`, `ARCHITECTURE.md`, `DECISIONS.md`, `PHASES.md`,
`ROADMAP.md`.

## Tests

```text
go build ./...                   PASS
go vet ./...                      PASS
go test ./...                     PASS (all packages)
go test -race ./...               PASS (all packages)
go test -race -count=20 ./event/... PASS (stress run)
gofmt -l .                         (clean, no output)
```

Coverage: basic publish/subscribe, ID/OccurredAt auto-stamping vs.
caller-supplied preservation, empty-Type rejection, cancellation (at
entry and after lock acquisition), type filtering, exact-order
delivery (single and concurrent publishers with a start barrier for
real contention), concurrent subscribers (readiness-barrier
synchronized, errors reported via channel — not `t.Fatalf` from
goroutines), backpressure/drop-newest without blocking, `Close`
semantics (closes subscriptions, rejects further use, idempotent),
per-subscription cancellation, and a goroutine-count-based leak test
for `context.Background()` subscriptions (verified to actually catch a
real leak by deliberately reintroducing one and watching it fail,
documented in ADR-014).

## Review 1

Reviewer: astra (`amazon-bedrock/us.openai.gpt-6-astra`). Two rounds —
first review and a leak-test-tolerance follow-up. See
`docs/reviews/phase-02-review-astra.md`. Final verdict: **READY FOR
PHASE COMMIT**.

## Review 2

Reviewer: gpt-oss-120b (`amazon-bedrock/openai.gpt-oss-120b-1:0`). Two
rounds — an initial pass that found no issues, and a targeted
re-examination of astra's `t.Fatalf`-from-goroutine and
ordering-test findings, which confirmed both were real and resolved.
See `docs/reviews/phase-02-review-gpt-oss-120b.md`. Final verdict:
**READY FOR PHASE COMMIT**.

## Findings resolved

Every finding from both reviewers resolved — including a genuine bug
in the test suite itself (calling `t.Fatalf` from goroutines other
than the test's own, which the Go `testing` package explicitly
disallows) and a leak-detection test whose first-draft tolerance was
loose enough to mask real leaks. Both were caught by the human-in-
effect-but-actually-the-reviewer-model process working as intended:
an independent reviewer found what the implementer missed.

## Known limitations

- `Bus` is in-process only: no persistence, replay, or cross-process
  transport. A future Lambda/control-plane event transport would need
  to implement the same `Publisher`/`Subscriber` interfaces separately.
- `TotalDropped()` is a bus-wide counter, not per-subscription; a
  caller cannot tell *which* subscription(s) experienced drops from
  this API alone.
- Backpressure drops data (observability only); core project/timeline
  state is unaffected, by design — consumers must not treat the event
  stream as a reliable notification channel for anything authoritative.

## Commit

See the phase commit immediately following this evidence file in the
repository history (`phase(02): implement event system`).

## Follow-ups

- None blocking. Phase 08 (Model Manager / Resource Planner) and other
  future phases that publish real `MODEL_LOAD_STARTED`/
  `GENERATION_*`/etc. events should keep the payload-immutability
  contract (ADR-014) in mind.
