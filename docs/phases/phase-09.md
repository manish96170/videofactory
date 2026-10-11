# Phase 09 — Validation / Shot Intelligence

> **COMPLETE (2026-10-11).** The frozen validation API,
> intelligence implementation, acceptance ownership fixes, and Astra R1 fixes pass
> the lead full uncached gate. Astra has approved; Terra review, final evidence/docs/
> hygiene, and the one phase commit remain. No active code/test writers.

## Goal and authority

Measured media validation and deterministic-first shot advice prevent unnecessary
expensive generation. `PHASES.md` §0.2/Phase 09/§14 and ADR-022 govern. Routine
conservative policy is delegated to the lead, not individually owner-approved defaults.
Standing roadmap authority survives rollover; the explicit stop ends work now.
No paid calls/downloads/publication or skipped gates; consequential scope/security/
cost/licensing choices need owner approval. Keep future tasks small and bounded.

## Baseline and provenance

- Start/HEAD: `61e078b`, `phase(08): implement resource and model management`, branch `main`.
  Prior Phase 07 is `dbc5ace`. Phase 08 is DONE; its final full gate is immutable
  prior evidence, not current dirty Phase 09 verification.
- Fresh project/provider/render baseline PASS and all execution results below are
  lead/owner reports; this documentation owner inspected source but ran no code tests.
- Preserve [Phase 08](phase-08.md), ADR-021, its Terra/Luna records and closed handoff.
  Resume from [Phase 09 handoff](../handoffs/next-session-phase-09.md), not Phase 08.

## Accepted policy and implemented boundary

Full policy: `PHASES.md` Phase 09 and ADR-022; partial source-inspected architecture:
[`ARCHITECTURE.md`](../../ARCHITECTURE.md), Phase 09. Presence is not acceptance.
- Reuse `domain.ValidationResult` and `domain.Quality`; explicit requirements,
  no label-derived resolution or parallel schema/result boolean. DurationMs is wall time.
- Asset ID/Kind/URI/Metadata remain domain fields; Metadata is untrusted. Measure
  regular authorized local media, no network/protocol URIs or symlink/root escapes.
- Deterministic facts first; only explicitly associated `Project.Assets` IDs qualify
  for measured reuse and positive suitability. No ShotRef or asset IDs in DependsOn.
- Registry conceptual capability differs from renderer support and input readiness;
  unsupported exact deterministic content does not automatically become AI.
- Provider/resource outputs are advisory, never execution/spending permission;
  preserve exact ALL-tags/health/locality/cost/latency/hardware eligibility gates.
- Cancellation before/after callbacks rejects late success; raw diagnostics are
  scrubbed and causes retained for errors.Is/As. P09-A1/A2 are resolved: callback
  inputs are isolated from the retained request/policy/provenance baseline.

## Validation API and native behavior — frozen

`validation/validation.go`: `Quality = domain.Quality`; `Requirements.Validate()`;
`NewMediaChecker(Inspector, Config) (*MediaChecker, error)`;
`MediaChecker.Validate(context.Context, Request) (domain.ValidationResult, error)`.
Request has ID/TargetID/Asset/Requirements. Inspector receives URI/Kind and returns
measured InspectionFacts (kind/duration/dimensions/FPS/codec/audio count/rate/channels,
visual/audio decode facts, optional uniform-frame fact), not metadata or a schema alias DTO.

- DefaultConfig: inspection 5s, duration ±150ms, FPS ±0.01. Video dimensions/FPS/
  duration explicit; image/audio nonapplicable fields must be zero. Images may request
  minimum rather than exact dimensions for reuse. Nil contexts fail safely.
- Results use passed/failed/warning and Details quality/core issues; observed failures
  fail media, invalid requirements/dependencies/caller cancellation return errors.
  Retry defaults false; permanent media/source/runtime/limit markers override transient/
  expired-child advice. Parent cancellation is never retry permission.
- `NewManagedNativeInspector(NativeConfig)` rejects empty roots; trusted runtime
  config owns source roots/cwd/executables/timeout. Source <=1 GB; each dimension
  <=8192, pixels <=16,777,216. Metadata/stdout <=64 KiB, stderr <=8 KiB as built.
- PNG/JPEG/GIF: guarded DecodeConfig then image.Decode. Forced video/audio demuxer,
  file-only protocols, MOV external refs disabled, decoder stdin disabled; no fetch.
  Strict bounded JSON/number checks; audio count independent of stream order.
- One 8x8 midpoint thumbnail; first audio stream's first 20ms bounded decode.
  Static/flat/dark/fades valid; optional uniform warning is not semantic/motion QA.
  Not whole-stream corruption detection, all-GIF-frame QA, hostile-FS race protection,
  or aggregate/kernel RAM/CPU/GPU containment; per-allocation guards only.
- `render.ProbeResult.AudioTracks` counts all audio; `render.SupportsPrimitive`
  reports Background/Image/Text/Transition, not property/tool readiness.

## Intelligence API — implemented and acceptance fixes verified

`intelligence.New(Primitives, validation.Validator, Router, Decider, Planner)` returns
`(*Analyzer, error)`; `Analyzer.Analyze(context.Context, Request) (Result, error)`.
Required dependencies reject typed nil; optional decider/planner may be absent.
- Request: authoritative Project/ShotID/ID, VIDEO requirements matching shot duration,
  <=8 distinct Candidate IDs and <=16 Bindings. Candidate suitability uses typed
  Suitable/ImageSufficient/AnimationSuitable verdicts, not arbitrary prose.
- AudioRequiredTags: <=8 distinct explicit composition tags, not unrelated route tags.
  MissingAudioReadiness remains explicit; new audio-required routes need these tags;
  validated audio-bearing reuse needs no generation route.
- Advisory strategies: Deterministic/ReuseVideo/StaticImage/AnimateImage/GenerateVideo/
  OpNone. Separate conceptual/support/input facts and measured asset evidence.
  Cheap known facts bypass decisions; unknown/uncertain/error does not resolve choice.
- Optional structured classifier has 5s cooperative callback limits and bounded
  duration/count/candidate-presence/dimension/FPS/audio descriptors. No prose/type
  detail: semantic usefulness is a documented limitation for formal review.
- Core validation identity/quality/status/severity scrubbing blocks forged evidence.
  Optional resource advice verifies provider/model/profile/revision/workload/provenance;
  local-only statuses local_ready/wait/local_ineligible survive matching evidence.
  Any/Remote resource evidence stays unknown: RouteResult cannot attest locality.
  Explicit remote routing still works; no remote_available bridge is claimed.

## Files and completed assignment owners

- Validation owner `ses_ed827b1c4ffe8efrvF2XU5MflZ`: completed/frozen; files
  `validation/{validation,native,process,validation_test,native_test}.go`,
  `render/probe.go`, `render/support.go`.
- Intelligence owner `ses_ed67df163ffefm3zAhNHMSbu0p`: completed checkpoint;
  `intelligence/{intelligence,helpers,intelligence_test}.go`.
- Test-only owner `ses_ed65f4c94ffeTZ6HrDMiQvAj6g`: completed
  `intelligence/acceptance_test.go`; failures reported, no production edits.
- No active code/test writer. Main owns tracker/README/roadmap/acceptance finding
  record/integration/reviews/commit. Doc checkpoint owns architecture append,
  ADR-022 only, this evidence, and new handoff; no staging/commit/push/agents.

## Tests — attributed results, not a final gate

- Lead validation/render full source inspection plus scoped uncached normal/race/vet
  PASS after three red/green refinements: domain.Quality alias, nil contexts, permanent
  retry precedence. Regressions: TestLeadQualityAlias,
  TestLeadNilContextsAndPermanentRetryPriority.
- Validation/native tests present: TestRequirementsAndTolerances,
  TestMeasuredKindsAudioImagesAndMetadata, TestDependenciesCancellationRetryAndSafeErrors,
  TestNativeLocalImageAndSourceGuards, TestNativeProbeStrictNumbersAndAudioOrder,
  TestNativeProcessBoundsAndCancellation, TestNativeRenderedVideoAndWAV.
- Four intelligence focused tests and independent lead scoped normal/race/vet PASS
  **before** acceptance additions: TestFixtureDirectorCheapPath,
  TestAudioIntentIsAnExplicitRoutingGate, TestValidationUnknownSeverityIsScrubbed,
  TestResourceEvidenceRequiresMatchingBindingAndProvenance.
- Five acceptance functions: TestAcceptanceDeterministicAndRequestBoundaries,
  TestAcceptanceAssociatedMeasuredAssets, TestAcceptanceStructuredDecisionEngine,
   TestAcceptanceRealRoutingAndResourceAdvice, TestAcceptanceCancellationAndOwnership.
   The last function's ownership subtest is historical RED evidence; it is now GREEN
   under normal and race execution after the fixes.
- Main independently reproduced historical normal FAIL with:
  `go test -count=1 -timeout=30s ./intelligence -run '^TestAcceptanceCancellationAndOwnership/independent-inputs-and-return-evidence$'`.
   Same focused subtest under -race/60s also failed, with no race diagnostics. After
   fixes, lead scoped intelligence/validation normal/race/vet and full uncached
   repository test/race/vet/build gates PASS.

## Historical checkpoint issues — resolved

- **P09-A1 HIGH (resolved)**, `intelligence/intelligence.go:141`, historical
  Analyze/snapshot(req, &req):
  decoding into populated Request reuses caller Project/binding pointers/slices/maps;
  original fontSize int becomes float64 and planner callback mutates caller binding.
  Proposed minimal fix: decode into fresh zero Request, then replace local req.
- **P09-A2 MEDIUM (resolved)**, `intelligence/helpers.go:216`, historical
  route/router callback:
  mutable policy.RequiredTags changes retained admission checks, skipping planner;
  observed planner calls 1 instead of 2/resource unknown. Proposed fix: policy.Clone().
- [Main acceptance findings](../reviews/phase-09-acceptance-findings.md) records
  the historical RED state and is **not** formal Review 1/2.

## Reviews, commit, and next gate

1. Astra — `amazon-bedrock/us.openai.gpt-6-astra`: **APPROVED** after four MEDIUM
   findings were fixed and retested; see `docs/reviews/phase-09-review-astra.md`.
2. Terra — `amazon-bedrock/openai.gpt-5.6-terra`: **APPROVED** after its MEDIUM
   documentation/evidence findings were reconciled and confirmed; see
   `docs/reviews/phase-09-review-terra.md`.
Roster/catalog IDs were verified. Final lead uncached test/race/vet/build and hygiene
checks pass. Completion is recorded by `phase(09): implement validation and shot
intelligence`; no push.
No generation/model/resource acquisition/cache/player/speech/provider downloads or
paid calls. Phase 10 follows automatically only after Phase 09 §14 completion/commit.
