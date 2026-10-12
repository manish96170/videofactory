# ROADMAP.md

This is a condensed, human-friendly view of the phase roadmap. `PHASES.md`
section 20 (phase roadmap) and section 32 (Current Execution State) are
the authoritative source; this file mirrors them and must stay in sync.

| Phase | Name                                        | Status      |
|-------|----------------------------------------------|-------------|
| 00    | Foundation / Contracts                       | DONE        |
| 01    | Project State / Timeline                     | DONE        |
| 02    | Event System                                 | DONE        |
| 03    | Deterministic Renderer                       | DONE        |
| 04    | Flows / Framing / Primitives                 | DONE        |
| 05    | Director / Video Planning                    | DONE        |
| 06    | Decision Engine                              | DONE        |
| 07    | Provider System / Router                     | DONE        |
| 08    | Model Manager / Resource Planner             | DONE        |
| 09    | Validation / Shot Intelligence               | DONE        |
| 10    | Preview / Checkpoint / Cache / Visual Review | DONE        |
| 11    | Audio / Timestamp / Temporal Intelligence    | DONE        |
| 12    | TUI Control Plane / Visual Review            | DONE        |
| 13    | Director Chat / ChangeSets / Visual Editing  | DONE        |
| 14    | Local AI Providers                           | DONE        |
| 15    | Cloud Providers                              | NOT STARTED |
| 16    | End-to-End Autonomous Pipeline               | NOT STARTED |
| 17    | Performance / Reliability / Packaging        | NOT STARTED |
| 18    | Public Repository / Release Readiness        | NOT STARTED |

## First milestones (PHASES.md section 29)

Phases 09 and 10 are complete locally with their required reviews and full gates.
Phase 10 provides a programmatic preview/cache/checkpoint/review boundary; its player
and scrubber UI remain intentionally deferred to Phase 12. No phase has been pushed.

1. Manual `VideoPlan` → 3 deterministic shots → deterministic renderer → MP4.
2. Prompt → Director → `VideoPlan` → deterministic renderer → MP4.
3. Prompt → Director → Shot Intelligence → Decision Engine →
   deterministic/AI provider → Validation → MP4.
4. TUI → Director Chat → Plan → Preview → Approve → Final.

Update this table whenever `PHASES.md` section 32 changes.
