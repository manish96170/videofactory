# Release readiness

Phase 18 prepares the source repository for public review. It is not a publication
or binary-distribution authorization. [PHASES.md](../PHASES.md), especially its
Phase 18 checklist and current execution state, is the authoritative completion
tracker. This document records verification boundaries rather than marking that
gate complete.

## Current deliverable

The source contains a standard-library Go core, deterministic FFmpeg rendering,
verified cache/checkpoint and preview packages, provider/resource boundaries, and
a one-shot CLI. Local/cloud integration is fixture-driven; pipeline execution of
those strategies stops at pending approval. The CLI offers help/version/config
validation/TUI snapshot commands, not prompt-to-render execution or a player.

The project is proprietary source-available under [LICENSE](../LICENSE). Personal,
non-commercial use and compiling unmodified source are permitted on its terms.
Modification, derivative works, commercial use including monetization,
redistribution, and sublicensing require separate written authorization, subject
to non-waivable law. Native tools, fonts, models, and input assets retain their own
terms; see [licensing](licensing.md) and [dependencies](dependencies.md).

## Evidence and its limits

| Area | Existing evidence | Boundary |
| --- | --- | --- |
| Architecture | [ARCHITECTURE.md](../ARCHITECTURE.md), package contracts, phase reviews | Design/review evidence is not a production deployment claim |
| Deterministic video | Real FFmpeg tests in `render`, `director`, and `pipeline` | Some native tests skip if prerequisites are absent; the pipeline composition sample still injects planning/analysis/validator fixtures |
| Cache/recovery | Pipeline and preview tests | Synthetic fixtures verify orchestration separately from native media behavior |
| Providers | Local/cloud injected runner/transport tests | No live model/service qualification or model performance measurement |
| Performance | [Phase 17 evidence](phases/phase-17.md) | Deterministic fixture observations, not hardware guarantees |
| Dependency graph | `go.mod` and `go list -m all` | No third-party Go modules; FFmpeg/FFprobe/fonts remain external |
| CLI/config | `cli` tests and `cmd/videofactory` | Strict presentation JSON and one snapshot; no generation command |

## Build matrix

[Phase 17](phases/phase-17.md) records the following compile checks. Commands for
repeating them with `-trimpath -buildvcs=false` are in
[CONTRIBUTING.md](../CONTRIBUTING.md).

| Target | Recorded check | Runtime qualification |
| --- | --- | --- |
| macOS / arm64 | CLI cross-build and host test evidence | Native render tests require the installed tools and optional text prerequisites |
| Linux / amd64 | CLI cross-build | Cross-build alone does not establish Linux runtime tests or native rendering |
| Windows / amd64 | CLI cross-build | Bounded worker runner is explicitly unsupported |

No installed native-tool compatibility or live-provider support follows from a
successful Go cross-build. Version metadata is injected explicitly; development
defaults are `dev` and `unknown`. Build flags remove local source-path information
and disable VCS stamping, but do not by themselves certify reproducible binaries
across toolchains.

## Phase 18 verification record

The final gate requires fresh full tests, race tests where applicable, vet, build,
native milestone results with skip status, the target build matrix, two sequential
independent reviews and their resolutions, and final lead inspection. Preserve the
results in `docs/phases/` and `docs/reviews/` before marking Phase 18 complete.
Earlier phase results are historical evidence, not substitutes for that gate.

The public-repository review in `PHASES.md` covers the working tree, staged content,
tracked files, and history for credentials/private data, machine-specific paths,
large generated media/binaries/weights, and third-party provenance. Documentation
changes alone do not certify those checks as passed. Public push/publication remains
a separate owner-authorized action after the repository hygiene gate.
