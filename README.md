# Video Factory

A deterministic video factory / video compiler with pluggable AI shot
generators — not primarily an AI video model. See `PHASES.md` for the full
product definition, architecture, and execution plan (it is the execution
authority for this project; see `AGENTS.md`).

```text
Prompt → Director → VideoPlan → Shot Planner → Shot Intelligence
  → Decision Engine → Provider Router → Resource Planner → Execution
  → Validation → Timeline → Final Render
```

## Status

Phase 05 (Director / Video Planning). See `ROADMAP.md` for phase status
and `ARCHITECTURE.md` for the as-built architecture.

## Requirements

- Go 1.23+
- [FFmpeg](https://ffmpeg.org/) and `ffprobe` on `PATH`, for the
  `render` package. A plain `ffmpeg` build is enough for
  Background/Image-only shots; shots using the `Text` primitive need
  `ffmpeg` built with `drawtext` support (requires `libfreetype`). On
  macOS, Homebrew's default `ffmpeg` formula does **not** include
  `drawtext`; `brew install ffmpeg-full` does. Tests that need `ffmpeg`
  skip gracefully if it (or `drawtext`) isn't available.

## Build

```sh
go build ./...
```

## Test

```sh
go test ./...
go test -race ./...
go vet ./...
```
