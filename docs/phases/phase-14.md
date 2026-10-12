# Phase 14 — Local AI Providers

## Implemented boundary

`localprovider` supplies configured local adapter descriptors and injected worker/model
seams for generation, decision, and ASR capability paths. It declares hardware/profile
facts and returns configured provenance, but never installs, downloads, invokes, or
benchmarks a real model. The caller owns router admission, resource leases, model
load/use/unload, and cleanup.

## Mac compatibility

Metal is represented only as an explicit trusted profile/backend declaration. This phase
does not detect hardware, assert model compatibility, or guarantee any Apple runtime.

## Review

Terra found no code issue and required this documentation/status closeout. Luna found no
code issue and required only the README in-progress correction, now resolved. Both
reviews approve the implementation; final gate, evidence finalization, and commit remain.
