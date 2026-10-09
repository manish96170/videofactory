# Phase 07 — Provider System / Router

> **DONE (2026-10-09), recorded in the Phase 07 commit.** All fixes are lead-inspected;
> full uncached build/vet/unit/race after the latest fix PASS; gofmt clean.
> Luna6 final APPROVE then Astra final APPROVE sequentially; no outstanding
> findings. Post-confirmation full gate and ten provider/decision runs PASS;
> formatting, final staged diff, and hygiene checks pass.

## Goal and authority

Introduce provider abstraction, metadata registry, eligibility, and deterministic
routing without executing providers. Base: `a6325b5` (completed Phase 06).
`PHASES.md` remains execution authority; see §0.2 and its Phase 07 lead decisions,
plus `DECISIONS.md` ADR-020. The owner authorized continued Phases 07–18 and
delegated routine conservative policy to the lead, not individual approval of
every value. Continue in roadmap order without a new phase-by-phase prompt.
No skipped gates or automatic push; consequential cost/security/licensing/
publication decisions remain subject to owner approval.

## Implemented (source-inspected and reviewed)

- Existing `provider.Provider` signature unchanged; `domain.ProviderCapabilities`
  adds `CostUnit string` and `EstimatedLatencyMs *int64`. Separate ProviderHealth
  states UNKNOWN/HEALTHY/UNHEALTHY/DISABLED; registration starts UNKNOWN.
- Zero-value-usable `provider.Registry`, `NewRegistry()`, explicit
  `Register(ctx, Provider)`, `Refresh(ctx, id)`, `SetHealth(ctx, id, state)` return
  error; `Get(ctx, id) (Provider, Snapshot, error)` returns caller-owned adapter
  reference + copied metadata, not eligibility. `List(ctx) ([]Snapshot, error)`
  captures the whole registry coherently in lexical ID order.
- Discovery outside locks with cooperative context; copied tags/Hardware/latency
  pointers, atomic publication, failed refresh leaves capabilities intact.
  Preserve intervening health updates; valid refreshes last-publication-wins,
  not start-order freshness. No polling, implicit providers, removal/replacement.
- `NewRouter(*Registry) (*Router, error)` rejects nil. `RouteRequest` has
  RequiredTags/PreferredIDs/Locality, `MaxCost *CostLimit`, `MaxLatencyMs *int64`,
  `Hardware HardwareFacts` containing `AvailableMemoryGB *float64`/`HasGPU *bool`.
  `Clone()` copies all nested pointers/slices; `Validate()` rejects invalid input.
- `Route(ctx, RouteRequest) (RouteResult, error)` calls no adapter methods,
  including ID. `EligibleIDs` includes first choice + all alternatives;
  `FallbackIDs` independently copies the tail. Rejections are lexical-ID ordered,
  owned codes ordered health/tags/locality/cost/latency/hardware. When none are
  eligible, return rejection details alongside `ErrNoEligibleProviders`.
- `Resolve(ctx, id, RouteRequest) (provider.Provider, error)` checks exact ID
  against current captured metadata/policy, then adapter ID outside locks.
  Never substitutes fallback; admission is point-in-time, not a lease.
- `decision.NewProviderResolver(*provider.Router, provider.RouteRequest)` returns
  `(*ProviderResolver, error)`, copies policy and adds mandatory semantic-decision
  (initially empty tags allowed here). `Resolve(ctx, id) (decision.Provider, error)`
  asserts real Decide implementation and maps permanent errors to Phase 06
  identity/not-found/invalid-provider sentinels, retaining underlying causes.
- Engine unchanged: callers explicitly use `DefaultConfig(result.EligibleIDs...)`;
  its eight-provider cap remains. Fixture now declares local deterministic +
  semantic-decision, explicit empty HardwareRequirements, free `USD/decision`,
  nil/unknown latency; no billable service or electricity estimate is invented.

## Design decisions and boundaries

Accepted lead policy: ADR-020; details: `ARCHITECTURE.md` appended Phase 07.
Require nonempty ALL-tag requests, exact case/untrimmed match, explicit HEALTHY,
local-only default (`LocalOnly`); remote opt-in `RemoteOnly`/`AnyLocality`.
Filter all constraints first, then eligible preferences in caller order, then
deterministic (including opted-in remote), local nondeterministic, other remote;
lexical tier ties. Unknown/ineligible preferences ignored; blank/duplicate rejected.
Fallback lists never execute/retry/relax gates. Exact cost units, no conversion;
labelled zero free/unlabelled zero unknown, nonzero requires unit, finite
nonnegative cost and nonnegative latency, inclusive caps. Unknown/mismatched
estimates fail applicable caps only. Local nil Hardware is unknown/ineligible;
explicit `{}` no minimum. Positive RAM/GPU requirements need known sufficient
pointer facts, not all caller facts unconditionally. Remote HW ignores local facts.
No Phase 08 leases/reservations/enforcement. Caller owns safe concurrent adapter
lifecycles and stable metadata during copying; no detached work/hidden timeouts.
Core error strings/rejections use safe codes/nonsecret handles; raw unwrapped
causes still require caller redaction. Phase 07 metadata is not Phase 06 redesign.

## Files changed

- New production: `provider/registry.go`, `provider/router.go`, `provider/errors.go`,
  `domain/provider_health.go`, `decision/provider_resolver.go`.
- Changed metadata: `domain/provider.go`, `decision/fixture.go`; ownership comments:
  `provider/provider.go`. No `decision/engine.go` change.
- New tests: `provider/registry_test.go`, `provider/router_test.go`,
  `decision/provider_resolver_test.go`.
- Docs owned here: append Phase 07 only in `ARCHITECTURE.md`, modify ADR-020 only
  in `DECISIONS.md`, reconcile this evidence. Previous phases/ADRs unchanged.
  Tracker/status and review records remain lead-owned.

## Tests

Results/proofs are lead/reviewer/implementer reports, not documentation-agent runs.
Baseline/initial gates passed; lead latest full gate after fixes PASS, gofmt clean.
The final post-confirmation full gate also passes, as do ten repeated
provider/decision runs and formatting checks; no documentation-agent test claim.

| Command | Baseline `a6325b5` | Lead after latest fix | Post-confirmation |
| --- | --- | --- | --- |
| `go test -count=1 ./...` | PASS | PASS | PASS |
| `go test -race -count=1 ./...` | PASS | PASS | PASS |
| `go build ./...` | PASS | PASS | PASS |
| `go vet ./...` | PASS | PASS | PASS |

Coverage includes snapshot/JSON ownership, atomic refresh/concurrent health,
reentrancy/cancellation, exact ALL-tag/locality/health/cap gates, stable ranking,
result/request copying, exact-ID admission, decision interface/sentinel mapping,
and preserved retry/uncertainty/input-isolation/audit behavior through the bridge.
Final lead scan of all 120 tracked files found no matching credential/private-key/
home-path patterns, unexpected media/model/environment artifacts, or oversized files.

## Sequential reviews

- Luna6 (`luna6`), `amazon-bedrock/us.openai.gpt-6-luna`: **FINAL APPROVE**;
  final scoped provider unit/race/vet PASS. One LOW R1 test-oracle finding closed.
  Lead-owned record: `docs/reviews/phase-07-review-luna6.md`.
- Astra (`astra`), `amazon-bedrock/us.openai.gpt-6-astra`: **FINAL APPROVE**;
  final provider+decision unit/race/vet/diff checks PASS. One LOW R2 oracle finding
  closed. Lead-owned record: `docs/reviews/phase-07-review-astra.md`.
Final Luna6 approval preceded final Astra approval sequentially. Roster/catalog
verification is lead-reported; formal records are reconciled separately by lead.
Neither formal review found a production defect; no outstanding findings remain.

## Findings resolved and proof

- **Lead self-review — production correction:** stable adapters returning ordinary
  Capabilities zero + error incorrectly became identity mismatches. Check adapter
  ID before/after even on errors; validate result ProviderID/metadata on success
  only, ignoring error payloads. Actual ID drift still wins; preserve causes/safe
  strings. Six regressions were RED before/GREEN after, with no-partial-refresh
  guards (`TestDiscoveryIdentityPrecedesErrorAndPreservesCauses` and
  `TestRefreshDiscoveryErrorIgnoresPayloadAndPreservesSnapshot`).
- **Luna6 R1 LOW — test-only:** `TestRegistryCopiesEveryMetadataFieldAndJSON`
  now uses independent literal Snapshots, not production clone helpers, for
  Register/Refresh and Get/List ownership: tags, Hardware, latency, scalars/health.
  Original test + shallow-clone mutant PASS; hardened test FAIL on adapter mutation.
  Original production copying restored; lead verified empty production diff.
- **Astra R2 LOW — test-only:** old Unwrap oracle mutated only first cause without
  asserting permanent sentinel survival. Reviewer's external aliasing overlay
  passed the original provider suite. Repo assertions now clear ALL returned
  causes and check applicable ErrInvalidProvider/ErrIdentityMismatch, transient,
  `errors.Is/As`, and the original typed-pointer identity. Implementer old-mutant
  PASS/new-mutant FAIL in all six cases; production defensive Unwrap restored,
  lead verified empty production diff. No production Unwrap defect was reported.
These fixes harden delegated policy/tests, not new product policy or gate relaxation.

## Limitations, commit, and continuation

No inference, installation, external calls, generation execution, or leases;
coarse declarations do not guarantee later health/availability or reserve resources.
Cooperative context cannot forcibly stop an ignoring adapter.
Single commit: **`phase(07): implement provider registry and routing`**
(the commit containing this evidence; resolve its hash from repository history).
All §14 checks pass. After this gate and single local commit, Phase 08 begins automatically under
continuous owner authority, without a fresh prompt. No automatic push; this
documentation owner does not stage/commit/push or advance Phase 08 itself.
