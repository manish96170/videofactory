# Phase 11 — Formal Review: Astra

**Model:** `amazon-bedrock/us.openai.gpt-6-astra` · **Date:** 2026-10-12 ·
**Commit reviewed:** dirty Phase 11 state after Luna6 closure · **Scope:** audio/domain
implementation, serialization, SRT, cancellation, and phase boundaries.

Astra found HIGH unsafe/cyclic metadata admission and MEDIUM clone, standard-SRT, and
UTF-8 export issues. The accepted metadata contract is now bounded plain JSON only,
cycles/unsupported values are rejected before callbacks, SRT uses standard two-digit
hours, and invalid UTF-8 is rejected. A LOW requested JSON round-trip regression was
also added. Astra independently passed scoped race/vet checks.

**Final status: APPROVE.**
