# Phase 06 — Decision Engine

> **DONE (2026-10-09), recorded in the Phase 06 commit.** All fixes are complete;
> lead-inspected full uncached build/vet/test/race gate PASS after the latest fix.
> Terra final APPROVE then Luna6 final APPROVE sequentially; no outstanding
> findings. Final lead post-confirmation full gate and ten repeated decision
> runs PASS; formatting, final staged diff, and hygiene checks pass.

## Goal

Structured, cheap, pluggable YES/NO/UNCERTAIN classification, with deterministic
core policy as authority (`PHASES.md` Phase 06). No real model/provider or
Director integration, Phase 07 router, or new domain schema fields.

## Implemented

- `NewEngine(Resolver, Config)` validates explicit configuration and copies the
  provider chain. `DefaultConfig(providers ...string)` supplies recommendations;
  no implicit zero-value defaulting. `AuditTimeout` must be positive even disabled.
- `Engine.Decide(ctx, domain.DecisionRequest)` validates nonblank ID/kind and
  snapshots JSON Input once, decoding an independent copy for every attempt.
  Existing domain fields/signatures and `decision.Provider` remain unchanged.
- `Resolver.Resolve(ctx, id) (Provider, error)` is context-aware, not bool lookup.
  Zero-value-usable `Registry.Register(ctx, CapabilityAdapter) error` admits both
  existing decision/provider interfaces with stable matching capability IDs and
  `semantic-decision`; rejects duplicates and nil/typed-nil adapters. Adapter calls
  are outside map locks; references/lifecycles remain caller-owned.
- Resolution and provider execution are synchronous under one child timeout per
  attempt. Core validates result identity, finite score/known verdict, recomputes
  classification, and retains arbitrary non-authoritative ReasonCode metadata.
  Post-call adapter identity is checked even on error; mismatches beat transient
  retry, retain causes, and respect caller/child deadline precedence.
- Bounded retries/fallback preserve valid uncertainty; `Error.Outcome()` and safe
  strings use core codes. `Unwrap() []error` copies the cause list for `errors.Is/As`;
  raw causes need caller redaction, not logging. Core emits no raw kind/error text.
- `NewFixtureProvider(id, results)` copies an immutable per-kind table, ignores
  Input, returns `ErrUnsupportedKind` for unknown kinds, and declares local
  `semantic-decision` capabilities. No model/network/process calls.
- Optional nil/typed-nil-disabled audit publishes four decision event types with
  fresh scalar-only payload maps. Atomic `AuditFailures()` counts failed/timed-out
  publications, not subscriber drops; caller owns publisher/bus lifecycle.
  FAILED uses the last attempted configured handle; COMPLETED uses the valid
  result's provider, including saved uncertainty. Final context check after
  terminal publication clears results on cancellation without a second terminal.

## Design decisions

See `DECISIONS.md` **ADR-019** and the owner-approved Phase 06 policy in
`PHASES.md`; as-built details are in `ARCHITECTURE.md` Phase 06. Owner-approved
defaults and policy are unchanged by bounded self-review/review hardening.

- Configurable defaults: YES `>= 0.85`, NO `<= 0.30`; attempt timeout 5s;
  2 attempts/provider; fixed retry delay 100ms; uncertainty escalation enabled;
  audit timeout 200ms. Fixed hard caps: 8 distinct providers, 3 attempts/provider.
- Preserve zero/equal policy and YES-first ties; retry transient/child timeout
  only, not uncertainty. Saved UNCERTAIN survives later failures/abstention;
  all-failed execution errors. Caller cancellation must win, including terminal audit.
  Zero Policy is valid (all valid scores yield YES), not implicitly defaulted.
- ADR-008 input ownership: no caller mutation while snapshotting or adapter
  retention/asynchronous mutation after return; concurrent dependencies must be safe.
- Audit omits input/raw kind/reason/error text; nonsecret handles only, owned
  scalar maps, no durability promise. Publication success does not imply delivery.

## Files changed

- New production: `decision/engine.go`, `decision/registry.go`, `decision/errors.go`,
  `decision/fixture.go`, `decision/audit.go`.
- New tests: `decision/engine_test.go`, `decision/registry_test.go`,
  `decision/audit_test.go`; event vocabulary: `event/event.go`.
- Comment-only clarifications: `decision/decision.go`, `domain/decision.go`,
  `director/interpreter.go` (no Director integration/signature/schema changes).
- Docs: appended Phase 06 in `ARCHITECTURE.md`, updated only ADR-019 in
   `DECISIONS.md`, and this evidence. Lead reconciled `PHASES.md`, `ROADMAP.md`,
   `README.md`, and both review records.

## Tests

Results are lead/reviewer/implementer reports, not documentation-agent test runs.
Baseline `ed20587` and initial implementation passed. Lead inspected exact latest
fix diffs and reran the full uncached code gate after those fixes: all PASS.

| Command | Baseline | Lead after latest fix |
| --- | --- | --- |
| `go test -count=1 ./...` | PASS | PASS |
| `go test -race -count=1 ./...` | PASS | PASS |
| `go build ./...` | PASS | PASS |
| `go vet ./...` | PASS | PASS |

Source-inspected coverage: threshold authority/zero/equal policy; config validation/
ownership/hard caps; malformed/provenance handling; retry/fallback order and exact
24-attempt budget; uncertainty preservation; parent/child cancellation/late success/
retry-delay boundaries; safe multi-cause errors; nested JSON retry/fallback isolation;
concurrent engine/registry/counter behavior; fixture table/capability ownership;
audit redaction/order/immutable maps, bounded cleanup, rejected requests, and
closed/full bus publication versus delivery. Review regressions are detailed below.

Terra final scoped normal/race/vet: PASS. Luna6 final independently reran the full
uncached commands above and staged diff check: PASS, after Terra's final approval.
Final lead post-confirmation full uncached build/vet/test/race gate also PASS;
`go test -count=10 ./decision` PASS and formatting clean. Documentation is
lead-inspected; final staged diff/whitespace checks pass. A scan of all 109
tracked files found no matching credential/private-key/home-path patterns,
unexpected generated media/model/environment artifacts, or oversized files.

## Review 1

Terra (`terra`) — `amazon-bedrock/openai.gpt-5.6-terra`: **FINAL APPROVE**.
R1-01 explicitly withdrawn; R1-02 fixed. Final scoped normal/race/vet PASS;
approval on latest fixed code preceded Luna6's final approval.
Evidence: `docs/reviews/phase-06-review-terra.md` (lead-owned).

## Review 2

Luna6 (`luna6`) — `amazon-bedrock/us.openai.gpt-6-luna`: **FINAL APPROVE**.
L2-01 fixed, L2-02 covered; final full uncached build/vet/test/race and staged
diff check PASS. Final confirmation followed Terra sequentially.
Evidence: `docs/reviews/phase-06-review-luna6.md` (lead-owned).

Owner-approved pair, lead-verified roster/catalog; implementation uses the main
default model, different from both reviewers. No other name/model mapping assumed.

## Findings resolved

- **Lead self-review:** `TestCancellationDuringTerminalAuditWins` failed all three
  certain/saved-uncertain/exhausted cases before the fix, then passed. Final ctx
  check returns zero result/caller cancellation, preserving prior causes through
  `errors.Is/As`. One terminal snapshot at publication start, non-atomic with
  cancellation; no second terminal event or mutation of the published record.
- **Terra R1-01 — WITHDRAWN:** missing terminal after STARTED cancellation claim
  was disproven before production edits by immediate and cooperative cancellation
  regressions. STARTED → FAILED, no attempts/calls, empty handle/ordinal 0, bounded
  independent cleanup. `TestStartedAuditCancellationStillPublishesTerminal` remains;
  the defer did NOT move. This is not a claimed production fix.
- **Terra R1-02 — FIXED:** four RED cases (exhausted, last resolver failed,
  in-attempt cancellation, provenance) became GREEN. `lastProvider` is set from
  the configured handle when ordinal increments, before resolution; FAILED uses
  last attempted handle (empty only with no attempt). COMPLETED retains valid
  result/saved-uncertainty source. No new raw secret metadata.
- **Luna6 L2-01 — MEDIUM FIXED:** unconditional post-call ID check before error
  return preserves mismatch/original causes, beats ErrTransient, and leaves caller/
  child deadline precedence intact. Two MaxAttempts=3 cases were RED (three calls);
  now GREEN (one rejected call, direct fallback where configured).
- **Luna6 L2-02 — LOW COVERED:** late cooperative resolver success after child
  expiry never invokes its provider; caps 1/2 assert timeout actions/ordinals and
  fallback. Already GREEN before production change: coverage-only, not a fixed bug.
- Added ignored-error-accompanying-result/context-precedence tests are GREEN.
  Both reviewers closed findings; none remain outstanding.

## Known limitations

Cooperative timeouts cannot forcibly stop noncooperative adapters/publishers.
Audit is not durable/compliance storage; publication and parent cancellation are
not atomic. No real inference, pipeline wiring, router, or schema expansion.
Terminal snapshots reflect publication-start state, not an atomic final-result log.

## Commit and follow-ups

Base: `ed20587`. Single commit: **`phase(06): implement decision engine`**
(the commit containing this evidence; resolve its hash from repository history).
The main session orchestrated bounded implementation/documentation/review work
and independently verified integration, final tests, diffs, and hygiene (§14).
No automatic push; this documentation agent does not stage/commit/push.
Next: **Phase 07 — Provider System / Router, NOT STARTED**; no work was advanced.
