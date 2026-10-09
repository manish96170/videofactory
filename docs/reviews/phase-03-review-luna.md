# Phase 03 Review — Reviewer 1 (luna / GPT-5.6 Luna)

**Phase:** 03 — Deterministic Renderer
**Reviewer:** luna
**Model:** `amazon-bedrock/openai.gpt-5.6-luna`
**Date:** 2026-10-09
**Commit reviewed:** pre-commit (staged working tree, Phase 03 implementation)
**Scope:** `render/*.go` (and `_test.go` files), `domain/shot.go`
(`PrimitiveTypeBackground`), `ARCHITECTURE.md`/`DECISIONS.md`
Phase 03 sections, `README.md`.

## Round 1 findings and resolutions

### HIGH
1. **FontPath filtergraph injection.** `escapeFilterPath` only escaped
   `\`, `:`, `'` — not `;`. The reviewer verified directly with a real
   `ffmpeg` command (a font path containing `;format=...` caused
   ffmpeg to interpret the suffix as a separate filter stage).
   **Resolution:** see "Deeper bug found during the fix" below — the
   final fix abandons escaping entirely in favor of controlled temp
   files.

### MEDIUM
1. **Non-finite bounds/Settings not rejected.** `validateBounds`
   accepted `NaN`/`Inf` (all comparisons with `NaN` are false);
   `Settings` had no validation for zero/negative dimensions/FPS.
   **Resolution:** `requireFinite` added and used in `validateBounds`
   and for `fontSize`; new `validateSettings` rejects non-positive
   `Width`/`Height`/`FPS`.
2. **Stable element identity not enforced.** `RenderShot` didn't verify
   `Primitive.ID` non-emptiness/uniqueness before reusing it as
   `VisualElement.ID`. **Resolution:** `validateShotPrimitiveIDs` added,
   called at the start of `buildFilterGraph`.
3. **Integration tests validated metadata, not pixels.** The ffmpeg
   tests checked process success/duration/manifests but never asserted
   pixel content; the "manually verified" claim in `ARCHITECTURE.md`
   wasn't reproducible CI evidence.
   **Resolution:** this is what surfaced the deeper bug below. Added
   `SamplePixel`/`RGB.CloseTo` (`probe.go`) and real pixel assertions
   in `render_test.go`.

### LOW
1. **`ComposeTimeline` didn't validate `End <= Start` for a single
   entry.** **Resolution:** added a per-entry check before the
   contiguity check.
2. **Graceful test skips could hide missing CI coverage.**
   Acknowledged as a documented follow-up (no CI infrastructure exists
   yet in this repo).

## Deeper bug found while fixing the HIGH finding

Building the pixel-sampling tests requested above (to actually verify
rendered content, not just file metadata) revealed that the escaping-
based fix for the FontPath injection — wrapping values in single quotes
with `\\`/`\:`/`'\''` for an embedded quote — **parsed without any
error but silently rendered completely empty text** for any string
containing an apostrophe. `TestRenderShotText` failed with "0
non-background pixels" even though `ffmpeg` exited `0`. The same
quote-trick also broke parsing of the *following* option when applied
to a font path containing special characters, differently from the
text case.

**Final fix:** abandon in-place escaping entirely. Text content is
written to a temp file and referenced via `drawtext`'s `textfile=`
option; `FontPath` is copied by content to a new temp file with a
Go-generated safe filename, referenced via `fontfile=`. Neither
original string is ever embedded in the `filter_complex` string again.
See `DECISIONS.md` ADR-016 (rewritten with the full history) and
`render/assets.go` (`writeTempTextFile`, `prepareSafeFontFile`,
`checkFilterGraphSafePath`).

## Final status (after confirmation round)

**READY FOR PHASE COMMIT.** Confirmed: text containing both `:` and
`'` renders correctly; `FontPath` injection is neutralized by
construction (the original string is never embedded). `go build/vet/
test/test -race` and `go test ./render/... -v` all pass.
