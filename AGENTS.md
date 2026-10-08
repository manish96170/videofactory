# AGENTS.md

## Execution Authority

`PHASES.md` is the **execution authority** for this project. It is not background reading — it is the operating contract that governs how work happens here.

Any agent (main/orchestrator session or delegated sub-agent) working in this repository must:

1. Read `PHASES.md` in full before writing or changing any code.
2. Treat its rules, phase definitions, checklists, and templates as binding, not advisory.
3. Resolve any conflict between this file, other documentation, prior conversation, or general conventions in favor of `PHASES.md`.
4. Keep `PHASES.md` up to date as work progresses — it is the source of truth for what is done and what is next.
5. Never skip, reorder, or short-circuit the process defined in `PHASES.md` without explicit user approval.

### What specifically is binding

In particular, defer to `PHASES.md` for:

- **Non-Negotiable Rules** (§0) — orchestrator responsibilities, commit discipline, no auto-push/amend/force-push, public-repo safety.
- **Phase Roadmap** (§20) and each phase's own goal/TODO/non-goals/required-result sections — do not implement out of order or add unscoped work.
- **Review Workflow and Severity Policy** (§12–13) — agent rotation, two-reviewer minimum, BLOCKER/HIGH/MEDIUM/LOW handling.
- **Phase Completion Gate** (§14) — a phase is `DONE` only when every gate item is satisfied, not when code merely exists.
- **Context Management / Handoff** (§15, §26) — checkpoint and hand off before context runs out, using the specified `docs/handoffs/` format.
- **Main Session Agent Protocol** (§23) — the Understand → Inspect → Plan → Implement → Review → Fix → Document → Commit sequence.
- **TODO State Rules** (§27) — `[ ]` / `[-]` / `[x]` / `[!]` semantics; never mark `[x]` without verification.
- **Current Execution State** (§32) — the authoritative phase-progress tracker; keep it accurate.
- **The Golden Rule** (§35) — optimize for correct architecture and reviewed, deterministic, resumable work, not for volume of output.

### If `PHASES.md` is silent or ambiguous

Ask the user rather than guessing, and once resolved, record the decision back into `PHASES.md` (and `DECISIONS.md` where appropriate) so the execution authority stays current and self-contained.
