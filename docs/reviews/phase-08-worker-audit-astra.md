# Phase 08 Supplementary Worker Reliability Audit — Astra

**Role:** component reliability/security audit; NOT formal phase Review 1/2.
**Reviewer:** astra — `amazon-bedrock/us.openai.gpt-6-astra`
**Date:** 2026-10-10
**Commit reviewed:** current Phase 08 worker/resource source above `dbc5ace`,
after lead typed-nil monitor correction. Model package remained separately owned.
**Scope:** worker protocol, bounded I/O, monitoring, stop proof, process family,
measurements, lifecycle ownership, tests, and frozen resource/domain contracts.

## Findings and resolution

### MEDIUM

1. **A08-M1 — Raw JSON identity can disagree with decoded correlation.**
   Case-insensitive Go field matching/duplicates can validate an alias while
   retaining/transmitting raw JSON with a different literal `id`/`requestId`.
   **Resolution: FIXED.** Bounded exact-key scanning rejects aliases and
   duplicate decoded keys at known struct boundaries (including escaped
   equivalents). Params/Metadata permit case-distinct extension keys; error
   payloads stay opaque. Valid wire bytes are preserved, not re-marshaled.
2. **A08-M2 — Cleanup sampling loses high-water/breach evidence.**
   Stop-proof queries observe valid live-family RSS but do not retain it.
   A cleanup-only 3 GB observation after a 100-byte initial sample can yield
   success/complete measurement under a 2 GB limit.
   **Resolution: FIXED.** Uncanceled cleanup samples feed shared peak/breach
   accounting. Cleanup proves stop/reap/join before returning true with sticky
   `ErrLimitExceeded`; reply suppressed and 3 GB retained as partial evidence.
3. **A08-M3 — Active monitoring errors suppressed after leader reap.**
   `observe` ignores an uncanceled sample failure when Wait has completed,
   although descendants may remain. Intentionally canceled observations are
   already distinguished by the context check.
   **Resolution: FIXED.** All active-context monitoring errors are retained
   after leader reap; intentionally canceled observations remain ignored.

### LOW

1. **A08-L1 — Descendant-stop test accepts arbitrary query failure as absence.**
   A timeout/start/unexpected query error can pass the independent child-stop
   assertion. Production no-live-group proof is separate.
   **Resolution: FIXED.** Bounded whole-process-list assertions require query
   success and unique valid-PID parsing; uncertainty fails rather than implying
   absence. Valid absence/dead-state results remain accepted.

## Independent verification

Astra ran uncached worker/resource/domain tests, worker race tests, and vet:
PASS. Four external-overlay regressions for M1–M3 failed as expected, including
under race testing without a reported data race. An external-overlay eight-caller
concurrent retry test passed under race checking, with no overlapping pending
queries and joined tasks. No repository changes; overlays outside the repository.

Worker owner produced red/green regressions for all four findings. Lead inspected
the changed protocol and runner evidence paths, then ran worker/resource/domain
unit tests, worker race tests, and vet (PASS). Astra confirmation on 2026-10-11
independently reran those checks plus all five original external-overlay tests
under race testing (PASS), including concurrent cleanup retries and native macOS
inherited-pipe cleanup. No new actionable defect; Linux runtime was not executed.

## Positive observations and limits

Explicit lease ownership; one Wait; bounded capture capacity; inherited-pipe
descendant cleanup; quarantine retention; typed-nil rejection; safe diagnostics;
parent-only measurement source. Native descendant tests passed on macOS.
Linux runtime was inspected, not executed; native ps remains trusted dependency.
Trusted workers may not escape their group; no kernel sandbox claim.

## Status

Initial verdict: **CHANGES REQUESTED: 3 MEDIUM + 1 LOW**.
Final component confirmation: **APPROVE — ALL FOUR FINDINGS CLOSED**.
Formal whole-phase Terra → Luna reviews remain required after model integration
and full retests; this is not phase DONE.
