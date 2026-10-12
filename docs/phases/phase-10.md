# Phase 10 — Preview / Checkpoint / Cache / Visual Review

## Goal

Make iterative rebuilds cheap, deterministic, inspectable, and resumable without
making rendered media or a UI the source of truth.

## Implemented

- Canonical typed SHA-256 cache keys and dependency invalidation closure.
- Controlled-root atomic artifact publication, integrity-verified cache lookup, and
  separate ID-only visual metadata bindings.
- Versioned checkpoint persistence/resume that revalidates every claimed artifact.
- Append-only validated review-feedback persistence and detached preview read model.
- Synchronous cache-hit/miss render coordinator with media validation and measured
  render duration. Peak memory remains explicitly unknown unless measured.

## Reviews and tests

Luna approved after identity-only manifest rebinding was added. Luna6 approved after
validation-gated cache hits and render measurements were corrected. The lead passed
the uncached repository test, race, vet, and build gate after the final fixes.

## Limitations / follow-ups

The actual local player and scrubber UI are Phase 12 concerns. Phase 10 supplies
programmatic `View.AtShot`/`AtProject` and stable elements only; it does not create a
web/TUI player, ChangeSets, audio timestamps, provider execution, or a scheduler.
