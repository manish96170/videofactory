# Phase 08 — Model Manager / Resource Planner

> **DONE (2026-10-11), recorded in the Phase 08 commit.** Started
> 2026-10-10. Core/worker/model source and scoped tests are present; no active code/
> test writers. Main full gate PASS and Terra independently reran it (PASS).
> Terra → Luna formal approvals complete; no outstanding findings. Final post-review
> lead full uncached test/race/build/vet rerun PASS; formatting/whitespace clean.
> Final staged diff and hygiene checks pass; no outstanding findings.

## Goal and authority

Code-enforced hardware-aware admission; `PHASES.md` §0.2/Phase 08/§14 govern. ADR-021
records delegated policy, not individual approval of defaults. Continue in order, no fresh prompt/all gates retained.
Consequential cost/download/security/licensing/scope/publication choices retain owner approval.

## Baseline and start

- Start: **2026-10-10**, **`dbc5ace`**, `phase(07): implement provider registry and routing`.
- Main reports clean Phase 07 DONE/full gate PASS, Luna6 → Astra approval, no findings.
- Fresh scoped resource/provider/decision baseline PASS is main-reported, not Phase 08 proof.
- Prior evidence: [Phase 07](phase-07.md), [Luna6](../reviews/phase-07-review-luna6.md),
  [Astra](../reviews/phase-07-review-astra.md), and ADR-020.

## Accepted design policy (not as-built claims)

Full policy: ADR-021 and `PHASES.md` Phase 08; [as-built draft](../../ARCHITECTURE.md).
- Preserve existing contracts; supplied decimal GB=10^9 budgets: 16-3-4-1.5=7.5 GB
  workers; explicit CPU/backend facts; MaxParallel=2, MaxHeavy=1, queue=64, heavy=2 GB.
- Atomic RAM/CPU/GPU/slot/heavy/exclusive; ceil/floor micro-units, permanent failures never queue; Heavy explicit OR GPU OR RAM threshold.
- FIFO head blocking/cancel removal/rollback before handoff; no lease expiry, copied state, bounded safe/idempotent Release.
- Metal != CUDA; unified allocations count in RAM. Logical GPU/CPU quotas, not OS caps.
- Trusted revisioned load/resident/workspace/profile envelopes; legacy floor not peak,
  25% reference margin; unknown/unbounded local fail. No invented model RAM formula.
- History 256 exact keys/copied evidence, only raises estimates; full/new keys ErrHistoryFull,
  no eviction/peak loss/prompts/paths/ambient persistence; unknown nil, not zero.
- Model busy refusal/frozen recomputation/full peak until unload; use release ownership only.
  Exact profile/workload reuse with enough held resources, else unload; no nested allocation/eviction.
- Loader callbacks outside locks, identity before/after every return; load=30s,
  unload/cleanup=5s; separate bounded cleanup, stopped proof or quarantine/explicit retry.
- Worker details below. Remote candidates retain Phase 07 gates; PreferRemote is not
  spending authority. No Python embedding/downloads/vendor installs/real inference/cloud calls.

## Implementation present and scoped verification

Lead inspected allocator/config/estimate/history/planner/types; initial scoped unit/race PASS.
History eviction is corrected: reject new keys at capacity, preserve peaks, allow
existing updates. Lead inspected corrected source and targeted regression, then
reran scoped unit/race/vet checks (PASS). Worker stable/lead-inspected/scoped-tested;
model source now complete/scoped-tested. Shared resource/domain APIs stay frozen.
Core: `resource/{config,math,manager,profiles,history,estimate,planner}.go`;
tests: `resource/{manager_test,planning_test}.go`; contracts: `resource/resource.go`,
`domain/{model,model_requirements,resourceplan}.go`. This is not Phase 08 completion.
Original model owner canceled before tests; five production files preserved (formatting
only), no rewrite/production fix required. Replacement smoke + peer test-only tasks completed.
All model production/tests read for this draft; API claims below are source-verified.
Full inventory/ownership is in the handoff, not a phase completion claim.

## Frozen core API reference

Core details: [architecture](../../ARCHITECTURE.md), `resource/{resource,config,math,manager,profiles,history,estimate,planner}.go`.
- Request: RAMBytes/CPU/GPU/Exclusive/Heavy/Backend; Manager `Acquire(ctx context.Context, req Request) (Lease, error)`;
  Lease `ID() string`, `Release() error`; `NewManager(cfg Config) (*BudgetManager, error)`.
- `Config() Config`, `Snapshot() Snapshot`, `Snapshot.AvailableNow(r Request) (bool, error)`;
  exact decimal arithmetic/counter units and reference configuration remain frozen.
- `domain/model_requirements.go`: version-1 revisioned bounds/envelopes/provenance; unknown/untrusted local fail.
- `resource/profiles.go` clone/validation helpers; History Record/Snapshot/Lookup copy exact-key evidence/timing/Version/Hash.
- `EstimateRequest(caps domain.ModelCapabilities, profileID string, workload domain.Workload, cfg Config, history HistorySnapshot, percent int64) (Estimate, error)`.
- `NewPlanner(snapshots SnapshotSource, history HistorySource, cfg PlannerConfig) (*Planner, error)`;
  `Plan(ctx context.Context, req PlanRequest) (PlanResult, error)`: advisory ready/wait/ineligible/
  remote statuses; explicit RemoteOnly Phase 07 routing gates, never spending/reservation authority.

## Source-verified model API and lifecycle

- `model/model.go`: Key{ProviderID, ModelID, Revision}; LoadSpec owns exact Key/Profile/Workload.
  Loader: `Identity() Key`, `Load(context.Context, LoadSpec) error`,
  `Unload(context.Context) (stopped bool, err error)`. Budget embeds resource.Manager +
  `Config() resource.Config`; History `Snapshot() resource.HistorySnapshot` and
  `Record(resource.HistoryKey, resource.Measurement) error`. DefaultConfig=30s/5s/25%/Now clock.
- `model/manager.go`: `NewManager(budget Budget, history History, cfg Config) (*Manager, error)`;
  `Register(ctx context.Context, caps domain.ModelCapabilities, loader Loader) error`;
  `Acquire(ctx context.Context, key Key, profileID string, workload domain.Workload) (*UseHandle, error)`;
  `Snapshot() Snapshot`. Frozen copied admission uses current budget/history, never LLM plans.
- Full peak RAM/CPU/GPU/slot/heavy/exclusive held through use release; busy conflicts refuse.
  Idle exact profile/workload reuse requires sufficient held reservation; history-raised
  need returns ErrNeedsUnload without nested allocation or hidden eviction.
- `model/lifecycle.go`: `Unload(parent context.Context, key Key) error`. Loader/budget/history/
  clock callbacks outside mutex; identity before/after every loader return, including errors.
  Failed/late/canceled Load gets fresh bounded cleanup; uncertainty quarantines full reservation.
  Identity-valid stopped=true may refund despite error, reporting it. Lease Release failure
  retains token/accounting/stopped proof; explicit retry does bookkeeping, not another unload.
- `model/handle.go`: `Key() Key`, `Spec() LoadSpec`, `Estimate() resource.Estimate`,
  `Release() error` only clears use ownership; copied/stale tokens cannot clear the next owner.
  `RecordMeasurement(measurement resource.Measurement) error`, `ObservationError() error`;
  history failure is advisory/countable, valid successful handle retained. Snapshots own
  state/spec/estimate/reservation/StoppedVerified/Observations/HistoryFailures/LastCode, no raw errors.
  States unloaded/reserving/loading/in_use/loaded_idle/unloading/quarantined; safe core errors
  in `model/errors.go`, unwrap causes require caller redaction. No real ML/network/inference.

## Stable worker API and safety boundary

- `worker/runner.go`: `NewRunner(c Config) (*Runner, error)`;
  `Runner.Run(ctx context.Context, spec CommandSpec, request Request, limits Limits) (RunResult, error)`;
  `CleanupHandle.RetryStop(ctx context.Context) (bool, error)` requires a deadline.
  RunResult: Reply/Measurement/Stopped/Cleanup/StderrCapturedBytes/StderrTruncated.
- CommandSpec.Exe absolute; trusted Argv/CWD/Env excluded from JSON, never GenerationParams.
  Nil Env is empty, not ambient. Config defaults NativeMonitor/parent Clock/5s cleanup;
  typed-nil monitor pointer/function rejected (constructor red/green + lead rerun PASS).
- `worker/protocol.go`: version 1 generate-only; header handles 1–128 restricted bytes;
  raw stdin/stdout bounded 1 MiB, stderr 8 KiB capture + discard/drain, raw stderr not returned.
  Wire bytes preflight before allocation; domain GenerationRequest/Result typed codecs.
  Exact successful header/source/replay and result RequestID correlations; errors' payloads opaque.
- Exact bounded schema rejects aliases/duplicates/escaped equivalents; extension maps allow case-distinct keys. Safe core codes.
- `worker/monitor.go`, `worker/platform_unix.go`: bounded contextual native ps/group RSS,
  Setpgid, one Wait/direct-child reap, family kill and task joins on failure/cancel/normal exit.
  No daemonizing/escape; native sampling errors fail closed. Unknown stop retains Cleanup
  and caller lease for explicit retry; worker never acquires/refunds resources.
- RetryStop true may return sticky ErrLimitExceeded: stopped proof != successful run.
  Cleanup samples share peak/breach accounting, suppress Reply on breach, retain partial RSS;
  active monitor errors after leader reap retained; intentionally canceled samples ignored.
- `worker/peak{,_darwin,_linux,_other}.go`: family RSS vs OS process peak labelled separately;
  milliseconds source parent clock. Missing metrics nil, not zero; sampling not kernel hard wall.
Lead owns tracker/reviews/integration/commit; implementation is present, phase gate not complete.

## Verification and sequential reviews

No code tests run by this documentation owner; PASS below is reported evidence:
- Main: Phase 07 full gate/scoped baseline; core post-history targeted/scoped normal/race/vet PASS.
- Main: worker production/config/protocol/monitor/platform inspection; fresh worker/resource/domain
  normal, worker race/vet PASS; typed-nil and audit-fix lead reruns PASS.
- Main: Linux/arm64, Windows/amd64 cross-build PASS. Worker owner: Darwin/Linux arm64
  test-compilation PASS. macOS native-helper/pipe cleanup unit/race PASS; Linux runtime
  **unverified**. Windows Run unsupported; build success is not runtime support.
- Model: three bounded smoke cases and independent lead normal/race/vet PASS; peer test-only
  lifecycle and ownership suites each report targeted normal/race/vet PASS, no production fix.
Main whole gate **PASS**: `go test -count=1 -timeout=120s ./...`,
`go test -race -count=1 -timeout=180s ./...`, `go build ./...`, `go vet ./...`.
These are actual whole-repository runs, also independently rerun by Terra (PASS).
Final post-review lead full uncached unit/integration/race/build/vet rerun PASS;
formatting/whitespace and staged diff clean. A scan of all 157 tracked files
found no matching credential/private-key/home-path patterns, oversized files,
or unexpected generated media/model/environment artifacts.

### Model test references (source read; execution attributed above)

`model/manager_test.go`: TestManagerRetainsReservationUntilExplicitUnload, TestManagerQuarantinesPartialLoadUntilStopVerified, TestManagerBusyAndStaleHandleRelease.
`model/lifecycle_test.go`: TestFailureCallerCancellation, TestFailureLoadTimeoutCleanup,
TestFailureLoaderIdentityDrift, TestFailureLeaseReleaseRetry, TestFailureFinalCallbackCancellation.
`model/ownership_test.go`: TestOwnershipFrozenMetadataAndAdmission, TestOwnershipHistoryRaisesIdleReservation,
TestOwnershipMeasurementAndHistoryFailures, TestOwnershipConcurrentLifecycleCallbacksOutsideLocks,
TestOwnershipConcurrentRepeatedAndStaleRelease. Five Failure + five Ownership complete; source inspection is not a new test/formal approval.

### Supplementary Astra worker audit — not formal Review 1/2

Started 2026-10-10; component APPROVE 2026-10-11, all 3 MEDIUM + 1 LOW **CLOSED**.
- M1: alias/duplicate DTO/raw-wire identity mismatch fixed by exact bounded schema scan.
- M2: cleanup-only 100-byte → 3 GB sample with 2 GB cap retains 3 GB partial peak,
  ErrLimitExceeded, Stopped=true, Reply=nil after stop proof; breach cannot disappear.
- M3: active monitor failure after leader reap retained; intentional cancellation ignored.
- L1: independent child-stop whole-ps assertion requires query success/unique valid PIDs;
  timeout/start/query failures are not absence. Owner red/green for all four; lead retest PASS.
Astra reran original five overlay regressions/race/native helpers/scoped normal/race/vet PASS;
no new worker issue. Main-owned evidence: `docs/reviews/phase-08-worker-audit-astra.md`.

### Formal whole-phase sequential reviews

1. Terra — `amazon-bedrock/openai.gpt-5.6-terra`: **APPROVE**; no code defects,
   one LOW stale-gate documentation finding corrected. Evidence:
   `docs/reviews/phase-08-review-terra.md`.
2. Luna — `amazon-bedrock/openai.gpt-5.6-luna`: **APPROVE** after Terra/correction;
   no BLOCKER/HIGH/MEDIUM or outstanding findings. Independent tests/race/vet,
   Linux+Windows builds/diff checks PASS. Evidence: `docs/reviews/phase-08-review-luna.md`.

Both approvals are sequential on 2026-10-11; roster/catalog verification is lead-reported.

## Final checks and local commit

Implementation/tests/reviews and final post-review lead gate/hygiene pass (§14).
Single commit: `phase(08): implement resource and model management` (the commit
containing this evidence; resolve its hash from repository history).
Then Phase 09 automatically under §0.2, no automatic push or routine fresh prompt.
Resume: [handoff](../handoffs/next-session-phase-08.md), same session or rollover; doc owner does not stage/commit/spawn.
