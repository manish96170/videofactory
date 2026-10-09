# Next Session Handoff

> **CHECKPOINT CLOSED (2026-10-09): PHASE 05 COMPLETE.** All Luna/Astra
> findings and the hint-slice ownership regression are fixed; both models
> approved the final code sequentially. Lead inspected diffs and reran the
> full uncached build/vet/test/race gate and real MP4 milestone (all PASS).
> **Original next-action lists below are superseded by the current
> `PHASES.md` tracker and `docs/phases/phase-05.md`.** No reimplementation
> or initial review is needed. Completion is recorded by the single local
> `phase(05): implement director / video planning` commit containing this
> notice; use repository history to resolve its hash. Phase 06 is next and
> remains unstarted. Keep the main session as a lean orchestrator with
> bounded implementation/documentation/review agents. No automatic push.

## Historical checkpoint — original handoff preserved below

## Current phase

Phase 05 — Director / Video Planning

## Current status

Implementation complete and manually/automatically verified end-to-end
(including a real rendered-and-probed MP4). **Not yet reviewed,
documented-as-done, or committed.** This is a mid-phase checkpoint, not
a context-exhaustion emergency stop — just handing off per user
request.

## Completed

- `project.ValidatePlan(projectID, plan)` exported from the `project`
  package (Phase 01) — a thin wrapper around the already-existing
  internal `validatePlan`, so a `VideoPlan` can be validated standalone
  before being attached to any `Project`. New test:
  `TestValidatePlanStandaloneMatchesEmbeddedValidation` in
  `project/validate_test.go`.
- New `director` package implementing the full Phase 05 TODO list:
  - `director.go` — `Director` interface, `PlanRequest`, `PlanResult`,
    `PlanMetadata`, `AssetRequirement`.
  - `interpreter.go` — `PromptInterpreter` (the optional LLM adapter
    seam), `PromptInterpretation`, `ShotHint`.
  - `decompose.go` — deterministic, AI-free heuristics: sentence
    splitting, avatar-keyword regex (`\b` word boundaries), category
    inference, reading-pace duration estimation (2.5 words/sec, 1.5s
    floor).
  - `fixture.go` — `FixtureDirector` (the only `Director`
    implementation): flow selection → framing selection → shot
    decomposition → deterministic-vs-AI primitive selection → asset
    requirements → capability aggregation (reusing Phase 04's
    `PrimitiveRegistry.Tags`) → `project.ValidatePlan` before
    returning. Optional `WithPromptInterpreter` option.
- Tests: `decompose_test.go`, `fixture_test.go` (unit + a fake
  `PromptInterpreter`), `second_milestone_test.go`.
- **PHASES.md section 29 Second Milestone verified for real:**
  `TestSecondMilestonePromptToRenderedMP4` runs
  `FixtureDirector.Plan` → `render.BuildSequentialTimeline` →
  `render.FFmpegRenderer.RenderShot` (Phase 03) →
  `render.ComposeTimeline` → `render.Probe`, producing and verifying an
  actual MP4. A companion test,
  `TestSecondMilestoneRejectsNonDeterministicPlanAtRenderTime`,
  confirms a Director-planned `DeveloperAvatar` shot correctly *fails*
  to render (no AI provider exists yet — expected, not a bug).
  **Also manually verified visually**: rendered the pipeline via a
  throwaway `/tmp` script, extracted frames with `ffmpeg`, and visually
  confirmed each sentence became its own correctly-rendered text shot,
  and the composed final video starts with shot 1. (Throwaway script
  and its output were deleted after inspection — nothing left in
  `/tmp`.)
- `ARCHITECTURE.md` — added a "Phase 05" section (package layout,
  pipeline walkthrough, the `PromptInterpreter` seam, the Second
  Milestone test, known limitations).
- `DECISIONS.md` — added ADR-018 documenting the heuristic-based
  Director design and the `PromptInterpreter`/capability-tag reuse
  decisions.
- Full gate passing: `go build ./...`, `go vet ./...`, `go test ./...`,
  `go test -race ./...`, `gofmt -l .` (clean). Confirmed again right
  before this handoff.

## Incomplete (exact next steps, in order)

1. **Self-review the diff** (`git add -A && git diff --cached`) for
   anything that slipped through — I had just done this once already
   right before the handoff request; redo it fresh in the new session
   since nothing is staged yet (see "Working tree" below).
2. **Route to two independent reviewers** (per `PHASES.md` §12.1
   rotation — pick two agents/models not yet paired together; check
   `~/.config/opencode/opencode.json`'s `agent` section for the
   `terra`/`sol`/`luna`/`astra`/`luna6`/`sol6`/`gpt-oss-*` roster and
   verify each maps to a distinct underlying model before assigning,
   per §12.2). Prior phases used: terra+astra (P00), sol+luna6 (P01),
   astra+gpt-oss-120b (P02), luna+gpt-oss-20b (P03), terra+sol6 (P04).
   Pick a fresh pairing for P05 (e.g. `luna`+`astra`, or `sol`+
   `gpt-oss-120b` — anything not yet combined). **Known gotcha:**
   calling `sol6`'s underlying model directly requires the model string
   `amazon-bedrock/global.openai.gpt-6-sol` (with the `global.` prefix)
   — using `amazon-bedrock/openai.gpt-6-sol` fails with an HTTP 404.
3. **Fix whatever findings are valid** — expect genuine findings; every
   prior phase's review surfaced at least one real bug (Phase 00:
   duration/timing docs; Phase 01: ULID encoding + CSPRNG fallback;
   Phase 02: `t.Fatalf`-from-goroutine + weak ordering test; Phase 03:
   FontPath filtergraph injection + silently-empty-text bug; Phase 04:
   slice-aliasing defeating the registries' mutex). Don't rubber-stamp;
   verify fixes actually work (prefer: deliberately reintroduce a bug
   and watch the regression test catch it, then restore the fix — this
   caught real issues in Phases 01-04).
4. **Re-test, get reviewer confirmation**, write
   `docs/reviews/phase-05-review-<reviewer1>.md` and
   `docs/reviews/phase-05-review-<reviewer2>.md`.
5. **Write `docs/phases/phase-05.md`** evidence file (see the Phase
   00-04 files in that directory for the expected shape/sections).
6. **Update `PHASES.md`**: check off every Phase 05 TODO item (lines
   ~1518-1539), flip `[ ] PHASE 05` to `[x]` in section 32's execution
   state tracker (currently at line ~2887).
7. **Update `ROADMAP.md`**: Phase 05 row `NOT STARTED` → `DONE`.
8. **Update `README.md`**: status line currently says "Phase 04 (Flows
   / Framing / Primitives)" — bump to "Phase 05 (Director / Video
   Planning)".
9. **Final gate re-run** (`go build/vet/test/test -race`, `gofmt -l .`,
   `git grep` secret/absolute-path hygiene check — see any prior phase
   commit's pre-commit checklist for the exact command).
10. **Commit** as one phase commit:
    `phase(05): implement director / video planning`
    (see Phase 00-04 commit messages for the expected style/detail
    level — summarize what was built, what review found/fixed, and
    confirm the gate passed).
11. Only then start Phase 06 (Decision Engine) if continuing in the
    same session.

## Current commit

`65a5441` ("phase(04): implement flow/framing/primitive registries") —
Phase 05's work is **not yet committed**, all on top of this as
uncommitted changes.

## Working tree

**Dirty**, nothing staged (plain `git status`, not `--cached`):

```text
 M ARCHITECTURE.md
 M DECISIONS.md
 M project/validate.go
 M project/validate_test.go
?? director/decompose.go
?? director/decompose_test.go
?? director/director.go
?? director/fixture.go
?? director/fixture_test.go
?? director/interpreter.go
?? director/second_milestone_test.go
```

Run `git add -A` before reviewing/committing, as always.

## Tests

All passing as of this handoff:

```text
go build ./...        PASS
go vet ./...            PASS
go test ./...           PASS (all packages, including new director)
go test -race ./...     PASS (all packages)
gofmt -l .               (clean, no output)
```

`director`'s `ffmpeg`-dependent tests
(`TestSecondMilestonePromptToRenderedMP4`,
`TestSecondMilestoneRejectsNonDeterministicPlanAtRenderTime`) ran for
real on this machine (`ffmpeg-full` is installed, see `README.md`) and
passed, including the pixel/duration/resolution assertions in
`render.Probe`.

## Reviews

### Reviewer 1

- model: **not yet assigned**
- status: not started
- findings: n/a

### Reviewer 2

- model: **not yet assigned**
- status: not started
- findings: n/a

## Documentation updated

- `ARCHITECTURE.md` — Phase 05 section added (uncommitted).
- `DECISIONS.md` — ADR-018 added (uncommitted).
- `PHASES.md` — **not yet updated** (Phase 05 TODOs still `[ ]`,
  section 32 still shows Phase 05 unchecked).
- `ROADMAP.md` — **not yet updated** (still shows Phase 05 as `NOT
  STARTED`).
- `README.md` — **not yet updated** (still says Phase 04 in the status
  line).
- `docs/phases/phase-05.md` — **not yet written**.
- `docs/reviews/phase-05-review-*.md` — **not yet written**.

## Known issues

None outstanding. Everything implemented so far builds, vets, and
passes tests, including a real end-to-end render. The only "issue" is
procedural: review/evidence/doc-sync/commit haven't happened yet.

## Exact next actions

1. `cd` into the repo, run `git status` and the full gate to confirm
   nothing drifted since this handoff.
2. `git add -A` and self-review the diff.
3. Dispatch two independent reviewers per the rotation note above.
4. Follow steps 3-11 under "Incomplete" above, in order.

## Do not redo

- Do not reimplement any part of the `director` package from scratch —
  it works, is tested, and has been verified end-to-end including a
  real rendered MP4. Only fix concrete reviewer findings.
- Do not re-derive the `project.ValidatePlan` export — it's a 3-line
  wrapper around existing logic, already tested.
- Do not re-run the manual `/tmp` visual-inspection smoke test unless
  something about rendering actually changes — it was for one-time
  human confirmation during development, not a permanent artifact (and
  nothing was left behind in `/tmp`).

## Next-session prompt

Continue Phase 05 from this handoff.

First:
1. Read `PHASES.md` (it is the execution authority — see `AGENTS.md`).
2. Read this handoff (`docs/handoffs/next-session-phase-05.md`).
3. Run `git status` and the full gate (`go build/vet/test/test -race`,
   `gofmt -l .`) to verify the current implementation before changing
   anything.
4. Continue only from the "Incomplete" steps above (self-review → two
   independent reviews → fix findings → evidence → docs sync →
   commit). Do not redo completed work.
5. Preserve the two-review requirement with genuinely different
   models (verify via `~/.config/opencode/opencode.json`'s `agent`
   section), and don't rubber-stamp reviews — every prior phase's
   review found at least one real, fixable issue.
6. Complete the Phase 05 completion gate (`PHASES.md` section 14) and
   commit the phase as `phase(05): implement director / video
   planning`.
7. Only then consider starting Phase 06 (Decision Engine).
