# Phase 03 — Deterministic Renderer

## Goal

Produce a real MP4 from deterministic primitives — the first major
milestone (PHASES.md "PHASE 03 — DETERMINISTIC RENDERER").

## Implemented

- **`domain.PrimitiveTypeBackground`** added (open vocabulary addition,
  not in PHASES.md section 6's list but required by this phase's TODO).
- **`render` package:**
  - `Renderer`/`FFmpegRenderer.RenderShot` — renders one `Shot`'s
    `Primitives` into an MP4 + sidecar manifest.
  - `buildFilterGraph` — composes Background (base layer, 0 or 1),
    Image/Text (bottom-to-top, any count), Transition (whole-shot
    fade, 0 or 1) into one ffmpeg filter graph; rejects any other
    `PrimitiveType` explicitly.
  - Text content and the font file are **never embedded as strings**
    into the filtergraph: text goes through `textfile=` (a temp file),
    and the font is copied to a Go-generated safe-named temp file
    referenced via `fontfile=`. See ADR-016 for why (a real,
    pixel-verified bug in the escaping-based predecessor).
  - `Probe`/`ValidateOutput` — ffprobe-based metadata validation.
  - `SamplePixel`/`RGB.CloseTo` — real pixel-level test verification.
  - `buildManifest` — one `VisualElement` per `Primitive`, reusing the
    Primitive's own ID directly (stable identity).
  - `BuildSequentialTimeline`/`ComposeTimeline` — default sequential
    shot placement + concat-demuxer composition into one final video.
  - `validateSettings`/`validateShotPrimitiveIDs`/`validateBounds`
    (NaN/Inf-safe) — structural guards before any ffmpeg invocation.

## Design decisions

See `DECISIONS.md` ADR-015/ADR-016:
- ADR-015: `os/exec`-based ffmpeg/ffprobe integration, not cgo/libav.
- ADR-016: text/font-path handling — the full history of three
  attempts (naive escaping → quote-trick escaping → controlled temp
  files), including a real bug (quote-trick parsed fine but rendered
  empty text) found via review-prompted pixel-level testing.

## Files changed

New: `render/render.go`, `render/ffmpeg.go`, `render/filtergraph.go`,
`render/properties.go`, `render/assets.go`, `render/probe.go`,
`render/manifest.go`, `render/timeline.go`, and their `_test.go` files
plus `render/testhelpers_test.go`. Modified: `domain/shot.go`,
`ARCHITECTURE.md`, `DECISIONS.md`, `README.md`, `PHASES.md`,
`ROADMAP.md`.

## Tests

```text
go build ./...        PASS
go vet ./...           PASS
go test ./...          PASS (all packages)
go test -race ./...    PASS (all packages)
gofmt -l .              (clean, no output)
```

`render` package specifically: unit tests for every property/filter-
graph helper (no ffmpeg needed); real integration tests (skip
gracefully via `requireFFmpeg`/`requireDrawtext`/`requireFontPath` if
unavailable) for Background/Text/Image/Transition shots, each asserting
**actual rendered pixel colors** (not just ffprobe metadata); a
dedicated `TestFontPathInjectionIsNeutralized` regression test; manifest
marshal/unmarshal round trips; timeline construction/composition
including contiguity and interval validation; and
`TestEndToEndThreeShotMilestone` — PHASES.md section 29's First
Milestone, literally: a manual 3-shot `VideoPlan` → rendered → composed
→ probed MP4, with no AI involved.

**Manual verification beyond the automated suite:** rendered output was
visually inspected during development by extracting frames and viewing
them directly (not just asserting via code) — confirmed centered white
text on a colored background, and confirmed the fix for ADR-016's bug
by deliberately reverting it and watching text disappear, then
restoring it and watching text reappear.

**ffmpeg environment note:** this machine's default Homebrew `ffmpeg`
lacked `drawtext` (no `libfreetype`); `ffmpeg-full` was installed
instead (`brew install ffmpeg-full && brew link --force ffmpeg-full`)
specifically so Text-primitive tests could run for real, not just be
skipped. See `README.md`.

## Review 1

Reviewer: luna (`amazon-bedrock/openai.gpt-5.6-luna`). Found a real,
empirically-verified `FontPath` filtergraph-injection HIGH finding
(reproduced directly against real `ffmpeg`), plus several MEDIUM/LOW
issues. See `docs/reviews/phase-03-review-luna.md`. Final verdict:
**READY FOR PHASE COMMIT**.

## Review 2

Reviewer: gpt-oss-20b (`amazon-bedrock/openai.gpt-oss-20b-1:0`) —
substituted after `sol6` failed with a provider error. Found
overlapping MEDIUM/LOW issues independently. See
`docs/reviews/phase-03-review-gpt-oss-20b.md`. Final verdict: **READY
FOR PHASE COMMIT**.

## Findings resolved

Every finding from both reviewers resolved. Notably, fixing the
reviewer-found `FontPath` injection issue led directly to discovering a
*more serious*, independent correctness bug (silently empty rendered
text) that neither reviewer's static reading had caught, via the
pixel-level tests they both asked for. Both reviewers then confirmed
the final (non-escaping-based) fix.

## Known limitations

- No true cross-fade between two shots' pixels (a Transition primitive
  is a whole-shot fade-to/from-black). Needs `xfade` + overlapping clip
  ranges; deferred.
- `ComposeTimeline` requires contiguous timeline entries; no
  gap-filling (black filler) yet.
- Every `VisualElement` spans its whole shot; no partial-shot timing.
- No audio track (Phase 11 scope).
- No CI pipeline exists yet to run the `ffmpeg`-dependent tests on
  every change automatically; they currently only run where `ffmpeg`
  (ideally `ffmpeg-full`, for `drawtext`) happens to be installed, with
  graceful skips otherwise. Worth revisiting in Phase 17/18.

## Commit

See the phase commit immediately following this evidence file in the
repository history (`phase(03): implement deterministic renderer`).

## Follow-ups

- Consider a CI job with `ffmpeg-full` installed so the render
  integration tests run on every change, not just locally.
- Cross-fade transitions, gap-filling, and partial-shot element timing
  are natural next refinements once a later phase needs them.
