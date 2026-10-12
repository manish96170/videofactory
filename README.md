# Video Factory

A Go video compiler built around deterministic rendering, verified shot caching,
and provider-neutral planning and execution contracts.

**License:** proprietary, source-available; **not open source**. The
[Video Factory Personal Non-Commercial No-Derivatives License](LICENSE) permits
personal, non-commercial use and compilation of unmodified source. Modification,
derivative works, commercial use (including monetized content), redistribution,
and sublicensing require separate written permission, subject to the license's
non-waivable-law exceptions. Public source visibility grants no additional rights.

## What works today

- FFmpeg rendering of Background, Image, Text, and whole-shot fade Transition
  primitives, with FFprobe checks and stable visual-element manifests.
- A fixture Director that turns sentence-based prompts into structured plans;
  it uses fixed heuristics, not a language model.
- A synchronous deterministic pipeline with integrity-checked artifacts,
  checkpoints, cache reuse, affected-shot invalidation, and final MP4 composition.
- Programmatic preview/review, audio/timestamp, ChangeSet, resource/model lifecycle,
  and terminal control-plane packages.
- A CLI for help, version, explicit configuration validation, and one TUI snapshot.

Local and cloud adapters use injected execution dependencies and offline fixtures.
There is no bundled live AI integration. The pipeline returns pending approval for
local/cloud/mixed execution; its approval/executor interfaces are not invoked.
The CLI has no render command or interactive player. The renderer produces silent
video; audio/timestamp contracts do not add an audio track to that output.

Phases 00–17 are complete. Phase 18 release-readiness work is in progress; see
[PHASES.md](PHASES.md) for the authoritative tracker and
[release readiness](docs/release-readiness.md) for verification boundaries.

## Architecture at a glance

```text
Prompt → Director → VideoPlan → Shot Intelligence
                                  │
                 deterministic    ├─→ Preview coordinator ←→ Artifact / checkpoint stores
                                  │       ↓ render + validate
                                  │   Verified shot views → Timeline composer → MP4
                 local/cloud/mixed└─→ Pending approval (no execution)

Provider registry/router + Decision Engine + Resource/model managers
  = injected planning/admission/lifecycle boundaries
Project + timeline + stable element IDs
  = authoritative state for preview, TUI, feedback, and ChangeSets
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for package details and
[providers](docs/providers.md) for adapter and hardware limitations.

## Build and try

Use Go 1.23 or newer from the repository root. The Go module uses only the
standard library and has no third-party Go module dependencies.

```sh
mkdir -p bin
go build -trimpath -buildvcs=false -o bin/videofactory ./cmd/videofactory
./bin/videofactory help
./bin/videofactory version
./bin/videofactory tui
```

Rendering additionally requires external `ffmpeg` and `ffprobe` on `PATH`, with
the `libx264` encoder. Text requires the `drawtext` filter and a usable font.
See [dependencies](docs/dependencies.md) for capability checks and licensing.

The [quickstart](docs/quickstart.md) covers strict JSON configuration and runnable
deterministic sample tests, including a real MP4 composition check.

## Verify

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -trimpath -buildvcs=false ./...
```

Native integration tests can skip when prerequisites are absent; inspect verbose
test output before treating a run as render verification. See
[CONTRIBUTING.md](CONTRIBUTING.md) for authorized development and cross-builds,
[DECISIONS.md](DECISIONS.md) for design records, and
[docs/licensing.md](docs/licensing.md) for the licensing decision.
