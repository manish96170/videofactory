# Phase 04 Review — Reviewer 1 (terra / GPT-5.6 Terra)

**Phase:** 04 — Flows / Framing / Primitives
**Reviewer:** terra
**Model:** `amazon-bedrock/openai.gpt-5.6-terra`
**Date:** 2026-10-09
**Commit reviewed:** pre-commit (staged working tree, Phase 04 implementation)
**Scope:** `registry/*.go` (and `_test.go` files), `domain/flow.go`
(extended), `domain/primitivecapability.go` (new), `ARCHITECTURE.md`/
`DECISIONS.md` Phase 04 sections.

## Findings and resolutions

### HIGH
1. **Registry entries are externally mutable; mutexes don't protect
   aliased slice contents.** `Register`/`Get`/`List` stored/returned
   values sharing backing arrays with caller-owned slices
   (`Flow.RecommendedCategories`, `Framing.Slots`,
   `PrimitiveCapabilities.RequiredProperties`/`Tags`). A caller
   mutating a `Get` result (or the value passed to `Register`, after
   the call) could silently corrupt the registry, racily — the mutex
   only ever protected the map.
   **Resolution:** `cloneFlow`/`cloneFraming`/
   `clonePrimitiveCapabilities` added; every `Register`/`Get`/`List`
   now defensively copies slice fields. Verified by deliberately
   reverting the `FlowRegistry` fix and confirming
   `TestFlowRegistryIsolatesSliceFieldsFromCallerMutation` failed with
   "mutating the slice passed to Register affected the stored entry,"
   then restoring it.

### MEDIUM
1. **Bounds validation accepted `NaN`/`Inf`.**
   **Resolution:** `validateSlotBounds` now checks
   `math.IsNaN`/`IsInf` for all four fields, matching
   `render/properties.go`. Test: `TestFramingRegistryRejectsNonFiniteBounds`.
2. **`RequiredProperties` contradicted the renderer's actual
   defaults** for `Background`'s `color` and `Transition`'s `style`.
   **Resolution:** removed from `RequiredProperties`; test rewritten
   to assert `Image`/`Text` require properties while
   `Background`/`Transition` explicitly don't (and document why).
3. **Composition "proof" tests weren't rigorous enough** — used
   `Background` (bounds-ignored by `render`) and only checked
   `"bounds"` key presence, not correctness; the custom-framing test
   only checked the first of three slots exactly.
   **Resolution:** both tests now use bounds-aware `Image` primitives
   and assert every slot's exact bounds, index-aligned against
   `framing.Slots`.

### LOW
1. **Doc comment typo** (`composition_test.go` → `compose_test.go`).
   **Resolution:** fixed.

## Final status

**READY FOR PHASE COMMIT.** `go build/vet/test/test -race` and
`go test ./registry/... -v` all pass; the fix for the HIGH finding was
verified by deliberately reintroducing and then re-fixing the bug.
