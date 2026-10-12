# Phase 11 — Audio / Timestamp Pipeline

## Implemented

Provider-neutral audio assets, words, subtitle cues, and temporal targets use existing
shot-relative, end-exclusive duration semantics. The `audio` package validates audio/
word/cue/sync boundaries, formats strict standard SRT, and provides deterministic voice,
local-ASR worker, and unsupported Apple-TTS seams without external execution.

Metadata is a bounded cycle-safe plain JSON tree and all provider boundaries copy it.
Sub-millisecond SRT cues are rejected rather than silently truncated.

## Reviews and tests

Luna6 found two MEDIUM and two LOW findings; all were fixed and confirmed. Astra found
one HIGH and three MEDIUM safety/interoperability findings; all were fixed and Astra
approved. Final uncached repository test, race, vet, and build gates pass.

## Deferred

No real TTS/ASR, audio mux/rendering, lip animation, UI, provider routing, or scheduler
is included.
