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

Phase 00 (Foundation / Contracts). See `ROADMAP.md` for phase status and
`ARCHITECTURE.md` for the as-built architecture.

## Requirements

- Go 1.23+

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
