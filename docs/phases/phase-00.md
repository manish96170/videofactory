# Phase 00 — Foundation / Contracts

## Goal

Create the Go repository foundation and stable domain contracts
(PHASES.md "PHASE 00 — FOUNDATION / CONTRACTS").

## Implemented

- Standalone git repository (isolated from an unrelated parent workspace),
  module `github.com/manish96170/videofactory`, Go 1.23.
- `domain` package: all Core Domain Model data contracts from PHASES.md
  section 5 — `Project`, `VideoPlan`, `Shot`, `Timeline`, `Asset`,
  `Primitive`, `Flow`, `Framing`, `DecisionRequest`, `DecisionResult`,
  `ProviderCapabilities`, `GenerationRequest`, `GenerationResult`,
  `ValidationResult`, `ResourcePlan`, `ModelCapabilities`,
  `VisualElement`, `RenderManifest`, `ReviewFeedback`, `ChangeSet` — plus
  `domain.ID` and small open string vocabularies (`ShotCategory`,
  `PrimitiveType`, `AssetKind`, `DecisionVerdict`, `Quality`,
  `ValidationStatus`, `ChangeSetStatus`, `FeedbackType`).
- `event` package: event type vocabulary (PHASES.md section 10) plus
  `Publisher`/`Subscriber` contracts.
- `provider` package: `Provider` interface (capability-based routing).
- `decision` package: `Provider` interface plus the pure `Policy`
  threshold type (with `Validate`) from PHASES.md section 8.
- `resource` package: `Request`, `Lease`, `Manager` contracts
  (PHASES.md section 9).
- `cmd/videofactory`: minimal entrypoint proving the module builds to a
  runnable binary.
- `.gitignore`, `README.md`, `ARCHITECTURE.md`, `DECISIONS.md`,
  `ROADMAP.md`.
- `AGENTS.md` (created in a prior session, confirmed still accurate).

## Design decisions

See `DECISIONS.md` ADR-001 through ADR-008. Summary:
- Standalone repo, isolated from its parent workspace (ADR-001).
- Module path/name chosen explicitly by the owner (ADR-002).
- One `domain` package for data; small per-concern packages for behavior
  (ADR-003).
- Open string vocabularies instead of closed enums for
  categories/capabilities (ADR-004).
- ID generation strategy deferred to Phase 01 (ADR-005).
- `decision.Policy` included as a pure value type, not a decision engine;
  amended with `Validate()` after review (ADR-006).
- Duration/timing serialization and relativity conventions documented
  (ADR-007).
- `map[string]any` extension-field JSON contract documented as an
  accepted Phase 00 tradeoff (ADR-008).

## Files changed

35 new files: `go.mod`, `domain/*.go` (14 files + 2 test files),
`event/*.go` (2 files), `provider/*.go` (2 files), `decision/*.go`
(2 files), `resource/*.go` (2 files), `cmd/videofactory/main.go`,
`.gitignore`, `README.md`, `ARCHITECTURE.md`, `DECISIONS.md`,
`ROADMAP.md`, plus this evidence file and the two review files under
`docs/reviews/`.

## Tests

```text
go build ./...        PASS
go vet ./...           PASS
go test ./...          PASS (all packages)
go test -race ./...    PASS (all packages)
gofmt -l .              (clean, no output)
go run ./cmd/videofactory   PASS (prints "videofactory dev (foundation phase)")
```

Round-trip JSON tests cover every Core Domain Model type. `decision`,
`event`, `provider`, and `resource` packages each have fakes proving
their interfaces are implementable, including cancellation and
over-budget/release-restores-capacity paths.

## Review 1

Reviewer: terra (`amazon-bedrock/openai.gpt-5.6-terra`). See
`docs/reviews/phase-00-review-terra.md`. No BLOCKERs; 1 HIGH, 2 MEDIUM,
2 LOW findings plus one architecture concern — all resolved.

## Review 2

Reviewer: astra (`amazon-bedrock/us.openai.gpt-6-astra`). See
`docs/reviews/phase-00-review-astra.md`. No BLOCKER/HIGH; 3 MEDIUM, 3 LOW
findings — all resolved.

## Findings resolved

All HIGH/MEDIUM/LOW findings from both reviews were resolved (see the
two review files for the mapping of finding → resolution). No findings
were deferred.

## Known limitations

- `domain.ID` has no generation strategy yet (by design — Phase 01).
- `event`/`resource`/`decision` packages contain only contracts; no real
  event bus, resource enforcement, or decision provider exists yet (by
  design — Phases 02/08/06 respectively).
- `map[string]any` extension fields decode numbers as `float64`
  (standard `encoding/json` behavior); lossless/typed payloads are a
  later-phase concern if needed (ADR-008).

## Commit

See the phase commit immediately following this evidence file in the
repository history (`phase(00): establish foundation and contracts`).

## Follow-ups

- None blocking. Future phases should keep the timing/relativity
  conventions in ADR-007 in mind when implementing the renderer
  (Phase 03) and timeline (Phase 01).
