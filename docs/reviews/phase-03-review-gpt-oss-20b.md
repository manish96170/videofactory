# Phase 03 Review — Reviewer 2 (gpt-oss-20b)

**Phase:** 03 — Deterministic Renderer
**Reviewer:** gpt-oss-20b
**Model:** `amazon-bedrock/openai.gpt-oss-20b-1:0`
**Date:** 2026-10-09
**Commit reviewed:** pre-commit (staged working tree, Phase 03 implementation)
**Scope:** `render/*.go` (and `_test.go` files), `domain/shot.go`
(`PrimitiveTypeBackground`), `ARCHITECTURE.md`/`DECISIONS.md`
Phase 03 sections, `README.md`.

(Note: an initial attempt at a second reviewer, `sol6`, failed with a
provider error (HTTP 404) due to a model-variant mismatch; `gpt-oss-20b`
was substituted.)

## Round 1 findings and resolutions

### LOW (no BLOCKER/HIGH/MEDIUM reported by this reviewer)
1. **`ComposeTimeline` doesn't reject `End <= Start` for a single
   entry.** **Resolution:** added a per-entry interval check.
2. **`roundToInt` doesn't guard against `NaN`.** **Resolution:**
   `requireFinite` added; `validateBounds` now rejects `NaN`/`Inf` for
   all four fields before reaching `roundToInt`.
3. **`FontPath` could contain spaces; `escapeFilterPath` incomplete.**
   **Resolution:** superseded by the full fix below — `FontPath` is no
   longer embedded as a string at all, so this (and the related `;`
   injection the other reviewer found) is moot.
4. **Primitive ID uniqueness not enforced** (raised as an architecture
   concern alongside a suggestion for deterministic manifest IDs in
   tests). **Resolution:** `validateShotPrimitiveIDs` added.

## Deeper bug found while fixing the FontPath finding

While addressing the FontPath escaping gap (found independently by the
other reviewer, luna), building real pixel-sampling tests revealed the
escaping-based fix parsed without error but silently rendered
completely empty text for any string containing an apostrophe. See
`docs/reviews/phase-03-review-luna.md` and `DECISIONS.md` ADR-016 for
the full account. Final fix: `textfile=` for text content, a
Go-generated safe-named copy for the font file — neither original
string is ever embedded in the filtergraph string.

## Final status (after confirmation round)

Asked specifically to verify the fix handles text containing both `:`
and `'` together, and that the original FontPath injection (`;`, `[`,
`]`) is genuinely neutralized, not just moved. Response: **"READY FOR
PHASE COMMIT... the new `textfile=` / safe-font-copy approach correctly
handles text containing both a colon and an apostrophe as well as
neutralizes FontPath injection when the path contains `;`, `[`, or
`]`. No further issues remain."** `go build/vet/test/test -race` and
`go test ./render/... -v` all confirmed passing.
