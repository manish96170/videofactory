# Phase 05 Review — Reviewer 2 (astra / GPT-6 Astra)

**Phase:** 05 — Director / Video Planning
**Reviewer:** astra (independent, read-only agent)
**Model:** `amazon-bedrock/us.openai.gpt-6-astra`
**Date:** 2026-10-09
**Commit reviewed:** staged Phase 05 diff above `65a5441`, after Luna
findings were fixed and the full uncached gate passed. Subsequent
unstaged changes at review time were review/tracker documentation only.
**Scope:** full phase implementation, tests, and architecture/decisions;
sequential independent review of the updated state, not the initial diff.

## Findings and resolutions

### BLOCKER / HIGH

None.

### MEDIUM

1. **A1 — Standalone validation accepts missing project linkage.**
   `project/validate.go`'s exported wrapper delegates to the internal
   validator, which skips identity comparison when the expected project
   ID is empty. An otherwise valid plan with empty `ProjectID` passes
   `ValidatePlan("", plan)`, but cannot belong to a valid nonempty-ID
   project. Aggregate validation previously protected this internal use;
   the new public wrapper needs its own empty-ID guard. The Director's
   request check already protects its normal path.
   **Resolution:** exported wrapper now documents and rejects an empty
   expected project ID. Five cases cover matching/mismatched linkage,
   empty expected ID with linked plan, both IDs empty, and missing plan
   linkage. The implementation agent observed the two empty-expected-ID
   cases fail before the guard, then all cases pass afterward.

### LOW

1. **A2 — Determinism and capability tests omit claimed guarantees.**
   `director/fixture_test.go`'s repeated-call assertion omitted primitive
   properties and requirements; converting mixed capability output to a
   map concealed duplicate/order defects.
   **Resolution:** repeated-call tests compare full normalized results,
   excluding generated IDs/timestamps but preserving content, properties,
   metadata, requirements, ordering, and reference relationships. Seventeen
   sensitivity cases prove substantive differences remain visible.
   Mixed capabilities are compared against the exact sorted, deduplicated
   slice, including repeated deterministic primitives.

Additional missing tests: runtime custom flows/framings (including absent
and dangling defaults), request override precedence over interpreter
suggestions, planning JSON round-trip, and concurrent planning with a
safe interpreter. Assigned as bounded Phase 05 test coverage, not new
production capabilities. All these coverage additions were implemented
and verified, including 24 concurrent calls with a stateless interpreter.

## Positive observations

Luna fixes correctly integrated; interpreter ownership preserved;
registry-based selection and no production rendering/native/network/
vendor coupling; consistent avatar intent and metadata; serializable
planning data; no new goroutines/leases; no observed public-repository
secrets or machine-specific paths. Fixture sentence/keyword rules,
uncapped durations, and Background+Text for non-avatar categories were
accepted limitations, not additional defects.

## Tests

Reviewer ran `go test -count=1 -v ./director ./project`,
`go test -race -count=1 ./director ./project ./registry`, and
`git diff --cached --check`: all PASS. Real prompt-to-MP4 milestone
executed (not skipped); avatar rejection also passed with empty PATH.
Full-repository build/vet/uncached test/race results were supplied by
the lead, not independently rerun by this reviewer.

After fixes, the lead independently reran the full build/vet/uncached
test/race gate (PASS), then obtained Luna final confirmation followed by
Astra final confirmation. Astra reran uncached verbose director/project
tests, director/project race tests, `go vet ./director ./project`, and
`git diff --cached --check`: all PASS. The real milestone executed again
(not skipped), and tool-independent avatar rejection passed.

## Final status

Initial verdict: **REQUEST CHANGES**. Final confirmation verdict:
**APPROVE — A1/A2 RESOLVED**. No remaining BLOCKER/HIGH/MEDIUM/LOW
findings. No source/test files were modified by the reviewer. Accepted
limitations: fixture heuristics, uncapped sentence duration, deferred AI
rendering; concurrent use requires a concurrency-safe interpreter.
