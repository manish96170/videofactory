# Next Session Handoff

## Current phase

Phase 09 — Validation / Shot Intelligence, **2026-10-11** checkpoint.
Read `PHASES.md` in full before edits: execution authority §0.2/§12–15/§23/§26–27/§34.

## Historical checkpoint status

**IN PROGRESS — FIX REQUIRED at rollover.** Validation API frozen/scoped-tested; intelligence
source and both test suites present. All assigned implementation/test tasks ended;
no active code/test writers. Two acceptance ownership bugs are UNFIXED.
Both formal reviews NOT STARTED, full final gate/Phase 09 commit pending.
The owner explicitly requested stop/checkpoint/new-session rollover at high context;
do not keep implementing in the old session merely because standing authority exists.

Standing §0.2 authority permits continued Phases 07–18 in roadmap order and routine
conservative lead decisions without a fresh prompt after each completed phase.
It does not authorize skipped gates, paid generation, large downloads, publication,
licensing choices, or consequential scope/security/cost changes. No automatic push,
amend/force-push/history rewrite. Phase 10 starts only after Phase 09 gate/commit.

## Completed

- Phase 08 DONE at `61e078b`; its full gate/reviews/source/handoff remain closed.
- Phase 09 read-only design scout and delegated policy recorded in PHASES/ADR-022.
- Validation/native/render source lead-inspected and scoped uncached normal/race/vet
  PASS after three lead red/green refinements: domain.Quality alias, nil contexts,
  permanent-failure retry precedence. Validation API frozen; do not redesign it.
- Intelligence bounded follow-up: explicit audio-intent gates, local binding/profile/
  workload/provenance checks, safe validation severity scrubbing; four focused tests
  and lead scoped normal/race/vet PASS before acceptance addition.
- Five additional acceptance functions in one test-only file, completed with actual
  failures reported. Lead independently reproduced the failing ownership subtest.
- Partial as-built architecture, ADR-022 checkpoint, phase evidence, this handoff.
  Documentation owner source-inspected APIs/test names but ran no code tests.

## Superseding resume update (2026-10-11)

P09-A1/A2 and Astra's four MEDIUM findings are resolved with retained/additional
regressions. The lead full uncached repository test/race/vet/build gate passed; Astra
approved. Terra found no production defect or test gap, but requested documentation
and Astra-evidence reconciliation before approval. This handoff is historical only;
the live tracker and review records govern current closeout.

## Historical incomplete items at rollover

- P09-A1 HIGH and P09-A2 MEDIUM production fixes; existing regression must go GREEN.
- Lead acceptance/full-repository gate after fixes, formal Astra → Terra reviews,
  finding resolution/retests/evidence, final docs/diff/hygiene and one local commit.
- Main's tracker/README/roadmap/acceptance finding checkpoint updates are present;
  re-read current files and reconcile again after fixes/reviews.

## Current commit

- Branch `main`, HEAD `61e078b` — `phase(08): implement resource and model management`.
- Prior `dbc5ace` — `phase(07): implement provider registry and routing`.
- **No Phase 09 commit or push.** Dirty unstaged work must be preserved.

## Working tree

Observed after checkpoint document writes (`git status --short --untracked-files=all`):
```text
 M ARCHITECTURE.md
 M DECISIONS.md
 M PHASES.md
 M README.md
 M ROADMAP.md
 M render/probe.go
?? docs/handoffs/next-session-phase-09.md
?? docs/phases/phase-09.md
?? docs/reviews/phase-09-acceptance-findings.md
?? intelligence/acceptance_test.go
?? intelligence/helpers.go
?? intelligence/intelligence.go
?? intelligence/intelligence_test.go
?? render/support.go
?? validation/native.go
?? validation/native_test.go
?? validation/process.go
?? validation/validation.go
?? validation/validation_test.go
```
Cached diff empty. Snapshot is **not revert/delete permission**; recheck live status/
history. Main owns README/PHASES/ROADMAP/acceptance findings; doc owner only four docs.
No secrets/private paths, model weights/generated media or machine config in public docs.

## Source/API anchors

- `validation/validation.go`: Quality aliases domain.Quality; Requirements has explicit
  kind/quality/duration/FPS/dimensions and optional audio/minimum-image/uniform settings.
  `NewMediaChecker(Inspector, Config)`; `Validate(ctx, Request)` returns existing
  domain.ValidationResult. Inspector receives URI/Kind, returns measured facts;
  Asset.Metadata untrusted, never inspected/passed. Default 5s/±150ms/±0.01 FPS.
- `validation/native.go`/`process.go`: authorized regular roots, no protocol/network
  URI or symlink escapes, source 1 GB/dimensions 8192/pixels 16,777,216; metadata/
  stdout 64 KiB, stderr 8 KiB. Forced demuxers, file-only protocols, MOV refs disabled,
  decoder stdin disabled; PNG/JPEG/GIF guarded image.Decode, 8x8 thumbnail/20ms first audio.
  Not full-stream integrity/all-animation-frame QA/hostile-FS race/kernel containment.
- `render.SupportsPrimitive` is actual Background/Image/Text/Transition support;
  `ProbeResult.AudioTracks` counts all streams independently of video order.
- `intelligence.New(Primitives, Validator, Router, Decider, Planner)`/`Analyze`:
  authoritative Project/ShotID/ReqID, explicit VIDEO duration matching shot,
  <=8 associated Candidates, <=16 trusted Bindings, <=8 explicit distinct audio tags.
  Candidate suitability typed; Project.Assets only, never asset DependsOn/ShotRef.
  Strategies deterministic/reuse_video/static_image/animate_image/generate_video/none
  are advisory; conceptual support/current renderer/input readiness stay separate.
- Optional 5s structured decisions use bounded scalar duration/count/candidate/
  dimension/FPS/audio descriptors, no prose/type detail (known semantic limitation).
  Safe validation core evidence/provenance matching is implemented, but complete
  caller/input isolation is broken by A1/A2; do not repeat a blanket nonmutation claim.
- Default local-only routes retain all Phase 07 gates. Matching local evidence can
  return local_ready/wait/local_ineligible; Any/Remote resource status remains unknown
  because RouteResult cannot attest locality. Explicit remote routing is allowed,
  not remote_available resource bridging or permission to execute/spend.

## Historical tests at rollover

All execution below is prior lead/test-owner evidence, not new doc-owner testing.
- Validation/render scoped uncached normal/race/vet PASS after final validation fix.
  TestLeadQualityAlias/TestLeadNilContextsAndPermanentRetryPriority pin red/green work.
- Intelligence focused normal/race/vet PASS **before acceptance_test.go**;
  four functions named in `docs/phases/phase-09.md`. This is not final gate PASS.
- Acceptance owner normal and race: **FAIL**, only
  `TestAcceptanceCancellationAndOwnership/independent-inputs-and-return-evidence`;
  other cases owner-reported PASS. No race diagnostics, not a passing race suite.
- Lead independently reproduced normal failure:
  `go test -count=1 -timeout=30s ./intelligence -run '^TestAcceptanceCancellationAndOwnership/independent-inputs-and-return-evidence$'`.
  Original fontSize int becomes float64; callback changes caller binding; router
  mutation gives resource unknown/skips admission, planner count 1 rather than 2.
- Lead same focused subtest under -race/60s FAIL, no race diagnostics; repository
  `go build ./...`/`go vet ./...` PASS. Full post-addition test/race gate not run/passed.
  Historical Phase 08 full gate is not verification of dirty Phase 09.

## Historical review plan at rollover

### Reviewer 1
- Astra: `amazon-bedrock/us.openai.gpt-6-astra`, **APPROVED after R1 fixes**.

### Reviewer 2
- Terra: `amazon-bedrock/openai.gpt-5.6-terra`, reviewed after Astra; documentation
  confirmation pending.

Roster/catalog mapping was lead-verified earlier. Use two distinct models different
from implementation; verify availability on resume. Lead acceptance findings are
**not** formal review evidence and do not satisfy either review gate.

## Documentation updated

This task owns only ARCHITECTURE Phase 09 append, DECISIONS ADR-022, phase evidence,
and this new handoff. Main owns tracker/README/roadmap and acceptance finding record:
`docs/reviews/phase-09-acceptance-findings.md` (present at checkpoint). Preserve earlier
phase history byte-for-byte. No staging, commits, pushes, agents, or code edits by doc owner.

## Historical known issues at rollover — resolved

- **P09-A1 HIGH — RESOLVED**, `intelligence/intelligence.go:141`, historical Analyzer.Analyze:
  snapshot(req, &req) JSON decode reuses populated caller pointers/slices/maps.
  Minimal proposed fix: decode into fresh zero Request, then assign local req.
- **P09-A2 MEDIUM — RESOLVED**, `intelligence/helpers.go:216`, historical Analyzer.route:
  router receives retained mutable policy; callback changes RequiredTags used later.
  Minimal proposed fix: pass policy.Clone(). The production fix is now applied.

## Historical exact next actions at rollover

1. Read full PHASES/AGENTS, this handoff, current Phase 09 docs and acceptance findings;
   inspect live status/cached diff/history. Preserve work; no active writer to wait for.
2. Assign **one bounded producer** only A1/A2 and regression retests, not a rewrite.
   Run existing focused test RED first, apply fresh Request decode + policy.Clone,
   then focused GREEN and all intelligence uncached normal/race/vet. Report exact diff/results.
3. Lead inspect fixes/tests independently; run full uncached repository unit/integration/
   race/build/vet gate. Do not commit a failing/dirty unreviewed phase or skip gates.
4. Astra formal review → scoped fixes/retests → Terra formal review of updated state →
   fixes/retests/final lead review. Preserve findings/severity/resolution/retest evidence.
5. Reconcile docs/tracker, final formatting/diff/secret/artifact hygiene; complete §14,
   one local Phase 09 commit, verify status/history; only then continue Phase 10 under §0.2.

## Historical do-not-redo guidance

Do not rebuild Phase 08/replay closed render milestones/redesign frozen validation/
discard acceptance tests. Prior oversized Phase 08 model task caused delays: small bounded tasks,
disjoint test-only files, no repeated docs/full gates without meaningful code changes,
no progress polling or massive agent prompts. No generation/acquisition/model loading,
cache/player/speech/provider installs/downloads/paid calls in Phase 09.
Completed owner sessions (historical, none active): validation
`ses_ed827b1c4ffe8efrvF2XU5MflZ`; intelligence `ses_ed67df163ffefm3zAhNHMSbu0p`;
test-only `ses_ed65f4c94ffeTZ6HrDMiQvAj6g`. A fresh bounded fix owner was appropriate.

## Historical next-session prompt at rollover

This rollover prompt is superseded. A1/A2 and Astra R1 findings are fixed, the full
gate passed, and Astra approved. Current required action is Terra documentation
confirmation, then final hygiene and one local Phase 09 commit; do not push. Phase 10
continues automatically under §0.2 only after that completion gate.
