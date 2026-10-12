# Video Factory — Master Multi-Agent Implementation Plan

> **Purpose:** This document is the single operating contract for implementing the Video Factory repository from an empty/new codebase into a production-oriented deterministic video factory with pluggable AI providers.
>
> **Execution model:** A main OpenCode session acts as the **orchestrator/lead engineer**. It delegates implementation and review work to specialized agents/models (`terra`, `sol`, `luna`, etc.), integrates their work, runs tests, updates documentation, and commits each completed phase.
>
> **Important:** This is an implementation plan, not merely an architecture document. Agents must execute the TODOs, maintain evidence, update the checklist, and leave the repository in a resumable state.

---

# 0. Non-Negotiable Rules

## 0.1 Main session is the orchestrator

The main session must:

1. Read this file completely before changing code.
2. Inspect the current repository before making assumptions.
3. Maintain the phase TODO/checklist in this file.
4. Delegate meaningful implementation/review tasks to agents.
5. Never blindly accept an agent's output.
6. Inspect diffs and test results itself.
7. Integrate valid review findings.
8. Run the required tests before marking a phase complete.
9. Update architecture/decision/docs when implementation changes the documented design.
10. Commit **one commit per completed phase**.
11. Never auto-push unless explicitly requested.
12. Never rewrite published history.
13. Never use `git commit --amend` for completed phase commits unless fixing an immediately-created local mistake before the phase is considered complete.
14. Preserve a clean, understandable commit history.

The repository owner/target GitHub account is:

```text
github.com/manish96170
```

The intended repository is a **public repository**.

The owner explicitly approved `Manish Sharma <manish9617026809@gmail.com>` as this
repository's public Git author/committer identity on 2026-10-12. This is the narrow
exception to the generic public-hygiene rule for commit metadata.

Do not expose secrets, API keys, credentials, personal tokens, local absolute paths, or private infrastructure configuration.

## 0.2 Continuing orchestration authority (2026-10-09)

The owner authorized this main session to continue the remaining phases in
roadmap order, rather than waiting for a new-session prompt after each commit.
The main session remains the lead/orchestrator and delegates bounded
implementation, documentation, and review tasks with explicit file ownership.
Each phase still requires the full verification, sequential independent review,
evidence, documentation, and single local commit gate before the next begins.
This does not authorize skipped gates, automatic push/publication, unapproved
licensing choices, paid generation, or large model downloads. The owner separately
delegated routine phase-scoped design gaps to the lead: choose conservative,
deterministic defaults, document them here/in ADRs, and preserve review gates.
Conflicting product goals, scope changes, security tradeoffs, paid operations,
large downloads, licensing, and publication still require owner approval.
The owner explicitly authorized reviewer rotation among terra, luna6, luna,
and astra; verify their actual model IDs and use two distinct models
sequentially per phase, different from implementation. Planned rotation:
Phase 07 luna6 → astra; Phase 08 terra → luna; Phase 09 astra → terra;
Phase 10 luna → luna6; repeat/adapt this verified roster for subsequent phases.
Keep resumable checkpoints under §15/§26
if context needs rollover; do not silently abandon the current phase or require
the owner to reissue routine next-phase instructions.

---

# 1. Product Definition

The product is a **deterministic video factory / video compiler with AI shot generators**.

It is NOT primarily an AI video model.

The system should combine:

```text
Prompt
  ↓
Director
  ↓
Video Plan
  ↓
Shot Planner
  ↓
Shot Intelligence
  ↓
Decision Engine
  ↓
Provider Router
  ↓
Resource Planner
  ↓
Execution
  ↓
Validation
  ↓
Timeline
  ↓
Final Render
```

The system must prefer deterministic generation whenever possible.

AI should be used where AI provides genuine value:

- realistic human motion
- realistic environments
- cinematic camera movement
- avatar animation
- image/video generation
- semantic decisions
- speech recognition
- speech synthesis
- visual understanding

Exact content such as code, UI, terminal output, charts, captions, subtitles, layout, timing, and other structured visuals should normally be deterministic.

Core principle:

> **LLMs propose. Decision models classify. Deterministic code decides.**

---

# 2. Target Architecture

## 2.1 Core language strategy

### Go is the core language.

Use Go for:

- project state
- planning
- Director orchestration
- Decision Engine
- provider registry/router
- resource planner
- model manager
- scheduler
- event system
- workers/orchestration
- cache
- validation orchestration
- timeline orchestration
- TUI
- future API/control plane
- AWS/Lambda-compatible control-plane components

### Python is a worker language.

Use Python only where ML ecosystems require it:

- PyTorch
- MLX where appropriate
- Whisper/faster-whisper
- Qwen ASR
- InvokeAI
- FreeVideo
- Wan
- other Python-native ML providers

Do **not** embed Python into Go initially.

Use process boundaries:

```text
Go Core
  ├── starts worker process
  ├── sends structured request
  ├── receives structured result
  ├── monitors stdout/stderr
  ├── monitors resource usage
  ├── cancels process when needed
  └── unloads/stops worker
```

Possible protocols:

- JSON over stdin/stdout
- localhost HTTP
- Unix domain sockets

Choose the simplest protocol for each worker.

### TypeScript/JavaScript

Use TypeScript/JavaScript only where appropriate:

- Remotion
- React
- browser/HTML/SVG rendering
- Playwright/browser automation

### Native tools

Use native executables where appropriate:

- FFmpeg
- FFprobe
- OpenCV
- other mature native utilities

### Rust

Do not introduce Rust in the initial implementation merely for theoretical performance.

Consider Rust later only if profiling proves a specific component is CPU/memory-bound and Go is insufficient.

---

# 3. Hardware / Resource Target

Primary development target:

```text
Apple Silicon Mac
M1 Pro
16 GB unified memory
No discrete GPU
```

The architecture must not assume that a powerful GPU exists.

The system must support:

```text
small local model
→ heavy model loaded
→ generate
→ unload
→ another heavy model loaded
→ generate
→ unload
```

Resource management is a first-class architectural concern.

Example:

```text
Machine memory: 16 GB

OS/system reserve:       3 GB
User reserve:             4 GB
Factory budget:           9 GB

Director:                 1.5 GB
Worker available:         7.5 GB

Model A requires 6 GB
→ ALLOW

Model B requires 5 GB concurrently
→ WAIT / UNLOAD / REMOTE
```

The LLM must never be the authority enforcing resource limits.

The **Resource Manager** must enforce them.

---

# 4. Architectural Principles

Agents must preserve these principles throughout all phases.

## 4.1 Provider independence

Core must not depend directly on:

- InvokeAI
- FreeVideo
- OpenMotion
- Wan
- Veo
- Runway
- Whisper
- Qwen ASR
- a particular LLM
- a particular API vendor

Providers implement interfaces/contracts.

## 4.2 Model independence

Do not hard-code business logic around model names.

Bad:

```text
if model == "qwen3":
    ...
```

Good:

```text
if provider.Capabilities().Supports("semantic-decision"):
    ...
```

Model selection should come from:

```text
ProviderRegistry
ProviderCapabilities
DecisionPolicy
ResourcePlanner
ProviderRouter
```

## 4.3 Deterministic first

For every shot ask:

```text
Can this be deterministic?
Can an existing asset be reused?
Can an image be used instead of video?
Can an existing image be animated?
Can a cheap local model solve it?
Does it really need an expensive cloud model?
```

## 4.4 Small shots

Target shot granularity:

```text
~2–3 seconds
```

Not every shot must be exactly 2–3 seconds, but short shots should be the default design assumption.

Benefits:

- easier retries
- smaller model context
- cheaper generation
- local feasibility
- easier validation
- provider routing
- deterministic composition
- incremental rebuild

## 4.5 Preview first

Generation should support:

```text
draft/preview
→ validate
→ inspect
→ approve
→ final quality
```

Do not waste expensive compute before validating the concept.

## 4.6 Incremental rebuild

Changing Shot 17 must not regenerate Shots 1–16.

Use hashes/cache/checkpoints and dependency-aware invalidation.

## 4.6.1 Visual addressability

Rendered content must be addressable without relying on computer vision or LLM guessing when the renderer already knows the element identity.

The canonical relationship is:

```text
Project
  ↓
Shot
  ↓
VisualElement
  ↓
Timeline / property / timestamp
```

A visual element should have a stable ID and enough metadata for a preview surface to map a user interaction back to the authoritative project state.

Example:

```text
elementId
shotId
start
end
type
bounds (when applicable)
editableProperties
```

The rendered MP4 is a preview artifact, not the source of truth.

## 4.6.2 Visual review loop

The system should support this loop without creating a second editor architecture:

```text
Render
→ Preview
→ User selects element / timestamp
→ ReviewFeedback
→ Director / Intent
→ ChangeSet
→ Validate
→ Apply
→ Affected-shot calculation
→ Cache invalidation
→ Incremental rebuild
→ Preview
```

The same core contracts must support both a richer local web preview and the TUI control plane.

## 4.7 Explicit lifecycle

Every heavy resource should have a clear lifecycle:

```text
acquire
→ load
→ use
→ validate
→ release
→ unload
```

No hidden global model state.

## 4.8 Cancellation

Every long-running operation should support cancellation using:

```go
context.Context
```

Cancellation must propagate through:

```text
TUI
→ scheduler
→ worker
→ provider
→ process
```

## 4.9 No unnecessary dependencies

Prefer Go standard library for the core.

Add dependencies only when they provide substantial value.

Avoid framework-heavy architecture in Phase 0.

## 4.10 Lambda awareness

Core packages should avoid assumptions that prevent future Lambda/control-plane execution:

- no required long-lived global process
- no mandatory local filesystem state
- no hard-coded daemon assumptions
- no reliance on environment-specific paths
- no uncontrolled goroutines
- explicit configuration
- explicit lifecycle
- serializable requests/results

Heavy video/ML inference does NOT belong in Lambda.

Future architecture may use:

```text
Video Factory
    ↓
Execution Planner
    ├── local Mac
    ├── CPU worker
    ├── GPU EC2
    ├── ECS
    ├── AWS Batch
    ├── Lambda control-plane task
    └── external cloud provider
```

---

# 5. Core Domain Model

The system should eventually contain at least:

```text
Project
VideoPlan
Shot
Timeline
Asset
Primitive
Flow
Framing
DecisionRequest
DecisionResult
DecisionProvider
ProviderCapabilities
GenerationRequest
GenerationResult
ValidationResult
ResourcePlan
ModelCapabilities
VisualElement
RenderManifest
ReviewFeedback
ChangeSet
```

Do not add fields merely because they sound useful.

Every field should have a clear purpose.

### VisualElement

A `VisualElement` represents a stable, inspectable element produced by the timeline/renderer.

Conceptually:

```text
VisualElement
├── elementId
├── shotId
├── type
├── start
├── end
├── bounds (optional)
└── editableProperties
```

It is metadata about the rendered representation, not a second source of truth.

### RenderManifest

The renderer may emit a sidecar manifest mapping rendered elements to authoritative project/timeline entities.

Example:

```text
shot-017.mp4
shot-017.json
shot-017.render.json
```

The exact storage format may change, but the contract should allow preview clients to resolve:

```text
timestamp + selected element
→ VisualElement
→ project/shot/timeline target
```

### ReviewFeedback

User feedback captured from a preview surface.

It should support at least:

```text
project
shot
element
timestamp
optional duration
feedback type
natural-language note
createdAt
```

### ChangeSet

A validated, reviewable set of mutations to authoritative project state.

Visual feedback must eventually become a normal `ChangeSet`; do not create a separate visual-edit mutation system.

---

# 6. Shot Model

Recommended conceptual shot categories:

```text
UI
CODE
TERMINAL
CHART
AVATAR
TRANSITION
ENVIRONMENT
HUMAN_ACTION
CINEMATIC
```

Typical deterministic primitives:

```text
CodeEditor
Terminal
Browser
Chart
Text
Subtitle
Cursor
Image
Transition
```

AI-capable primitives:

```text
DeveloperAvatar
HumanAction
Environment
CinematicCamera
AnimatedImage
```

---

# 7. Flow vs Framing

These concepts must remain separate.

## Flow

Defines what type of video is being produced:

```text
developer-explainer
cinematic-product-demo
code-tutorial
talking-head
social-video
```

## Framing

Defines layout/composition:

```text
developer-circle
fullscreen
split-screen
picture-in-picture
screen-focused
```

`developer-circle` must NOT become a hard-coded global architecture.

It is simply one reusable framing strategy.

---

# 8. Decision Engine

Decision models should return structured classifications instead of prose.

Example:

```json
{
  "decision": "yes",
  "score": 0.94,
  "reasonCode": "REALISTIC_HUMAN_MOTION"
}
```

Preferred decisions:

```text
YES
NO
UNCERTAIN
```

Example policy:

```text
score >= 0.85 → YES
score <= 0.30 → NO
otherwise     → UNCERTAIN
```

The exact threshold must be configurable.

Possible providers:

```text
JEV
Qwen local
Gemma local
Claude Haiku
Gemini Flash
future providers
```

Example configuration:

```yaml
decisions:
  deterministic_possible:
    provider: qwen-local
    model: qwen3-small

  visual_classification:
    provider: gemma-local

  semantic_question:
    provider: claude-haiku

  form_action:
    provider: jev
    model: jev-v2
```

Do not hard-code these providers into core business logic.

---

# 9. Resource Management

The system must eventually expose a resource budget similar to:

```yaml
resources:
  totalMemoryGb: 16
  reservedGb: 2
  director:
    maxMemoryGb: 2
  workers:
    maxTotalMemoryGb: 10
  systemReserveGb: 4
  maxParallelWorkers: 2
```

Users should also eventually be able to express natural constraints such as:

```text
Keep at least 4GB free.
Do not run more than one heavy model.
Use remote generation if local memory is insufficient.
```

But:

> The Director may translate user intent into constraints; the Resource Manager is the enforcement authority.

Conceptual Go API:

```go
lease, err := resources.Acquire(ResourceRequest{
    RAM:       4 * GB,
    CPU:       2,
    GPU:       0,
    Exclusive: false,
})
if err != nil {
    return err
}
defer lease.Release()
```

Resource planning should eventually account for:

- RAM
- CPU
- GPU/Metal capability
- model size
- resolution
- frame count
- precision
- offloading
- chunking
- concurrency
- historical timing
- historical peak memory
- preview mode
- local/remote eligibility

---

# 10. Event Architecture

The execution backbone should be event-oriented.

Initial event vocabulary:

```text
PROJECT_CREATED
PLAN_CREATED
SHOT_CREATED
SHOT_UPDATED
SHOT_INVALIDATED

MODEL_LOAD_STARTED
MODEL_LOADED
MODEL_UNLOADED

GENERATION_STARTED
GENERATION_PROGRESS
GENERATION_COMPLETED
GENERATION_FAILED

VALIDATION_STARTED
VALIDATION_COMPLETED
VALIDATION_FAILED

TIMELINE_UPDATED

RENDER_STARTED
RENDER_COMPLETED
```

The event system should not force the entire product into a complicated event-sourcing framework prematurely.

Use a pragmatic event-driven architecture.

Core state remains the source of truth.

---

# 11. TUI

The TUI is a control plane, not the renderer.

Target inspiration:

```text
OpenCode-like terminal IDE/control room
```

Conceptual tabs:

```text
[Chat] [Plan] [Shots] [Timeline] [Assets] [Models] [Logs]
```

Potential panels:

- project
- shot list
- shot inspector
- timeline
- asset browser
- model status
- resource usage
- logs
- generation progress
- preview
- Director chat

Examples of user commands:

```text
Make shot 3 fullscreen.
Change this to deterministic code.
Generate 3 previews.
Use Claude Haiku for semantic decisions.
Use Qwen locally for cheap decisions.
Don't use more than 4GB RAM.
```

Chat must eventually translate into structured project mutations:

```text
User
→ Director Chat
→ Intent
→ ChangeSet
→ Review/Apply
→ Invalidate affected shots
→ Rebuild
```

The TUI must remain responsive while workers run.

---

# 12. Review Workflow

Every phase must follow:

```text
Implement
→ Self-review
→ Tests
→ Agent Review #1
→ Fix
→ Tests
→ Agent Review #2
→ Fix
→ Tests
→ Final lead review
→ Documentation update
→ Commit phase
```

## 12.1 Agent rotation

Do not let the same model write and perform the only review.

Use different agents/models.

Example:

```text
Phase implementation: Terra
Review #1: Sol
Review #2: Luna
```

Next phase:

```text
Implementation: Sol
Review #1: Luna
Review #2: Terra
```

Then rotate.

The exact mapping may be adapted based on which models are actually available.

## 12.2 Review command

Agents may be invoked through OpenCode-style commands such as:

```text
opencode --agent terra
opencode --agent sol
opencode --agent luna
```

Do not assume these names map to a specific model without checking the local OpenCode configuration.

The main session must verify which model each agent actually uses before relying on the review assignment.

## 12.3 Last two phases

At minimum, the **last two completed phases** must each receive review from **two different models sequentially** before being considered final.

This is a hard quality gate.

## 12.4 Evidence of review

A review is not complete because an agent said:

```text
LGTM
```

The main session must preserve evidence.

For every review record:

```text
Phase:
Reviewer:
Model:
Date:
Commit reviewed:
Scope:
Findings:
Severity:
Files:
Resolution:
Tests rerun:
Final status:
```

Store review evidence in:

```text
docs/reviews/
```

Recommended:

```text
docs/reviews/phase-01-review-terra.md
docs/reviews/phase-01-review-sol.md
```

If the review is only useful in the current context, still summarize it into the phase evidence file before commit.

---

# 13. Severity Policy

Review findings:

```text
BLOCKER
HIGH
MEDIUM
LOW
INFO
```

Rules:

### BLOCKER

Must fix before phase commit.

### HIGH

Must fix before phase commit unless the lead explicitly documents why it is deferred.

### MEDIUM

Fix if within phase scope.

Otherwise document as a follow-up TODO.

### LOW / INFO

May be documented without implementation if not material.

Never silently ignore findings.

---

# 14. Phase Completion Gate

A phase may be marked complete only when:

```text
[ ] All phase implementation TODOs complete
[ ] Unit tests pass
[ ] Integration tests pass where applicable
[ ] go test ./...
[ ] go test -race ./... where applicable
[ ] go vet ./...
[ ] Build succeeds
[ ] Agent review #1 completed
[ ] Review #1 findings resolved/documented
[ ] Tests rerun
[ ] Agent review #2 completed
[ ] Review #2 findings resolved/documented
[ ] Tests rerun
[ ] Documentation updated
[ ] Review evidence written
[ ] Git diff inspected
[ ] No accidental files/secrets
[ ] Phase commit created
```

Only then change the phase status to:

```text
DONE
```

---

# 15. Context Management

OpenCode sessions may become large.

The main session must proactively monitor context.

When approximately **80% of available context** is consumed:

1. Stop starting new unrelated work.
2. Inspect current TODO state.
3. Finish or safely checkpoint the current atomic task.
4. Update this file.
5. Update relevant architecture/decision documents.
6. Record:
   - completed work
   - incomplete work
   - current branch
   - current commit
   - uncommitted files
   - tests run
   - review status
   - known issues
   - next exact tasks
7. Create a clean next-session handoff prompt.
8. Save the handoff under:

```text
docs/handoffs/
```

Recommended filename:

```text
docs/handoffs/next-session-phase-XX.md
```

The final response of the session should also contain a concise copy of the next-session prompt.

Do NOT continue blindly until context is exhausted.

---

# 16. Documentation Structure

Maintain:

```text
README.md
AGENTS.md
ARCHITECTURE.md
DECISIONS.md
ROADMAP.md

docs/
├── architecture/
├── decisions/
├── reviews/
├── handoffs/
└── phases/
```

Use the simplest structure that remains useful.

Do not generate documentation merely for the sake of documentation.

---

# 17. Git Rules

Every phase gets exactly one logical phase commit.

Example:

```text
phase 00: foundation and contracts
phase 01: project state and timeline
phase 02: event system
phase 03: deterministic renderer
...
```

Commit format:

```text
phase(00): establish foundation and contracts
phase(01): implement project state and timeline
phase(02): implement event system
```

No automatic push.

No automatic merge.

No auto-generated credentials.

No committing:

```text
.env
API keys
tokens
credentials
local model weights
large generated videos
machine-specific configuration
private data
```

Use `.gitignore` from the beginning.

---

# 18. Public Repository Requirement

The repository is intended to be public under:

```text
github.com/manish96170
```

The implementation must therefore be safe for public source control.

Before the first public push, run a repository hygiene review:

```text
git status
git diff
git diff --cached
git ls-files
```

Check for:

- secrets
- API keys
- credentials
- personal information
- private URLs
- local machine paths
- model weights
- generated media
- oversized artifacts
- proprietary code copied from external projects

The repository should contain:

```text
LICENSE
README.md
CONTRIBUTING.md
.gitignore
```

Use a permissive open-source license only after confirming the intended project licensing decision.

Do not invent third-party licenses.

If third-party source code is copied, preserve its license and attribution requirements.

---

# 19. Development Quality Rules

Every Go package should aim for:

- small interfaces
- explicit dependencies
- dependency injection
- context cancellation
- deterministic behavior where possible
- structured errors
- useful error messages
- no hidden global mutable state
- testable units
- clear package boundaries

Prefer:

```go
type Provider interface {
    ID() string
    Capabilities(ctx context.Context) (ProviderCapabilities, error)
}
```

over giant interfaces containing every possible provider operation.

Split interfaces by capability when appropriate.

Avoid:

```go
type GodProvider interface {
    GenerateVideo()
    GenerateImage()
    Transcribe()
    Synthesize()
    Decide()
    Render()
    Validate()
    ...
}
```

---

# 20. Phase Roadmap

The implementation is divided into the following phases.

```text
PHASE 00  Foundation / Contracts
PHASE 01  Project State / Timeline
PHASE 02  Event System
PHASE 03  Deterministic Renderer
PHASE 04  Flows / Framing / Primitives
PHASE 05  Director / Video Planning
PHASE 06  Decision Engine
PHASE 07  Provider System / Router
PHASE 08  Model Manager / Resource Planner
PHASE 09  Validation / Shot Intelligence
PHASE 10  Preview / Checkpoint / Cache / Visual Review
PHASE 11  Audio / Timestamp / Temporal Intelligence
PHASE 12  TUI Control Plane / Visual Review
PHASE 13  Director Chat / ChangeSets / Visual Editing
PHASE 14  Local AI Providers
PHASE 15  Cloud Providers
PHASE 16  End-to-End Autonomous Pipeline
PHASE 17  Performance / Reliability / Packaging
PHASE 18  Public Repository / Release Readiness
```

Do not skip phases merely because later code appears easy.

A phase can be merged with the next phase only if the lead explicitly determines that the separation creates no useful architectural boundary and documents the reason.

---

# PHASE 00 — FOUNDATION / CONTRACTS

## Goal

Create the Go repository foundation and stable domain contracts.

## TODO

- [x] Inspect repository
- [x] Determine current repository state
- [x] Create/update `AGENTS.md`
- [x] Create/update `ARCHITECTURE.md`
- [x] Create/update `DECISIONS.md`
- [x] Create/update `ROADMAP.md`
- [x] Initialize Go module if needed
- [x] Establish package structure
- [x] Create domain contracts
- [x] Create event contracts
- [x] Create provider capability contracts
- [x] Create decision contracts
- [x] Create generation contracts
- [x] Create validation contracts
- [x] Create resource contracts
- [x] Create model capability contracts
- [x] Add JSON serialization where needed
- [x] Add context-aware interfaces
- [x] Add tests
- [x] Add `.gitignore`
- [x] Add basic public-repository hygiene
- [x] Run `go test ./...`
- [x] Run `go test -race ./...`
- [x] Run `go vet ./...`
- [x] Build executable
- [x] Agent review #1 (terra — see `docs/reviews/phase-00-review-terra.md`)
- [x] Resolve findings
- [x] Agent review #2 (astra — see `docs/reviews/phase-00-review-astra.md`)
- [x] Resolve findings
- [x] Write review evidence (`docs/phases/phase-00.md`, `docs/reviews/`)
- [x] Update docs
- [x] Commit phase

## Explicit non-goals

Do NOT implement:

- AI providers
- cloud providers
- TUI
- model inference
- heavy rendering
- Python embedding
- complex scheduler
- cloud infrastructure

## Required result

The repository should compile and provide stable contracts for future phases.

---

# PHASE 01 — PROJECT STATE / TIMELINE

## Goal

Implement project persistence and the fundamental video data model.

## TODO

- [x] Project model
- [x] VideoPlan model
- [x] Shot model
- [x] Timeline model
- [x] Asset model
- [x] Primitive model
- [x] Flow model
- [x] Framing model
- [x] Project loading
- [x] Project saving
- [x] Versioning strategy
- [x] Stable IDs
- [x] Stable visual element IDs
- [x] Element-to-shot/timeline relationship
- [x] Deterministic serialization
- [x] Timeline validation
- [x] Dependency representation
- [x] Unit tests
- [x] Corrupt-state/error handling
- [x] Agent review #1 (sol — see `docs/reviews/phase-01-review-sol.md`)
- [x] Fix findings
- [x] Agent review #2 (luna6 — see `docs/reviews/phase-01-review-luna6.md`)
- [x] Fix findings
- [x] Review evidence (`docs/phases/phase-01.md`, `docs/reviews/`)
- [x] Docs
- [x] Commit

## Required result

A project can be created, loaded, modified, saved, and validated without any renderer or AI model.

---

# PHASE 02 — EVENT SYSTEM

## Goal

Create the event backbone for observable execution.

## TODO

- [x] Event type
- [x] Event metadata
- [x] Event IDs
- [x] timestamps
- [x] project/shot correlation IDs
- [x] event payloads
- [x] event publisher
- [x] event subscriber
- [x] cancellation
- [x] backpressure behavior
- [x] error behavior
- [x] test concurrent subscribers
- [x] test event ordering guarantees
- [x] document ordering semantics
- [x] Agent review #1 (astra — see `docs/reviews/phase-02-review-astra.md`)
- [x] Fix
- [x] Agent review #2 (gpt-oss-120b — see `docs/reviews/phase-02-review-gpt-oss-120b.md`)
- [x] Fix
- [x] Evidence (`docs/phases/phase-02.md`, `docs/reviews/`)
- [x] Docs
- [x] Commit

## Required result

Workers and future TUI components can observe execution without directly coupling to one another.

---

# PHASE 03 — DETERMINISTIC RENDERER

## Goal

Produce a real MP4 from deterministic primitives.

This is the first major milestone.

Target:

```text
manual VideoPlan
→ deterministic shots
→ renderer
→ MP4
```

## TODO

- [x] Renderer interface
- [x] deterministic rendering backend
- [x] FFmpeg integration strategy
- [x] basic text primitive
- [x] basic image primitive
- [x] basic background primitive
- [x] basic transition
- [x] shot composition
- [x] timeline composition
- [x] render output
- [x] metadata
- [x] render manifest / visual element metadata
- [x] stable rendered element identity
- [x] element-to-shot/timeline mapping
- [x] editable-property metadata
- [x] FFprobe validation
- [x] deterministic test fixture
- [x] end-to-end render test
- [x] Agent review #1 (luna — see `docs/reviews/phase-03-review-luna.md`)
- [x] Fix
- [x] Agent review #2 (gpt-oss-20b — see `docs/reviews/phase-03-review-gpt-oss-20b.md`)
- [x] Fix
- [x] Evidence (`docs/phases/phase-03.md`, `docs/reviews/`)
- [x] Docs
- [x] Commit

## Required result

The repository can generate a playable deterministic MP4 without AI.

---

# PHASE 04 — FLOWS / FRAMING / PRIMITIVES

## Goal

Make video composition reusable instead of hard-coded.

## TODO

- [x] Flow registry
- [x] Framing registry
- [x] primitive registry
- [x] capability declarations
- [x] developer-explainer flow
- [x] code-tutorial flow
- [x] fullscreen framing
- [x] split-screen framing
- [x] picture-in-picture framing
- [x] developer-circle framing
- [x] screen-focused framing
- [x] composition tests
- [x] ensure framing is not embedded into business logic
- [x] Agent review #1 (terra — see `docs/reviews/phase-04-review-terra.md`)
- [x] Fix
- [x] Agent review #2 (sol6 — see `docs/reviews/phase-04-review-sol6.md`)
- [x] Fix
- [x] Evidence (`docs/phases/phase-04.md`, `docs/reviews/`)
- [x] Docs
- [x] Commit

---

# PHASE 05 — DIRECTOR / VIDEO PLANNING

## Goal

Convert a user prompt into a structured VideoPlan.

The Director should plan, not render.

## Pipeline

```text
Prompt
→ Director
→ VideoPlan
```

## TODO

- [x] Director interface
- [x] planning request
- [x] planning result
- [x] flow selection
- [x] framing selection
- [x] shot decomposition
- [x] shot duration planning
- [x] deterministic-vs-AI intent
- [x] asset requirements
- [x] provider capability requirements
- [x] plan validation
- [x] deterministic fixture Director
- [x] optional LLM adapter interface
- [x] prompt/version metadata
- [x] tests
- [x] Agent review #1 (luna — see `docs/reviews/phase-05-review-luna.md`)
- [x] Fix (Luna confirmed all findings resolved; uncached full gate passes)
- [x] Agent review #2 (astra — see `docs/reviews/phase-05-review-astra.md`)
- [x] Fix (standalone linkage validation and full invariant tests; both reviewers approved sequentially)
- [x] Evidence (`docs/phases/phase-05.md`, both review records)
- [x] Docs (architecture/ADR-018/status and historical handoff reconciled)
- [x] Commit (`phase(05): implement director / video planning`)

## Important

Do not tightly couple the Director to Claude, Qwen, Gemini, or any single model.

---

# PHASE 06 — DECISION ENGINE

## Goal

Build structured, cheap, pluggable classification.

## Owner-approved execution policy (2026-10-09)

- Reuse `decision.Provider` and `domain.DecisionRequest`/`DecisionResult`;
  do not add vendor/model-specific contracts or implement real AI adapters.
- Score is affirmative likelihood, not confidence in the reported verdict.
  Validate finite `[0,1]` scores and known verdicts; deterministic core
  recomputes the result with configurable thresholds. Default YES >= 0.85,
  NO <= 0.30; retain existing zero/equal-policy semantics and YES-first ties.
- Require request ID and nonblank kind. The decision-local registry admits
  adapters implementing the existing decision/capability interfaces, with
  matching stable IDs and `semantic-decision` capability. A small resolver
  seam permits later Phase 07 integration without building its full router.
- Require an explicit ordered provider chain (maximum 8 providers), with
  configurable cooperative per-attempt timeout (default 5s), maximum attempts
  per provider including initial call (default 2, hard cap 3), and fixed
  cancellation-aware retry delay (default 100ms). Retry only child timeouts
  and explicitly classified transient errors. Parent cancellation terminates
  the operation; no detached goroutines to hide noncooperative adapters.
- Default uncertainty escalation is enabled: valid UNCERTAIN advances without
  retry; return the last valid UNCERTAIN if later providers fail or abstain.
  If all attempts fail, return an exhausted error, not manufactured uncertainty.
- Snapshot JSON-compatible request input and provide an independent copy per
  attempt. Callers must not mutate input during snapshotting; adapters must
  not retain/asynchronously mutate it after return. Concurrent callers require
  safe adapters/resolvers/publishers; registry locks never cover adapter calls.
- Audit is optional best-effort observability through injected events, not
  durable/compliance storage. Default cooperative publication timeout 200ms;
  count publication failures without replacing classification results. Publish
  immutable scalar-only records and engine-owned outcome codes; omit input,
  raw kind/reason/error strings. Correlation/provider handles must be nonsecret.
  Cancellation terminal publication may use separately bounded cooperative
  cleanup. The existing event bus may drop records; no queues/persistence added.
- Orchestrator owns integration, test gates, tracker, and one local phase commit;
  bounded agents own implementation/docs. User-approved sequential reviewers:
  terra (`amazon-bedrock/openai.gpt-5.6-terra`), then luna6
  (`amazon-bedrock/us.openai.gpt-6-luna`), verified in roster/model catalog.

## TODO

- [x] DecisionProvider interface
- [x] DecisionRequest
- [x] DecisionResult
- [x] YES/NO/UNCERTAIN
- [x] confidence/score
- [x] reason codes
- [x] configurable thresholds
- [x] fallback/escalation policy
- [x] provider registry integration
- [x] deterministic mock provider
- [x] timeout handling
- [x] retry policy
- [x] malformed-result handling
- [x] audit logging
- [x] tests
- [x] Agent review #1 (terra — see `docs/reviews/phase-06-review-terra.md`)
- [x] Fix (Terra withdrew disproven R1-01, confirmed R1-02 fixed; full uncached gate passes)
- [x] Agent review #2 (luna6 — see `docs/reviews/phase-06-review-luna6.md`)
- [x] Fix (both reviewers confirmed final approval sequentially; full uncached gate passes)
- [x] Evidence (`docs/phases/phase-06.md`, both review records)
- [x] Docs (ADR-019, as-built architecture, and README status reconciled)
- [x] Commit (`phase(06): implement decision engine`)

---

# PHASE 07 — PROVIDER SYSTEM / ROUTER

## Goal

Introduce provider abstraction and routing.

## Lead decisions under delegated routine-policy authority (2026-10-09)

- Keep `provider.Provider` unchanged. Reuse capability tags (`deterministic`
  for that classification) and `Local`/`!Local`; add only missing cost-unit,
  optional latency-estimate, and separate health metadata. No model names.
- Build a zero-value-usable registry with explicit registration/refresh/health
  updates. Discover capabilities cooperatively outside locks; publish fully
  validated, defensively copied snapshots atomically. IDs stay stable/matching
  before and after discovery, even on error; capability-result ID matching
  applies on success only (results accompanying errors are ignored). Failed refresh preserves previous
  state; routing uses one coherent snapshot and makes no adapter calls.
- Default health is UNKNOWN; only explicit HEALTHY is eligible. No background
  polling, health inference, TTL, circuit breaker, or automatic replacement.
- Route by exact case-sensitive ALL-required-tags matching. Require a nonempty
  tag request. Default local-only; any/remote mode is explicit opt-in. Filter
  all constraints first, then eligible explicit preferences, then deterministic,
  local non-deterministic, remote; lexical provider-ID ties make order stable.
  Fallback is the remaining eligible IDs, not execution/retry or relaxed gates.
- `CostUnit` is an exact currency/unit label (no conversion); nonempty unit
  makes zero explicitly free. Unlabelled zero cost and nil latency are unknown;
  unknown/mismatched estimates fail an applicable cap but not unconstrained
  routing. Nonzero cost requires a nonblank unit. Estimates are finite,
  nonnegative declaration metadata; cap comparisons are inclusive.
- Local hardware must be declared: nil means unknown and is ineligible;
  explicit empty requirements mean no minimum. Positive RAM/GPU requirements
  need known sufficient caller hardware facts. Remote execution requirements
  are not compared with local machine facts. This is eligibility, not leases,
  reservations, dynamic resource enforcement, or Phase 08 model management.
- Exact-ID resolution never silently substitutes another provider. Add a
  decision-side adapter importing `provider` (not vice versa) to require
  semantic-decision plus a real `decision.Provider` implementation. Preserve
  Phase 06 behavior and map permanent identity/admission failures to its
  sentinels so transient causes cannot trigger retries of invalid providers.
- Copy caller request/configuration slices and nested metadata/pointers;
  preserve caller-owned concurrency-safe adapter lifecycles. Errors/rejection
  records use safe core codes and documented nonsecret provider handles.
- No real inference/provider installation, generation execution, worker leases,
  shot intelligence, or external calls are in scope. Reviews: luna6
  (`amazon-bedrock/us.openai.gpt-6-luna`) then astra
  (`amazon-bedrock/us.openai.gpt-6-astra`), verified in roster/catalog.

## TODO

- [x] Provider interface
- [x] ProviderCapabilities
- [x] ProviderRegistry
- [x] ProviderRouter
- [x] local provider classification
- [x] remote provider classification
- [x] deterministic provider classification
- [x] capability matching
- [x] cost metadata
- [x] latency metadata
- [x] hardware requirements
- [x] provider health state
- [x] eligibility checks
- [x] routing policy
- [x] fallback policy
- [x] tests
- [x] Agent review #1 (luna6 — see `docs/reviews/phase-07-review-luna6.md`)
- [x] Fix (independent oracle catches deliberate mutant; production restored, Luna6 confirmed)
- [x] Agent review #2 (astra — see `docs/reviews/phase-07-review-astra.md`)
- [x] Fix (six-case mutant caught, production restored, sequential final approvals)
- [x] Evidence (`docs/phases/phase-07.md`, both review records)
- [x] Docs (ADR-020, architecture/status, and continuous authority reconciled)
- [x] Commit (`phase(07): implement provider registry and routing`)

---

# PHASE 08 — MODEL MANAGER / RESOURCE PLANNER

## Goal

Make local AI execution hardware-aware and memory-safe.

## Lead decisions under delegated routine-policy authority (2026-10-10)

- Preserve resource Request/Manager/Lease and domain ResourcePlan contracts;
  add only missing heavy/accelerator flags, model execution requirements, and
  estimate provenance. Freeze shared contracts before disjoint model/worker work.
- Configuration is explicit, not detected or assumed. Reference profile: decimal
  GB = 1,000,000,000 bytes; supplied 16 GB total less 3 GB system, 4 GB user,
  1.5 GB Director gives 7.5 GB worker RAM. CPU/accelerator facts are explicit;
  reference MaxParallel=2, MaxHeavy=1, queue limit=64. Validate reserves and caps.
- Grant RAM/CPU/GPU/slot/heavy/exclusive allocations atomically. RAM is integer
  bytes; conservatively quantize fractional CPU/GPU allocation units to fixed
  micro-units to avoid cumulative floating-point release drift. Reject invalid,
  incompatible, overflowing, or permanently over-budget requests before queueing.
  Heavy classification includes explicit heavy, positive GPU use, or RAM >= a
  configured threshold (reference 2 GB); callers cannot bypass it with false flags.
- Use bounded FIFO with intentional head-of-line blocking. Remove canceled tickets;
  roll back a grant when cancellation is observed before handoff. Granted leases
  are caller-owned, never freed automatically while work might still run. Concrete
  Release is bounded, concurrency-safe, and idempotent. Snapshots are copied.
- Accelerator backend/capacity and unified/discrete memory facts are explicit;
  Metal is not CUDA. GPU units are logical allocations, not a measured VRAM ceiling.
  Profiles must account for unified-memory allocations in RAM; unknown positive
  requirements are locally ineligible. No claim of kernel RAM/CPU/GPU containment.
- Freeze revisioned execution profiles with declared load/resident/workspace bounds
  and supported precision/size/frame/quality/offload/chunk envelopes. Legacy minimum
  memory is a floor, not a peak. Apply a configurable reference 25% safety margin;
  unknown/unbounded profiles cannot load locally. No invented weights-to-RAM formulas.
- Bounded, copied history (reference 256 exact workload/profile/hardware keys)
  stores parent-measured peaks, load/run/unload timing, source/completeness, and
  deterministic snapshot provenance. History may raise reservations, never lower
  them from sparse samples. A full history rejects new keys instead of silently
  evicting learned high-water evidence; existing keys may still update. No ambient
  state, raw prompts/paths, or auto persistence.
- Model manager refuses busy/reserving/loading/unloading conflicts. Acquire a full
  peak reservation before loading and retain RAM/CPU/GPU/slot/heavy resources until
  explicit unload; use-handle release only clears use ownership. No nested worker
  acquisition, hidden eviction, or automatic context-expiry refund of live resources.
  Recompute admission from frozen profiles, not caller/LLM-edited ResourcePlans.
  Loaded-instance reuse is conservatively limited to the same profile/exact
  workload with sufficient held reservation; otherwise explicit unload is required.
- Call loaders outside locks; check identity before/after every callback, including
  error paths. Reference cooperative load timeout 30s, unload/cleanup 5s. Failed,
  canceled, or late loads perform separately bounded cleanup. Release only on
  proven stop; uncertain cleanup retains a quarantined reservation for explicit retry.
- Worker processes use direct os/exec and versioned bounded JSON, not shell text
  or Python embedding. Trusted runtime config owns argv/cwd/environment/executable,
  never generation parameters. Reference stdin/stdout limits 1 MiB, stderr 8 KiB,
  sample interval 100ms, cleanup timeout 5s; validate successful reply correlations,
  ignore error-accompanying payloads, and emit only safe core diagnostics.
- Own one Unix process group/Wait path and join reader/monitor tasks. Stop/reap and
  clean remaining descendants on cancellation, breach, protocol failure, or normal
  exit before reporting resources stopped. Native macOS/Linux family RSS sampling
  failures fail closed; sampled breach detection is not an instantaneous hard cap.
  Workers must not daemonize/escape the group; unsupported platforms fail explicitly.
- Planning distinguishes permanent local ineligibility, temporary wait, and explicitly
  gated Phase 07 remote candidates. PreferRemote is advice, never permission to spend
  or execute. No models/downloads/cloud calls. Reviews: verified terra then luna.

## TODO

- [x] ResourceManager interface
- [x] ResourceRequest
- [x] ResourceLease
- [x] ResourcePlan
- [x] memory budget
- [x] CPU budget
- [x] GPU/Metal capability representation
- [x] model requirements
- [x] model lifecycle
- [x] load/unload interface
- [x] resource reservation
- [x] resource release
- [x] concurrency limits
- [x] queueing
- [x] cancellation
- [x] process monitoring
- [x] peak-memory tracking
- [x] model timing
- [x] adaptive historical planning
- [x] local-vs-remote decision
- [x] worker process abstraction
- [x] no Python embedding
- [x] tests for over-budget requests
- [x] tests for concurrent resource requests
- [x] race tests
- [x] Agent review #1 (terra — see `docs/reviews/phase-08-review-terra.md`)
- [x] Fix (LOW stale full-gate status corrected; no code defects found)
- [x] Agent review #2 (luna — see `docs/reviews/phase-08-review-luna.md`)
- [x] Fix (Luna approved with no outstanding findings)
- [x] Evidence (`docs/phases/phase-08.md`, formal Terra/Luna and supplementary Astra records)
- [x] Docs (ADR-021, whole-phase architecture, status, and checkpoint reconciled)
- [x] Commit (`phase(08): implement resource and model management`)

## Required invariant

Resource limits must be enforced by code, not by LLM instructions.

---

# PHASE 09 — VALIDATION / SHOT INTELLIGENCE

## Goal

Prevent expensive generation when cheaper deterministic approaches are sufficient.

## Lead decisions under delegated routine-policy authority (2026-10-11)

- Deliver two bounded tranches: `validation/` plus narrow native/render helpers,
  then `intelligence/` after its validation API freezes. One source/test owner
  per tranche; no oversized all-in-one implementation assignment.
- Reuse `domain.ValidationResult` unchanged; details contain bounded core-owned
  issue/severity codes, not paths, raw native errors, or arbitrary asset metadata.
  Invalid requirements/dependencies/caller cancellation return safe errors;
  observed media failure returns a failed result. Retry advice defaults false.
- Quality is explicit: expected duration/FPS/width/height and draft/final label,
  with no resolution invented from the label. Reference tolerances: duration
  ±150ms and FPS ±0.01; require finite/positive values and safe conversions.
  Reuse existing domain.Quality rather than a parallel quality vocabulary.
  GenerationResult.DurationMs is wall time, never media duration.
- Inspect only authorized regular local files; reject network/protocol URIs and
  escapes outside configured roots. Native query/decode is synchronous and
  context-bounded, with bounded output, restricted formats/protocols, and source
  size/dimension limits. Reference timeout 5s, file limit 1 GB, 64 KiB metadata/
  diagnostic capture, source <=16,777,216 pixels and <=8192 per dimension;
  allocation guards are not kernel containment. No arbitrary network fetch.
- Measure video/audio/image facts rather than trusting Asset.Metadata. Count
  audio tracks independent of stream order; require audio only when explicitly
  requested, validate audio stream facts and bounded decodability. Decode one
  small bounded thumbnail for visual presence. Static/flat/dark shots and fades
  are valid; optional uniform-frame checks are warnings, not semantic/motion QA.
- Asset reuse requires explicitly associated candidate IDs from the authoritative
  project plus affirmative suitability and verified kind/quality. Shot.DependsOn
  references shots, not assets; do not invent persistent ShotRef fields or search
  every project asset for vague matches. Image substitution/animation needs a
  verified image and positive suitability; unknown/uncertain/error blocks that choice.
- Deterministic facts precede optional decision.Engine classifications. Registry
  deterministic capability is conceptual; a renderer-owned support query separately
  reports current Background/Image/Text/Transition support. Unsupported exact
  deterministic content never becomes AI solely because a provider is absent.
- Return advisory provider.RouteResult and optional resource.PlanResult only with
  matching trusted provider/model/profile/workload facts. Preserve all Phase 07
  health/tag/locality/cost/latency/hardware gates, local-only default, and Phase 08
  local_ready/wait/ineligible/remote statuses. Missing resource profiles are unknown,
  not ready. No budget acquisition, model load, generation, cache/player, speech,
  installs, or actual paid/cloud calls in scope.
- Audio-required new-video strategies require explicit caller-declared
  AudioRequiredTags, not arbitrary unrelated routing tags. Eligibility tags do
  not prove composed audio; verified audio-bearing video reuse is separate.
  Current intelligence resource evidence is attested only for local-only bindings;
  AnyLocality/remote resource readiness remains unknown rather than falsely ready.
- Callback/native cancellation is checked before/after and late success rejected;
  error-accompanying payloads ignored, metadata/slices copied, typed-nil required
  dependencies rejected. Safe diagnostics retain causes without public raw text.
  Only explicitly transient inspection errors/child timeouts merit retry advice;
  permanent missing/unauthorized/invalid/limited media or runtime failures override
  wrapped transient markers. Nil contexts are invalid requests, not panics.
  No automatic retries or permission to spend. Reviews: verified astra then terra.

Pipeline:

```text
Shot
→ Shot Intelligence
→ Decision
→ Provider
```

## TODO

- [-] deterministic feasibility check
- [-] asset reuse check
- [-] image-vs-video check
- [-] animate-existing-image check
- [-] local-vs-cloud check
- [-] provider eligibility check
- [x] quality requirements
- [x] validation rules
- [x] video metadata validation
- [x] duration validation
- [x] frame-rate validation
- [x] resolution validation
- [x] audio validation
- [x] basic visual checks
- [x] failure classification
- [x] retry recommendation
- [x] tests (lead full uncached test/race/vet/build gate passes after acceptance and R1 regressions)
- [x] Agent review #1 (astra — approval after R1 fixes; see `docs/reviews/phase-09-review-astra.md`)
- [x] Fix (P09-A1/A2 and four in-scope Astra MEDIUM findings resolved and retested)
- [x] Agent review #2 (terra — approval after documentation/evidence confirmation; see `docs/reviews/phase-09-review-terra.md`)
- [x] Fix (Terra MEDIUM documentation/evidence reconciliation resolved and confirmed)
- [x] Evidence (both formal reviews recorded)
- [x] Docs (as-built architecture, ADR, roadmap, checkpoint history, and review evidence reconciled)
- [x] Commit (`phase(09): implement validation and shot intelligence`)

## Historical context-rollover checkpoint (2026-10-11, superseded on resume)

Owner requested handoff at approximately 88% context. Stop implementation safely;
do not apply production fixes or start formal reviews in this session. Resume from
`docs/handoffs/next-session-phase-09.md`. HEAD `61e078b`, branch main; Phase 09
changes are dirty and unstaged, with no active source/test writers.

Validation is tested/frozen. Intelligence source and four focused tests exist;
five additional acceptance functions expose two unresolved ownership bugs:

- HIGH P09-A1: `snapshot(req, &req)` in `Analyzer.Analyze` decodes into existing
  caller-aliased state. Decode into a fresh zero-valued Request before assignment.
- MEDIUM P09-A2: router callback receives retained `policy` without another Clone;
  mutations can change downstream admission. Pass `policy.Clone()` to the callback.

The lead independently reproduced the ownership subtest failure in normal and
race runs (same assertions, no race-detector diagnostics). Checkpoint full build
and vet PASS, but the full current Phase 09 test/race gate has not passed. No fixes
have been applied; tests must not be weakened. Test-engineering evidence is in
`docs/reviews/phase-09-acceptance-findings.md`, NOT a formal phase review. Astra →
Terra formal reviews and the full Phase 09 completion gate remain pending.

---

# PHASE 10 — PREVIEW / CHECKPOINT / CACHE

## Goal

Make iteration cheap and incremental.

## Lead decisions under delegated routine-policy authority (2026-10-11)

- Keep `domain.RenderManifest` renderer-only and reuse existing domain Project,
  Quality, GenerationResult, ValidationResult, ReviewFeedback, dependency, and
  timeline contracts. New artifact/checkpoint/review/preview behavior belongs in
  small packages, not the renderer, project aggregate, or a new UI.
- Cache keys are SHA-256 hashes of a typed canonical projection, never raw map
  iteration/JSON bytes. Include only pixel-affecting explicit facts (shot/render
  configuration, quality, source-content digests, direct dependency artifact
  digests, and applicable seed/provider/model/profile revisions). Exclude output
  paths, IDs, clock values, measurements, feedback, and events.
- Cache hits require a completed, integrity-verified artifact/manifest within a
  controlled root; partial, corrupt, wrong-identity, or missing entries are safe
  misses. Publication later uses owned staging plus atomic rename; never persist
  contexts, leases, live workers, raw errors, prompts, credentials, or absolute
  paths. Resume schedules unfinished work; it does not resume a process.
- Reuse `project.DependencyGraph.AffectedBy` for downstream invalidation and add
  changed roots explicitly. A cache key independently incorporates direct
  dependency artifact identity so missed event delivery cannot reuse stale output.
- Phase 10 exposes a programmatic preview read model and persists validated
  `ReviewFeedback`; it does not implement a web/TUI player, playback clock,
  scrubber UI, ChangeSet translation/application, or Phase 11 audio timestamps.
  Both future clients consume this shared data boundary.
- Implement in bounded tranches after each API freezes: pure artifact key/
  invalidation; controlled artifact/checkpoint storage; feedback/preview read
  model; then a small synchronous coordinator. No generation, paid calls, model
  loading, or background scheduler. Planned reviewers: luna then luna6.

## TODO

- [x] preview mode (programmatic preview coordinator/read model)
- [x] draft quality (explicit domain quality in key, cache, checkpoint, and coordinator)
- [x] final quality (explicit domain quality in key, cache, checkpoint, and coordinator)
- [x] preview output (verified root-relative media and manifest view)
- [x] shot cache (integrity-checked immutable artifact store)
- [x] input hashing (canonical typed SHA-256 projection; scoped tests/race/vet pass)
- [x] dependency hashing (direct published-artifact digest roles; scoped tests/race/vet pass)
- [x] cache hit/miss (coordinator verifies and bypasses rendering on accepted hits)
- [x] checkpointing (versioned atomic checkpoint store)
- [x] resume (artifact-revalidating pending/ready classification, no process restore)
- [x] invalidation (changed roots; scoped tests/race/vet pass)
- [x] downstream invalidation (reuses validated dependency graph closure; scoped tests/race/vet pass)
- [x] sidecar metadata (checksummed artifact and manifest sidecars)
- [x] reproducibility metadata (key-bound provenance and artifact digests)
- [x] generation seed (optional explicit key/provenance input)
- [x] provider/model/version metadata (optional explicit revision provenance)
- [x] timing (measured coordinator render duration)
- [x] peak memory (explicit nil unknown; never fabricated)
- [x] validation result (accepted persisted validation is required for preview)
- [x] source assets (explicit digest-role provenance)
- [!] local preview player (deferred to Phase 12 UI; Phase 10 supplies shared read model only)
- [!] timeline scrubber / current timestamp (deferred to Phase 12 UI; `View.AtShot`/`AtProject` are available)
- [x] visual element selection (stable element/timestamp read-model queries)
- [x] timestamped review feedback (existing ReviewFeedback validated against authoritative timeline)
- [x] review feedback persistence (bounded append-only store)
- [x] preview reload after rebuild (deterministic view revision token)
- [x] tests (full uncached test/race/vet/build gate passes)
- [x] Agent review #1 (luna — approved after manifest-identity fix; see `docs/reviews/phase-10-review-luna.md`)
- [x] Fix (identity-only manifest rebinding; non-ID changes require rerender)
- [x] Agent review #2 (luna6 — approved after validation/measurement fixes; see `docs/reviews/phase-10-review-luna6.md`)
- [x] Fix (accepted validation required; render duration recorded; peak unknown preserved)
- [x] Evidence (both formal records written)
- [x] Docs (architecture, ADR, roadmap, phase evidence, and handoff reconciled)
- [x] Commit (`phase(10): implement preview checkpoint and cache`)

## Target asset layout

```text
shot-017.mp4
shot-017.json
```

---

# PHASE 11 — AUDIO / TIMESTAMP PIPELINE

## Goal

Treat audio as a first-class timeline input.

## Lead decisions under delegated routine-policy authority (2026-10-12)

- Add provider-neutral audio/timestamp behavior in a new `audio/` package and only
  the minimal pure domain contracts needed for serializable audio assets, word
  timings, and subtitle cues. Do not add real cloud/local inference, downloads, or
  platform speech frameworks.
- Normalize all temporal intervals to nonnegative, end-exclusive `time.Duration`
  values; reject overlaps, blank words, invalid linkage, and out-of-media ranges.
  Preserve existing shot-relative/project-relative conventions rather than creating
  a parallel timestamp coordinate system.
- SRT is a strict, bounded import/export codec over validated subtitle cues. It is
  not an HTML/player renderer or a replacement for future Phase 12 controls.
- Voice and ASR use small context-aware interfaces. A deterministic fixture voice
  provider and local-ASR worker adapter boundary may produce serializable results,
  but no native speech engine is invoked. Trusted runtime worker configuration owns
  executable/argv/environment, not caller-provided generation parameters.
- Audio/video sync is deterministic interval validation/advice against explicit
  expected durations only; it does not alter video, stretch audio, animate mouths,
  or schedule work. Apple TTS remains an explicit unsupported integration seam.
- Copy all maps/slices on ingress/egress; results accompanying dependency errors or
  late callback completion are ignored. No background retries/goroutines, provider
  selection, resource acquisition, or cache/player redesign. Reviews: luna6 then
  astra, distinct from implementation.

Pipeline:

```text
script
→ TTS
→ word timestamps
→ timeline
→ avatar mouth animation
→ subtitles
→ visual emphasis
```

## TODO

- [x] AudioAsset
- [x] VoiceProvider interface
- [x] ASR interface
- [x] word timestamp representation
- [x] SRT support
- [x] subtitle timing
- [x] voice timing
- [x] audio duration validation
- [x] audio/video sync
- [x] timestamp normalization
- [x] timestamp-to-element/event mapping contract
- [x] deterministic mock TTS
- [x] external worker interface
- [x] local ASR provider interface
- [x] Apple TTS integration boundary (explicit unsupported seam)
- [x] tests (final full uncached gate passes)
- [x] Agent review #1 (luna6 — approved after fixes)
- [x] Fix (metadata safety, strict SRT, and cancellation regressions)
- [x] Agent review #2 (astra — approved after fixes)
- [x] Fix (Astra findings resolved; JSON round-trip coverage added)
- [x] Evidence
- [x] Docs
- [x] Commit (`phase(11): implement audio and timestamp pipeline`)

---

# PHASE 12 — TUI CONTROL PLANE

## Goal

Create the OpenCode-like terminal control room.

The TUI must not contain business logic that belongs in core.

## Lead decisions under delegated routine-policy authority (2026-10-12)

- Build a dependency-free terminal control-plane model in a new `tui/` package first:
  explicit state, tabs, commands, event projection, keyboard navigation, errors, and
  cancellation. The package is a testable client over injected core interfaces, not a
  renderer/provider/project mutation implementation.
- Reuse Phase 10 preview read model and Phase 01 stable identities. Preview selection
  is a timestamp/element read state only; review feedback is persisted through the
  existing `review.Store`. No player process, web server, or duplicate mutation path.
- Keep Director/ChangeSet application, review-before-submit behavior, and natural
  language editing out of this phase (Phase 13). The Phase 12 Send-to-AI action is an
  injected request callback only; it never applies a ChangeSet.
- Use context-aware injected interfaces and bounded copied state/event payloads. The
  event bus is best-effort observation, never UI state authority; no uncontrolled
  goroutines, provider execution, resource leasing, or rendering are added.
- Deliver in bounded slices: core state/navigation; event/task/control adapters;
  preview/review-feedback integration; then a minimal stdlib terminal presentation.
  Reviews: astra then terra, distinct from implementation.

## TODO

- [x] TUI application shell
- [x] tabs
- [x] project view
- [x] plan view
- [x] shots view
- [x] timeline view
- [x] assets view
- [x] models view
- [x] logs view
- [x] preview view
- [x] current preview timestamp
- [x] selected visual element
- [x] review feedback capture
- [x] Send to AI action (proposal-only)
- [!] ChangeSet review/apply/cancel (Phase 13 boundary)
- [x] resource status
- [x] event subscription
- [x] task progress
- [x] cancellation
- [x] responsive behavior
- [x] keyboard navigation
- [x] errors
- [x] agent status
- [x] tests where practical
- [x] Agent review #1 (astra approved after fixes)
- [x] Fix
- [x] Agent review #2 (terra approved)
- [x] Fix
- [x] Evidence
- [x] Docs
- [x] Commit (`phase(12): implement TUI control plane`)

## Important

The TUI is a client/control plane.

It should consume state/events instead of owning renderer/provider logic.

---

# PHASE 13 — DIRECTOR CHAT / CHANGESETS

## Goal

Allow natural-language editing of a video project.

## Lead decisions under delegated routine-policy authority (2026-10-12)

- Reuse `domain.ChangeSet`/`Change` but define closed deterministic mutation
  vocabulary and validation in a new `changeset/` package. Chat/feedback adapters
  produce proposals through an injected context-aware IntentProvider; no model,
  vendor, network, or prompt interpreter implementation is added.
- ChangeSets are optimistic proposals bound to a canonical project hash. Apply only
  explicit supported mutations against copied authoritative project state; reject
  stale/cancelled/invalid proposals atomically. Review mode defaults on; automatic
  mode means apply a previously validated proposal, never auto-accept provider prose.
- ReviewFeedback is converted through the same intent/proposal path with its stable
  project/shot/element/timestamp context. No separate visual mutation system.
- Affected-shot calculation reuses Phase 10 root-plus-downstream invalidation. Cache
  invalidation is advisory output only; no rendering/cache deletion/provider/resource
  execution is authorized. Audit records bounded core codes and IDs, not prompts/raw
  errors/payloads.
- Reviews: luna then luna6. Phase 14 starts only after the Phase 13 full gate and one
  local commit; real local provider execution/install/model download remains out of
  scope without separate authorization.

Pipeline:

```text
User
→ Director Chat
→ Intent
→ ChangeSet
→ Review/Apply
→ Invalidate
→ Rebuild
```

## TODO

- [x] chat request model
- [x] intent model
- [x] ChangeSet model
- [x] visual review feedback input
- [x] element/timestamp-aware intent
- [x] preview changes (advisory invalidation output)
- [x] Apply
- [x] Cancel
- [x] validation
- [x] affected-shot calculation
- [x] cache invalidation (advisory only)
- [x] resource impact calculation (explicit unknown)
- [x] review mode
- [x] automatic mode (validated proposals only)
- [x] audit trail
- [x] tests
- [x] Agent review #1 (luna approved after fixes)
- [x] Fix
- [x] Agent review #2 (luna6 approved)
- [x] Fix
- [x] Evidence
- [x] Docs
- [x] Commit (`phase(13): implement director chat and changesets`)

## Visual editing loop

The Director/ChangeSet system must accept both normal natural-language requests and feedback captured from the preview player.

Target:

```text
Preview
→ selected element / timestamp
→ ReviewFeedback
→ Director / Intent
→ ChangeSet
→ Review or Apply
→ affected-shot calculation
→ cache invalidation
→ incremental rebuild
→ Preview
```

Do not make this Claude-specific. The user-facing action should be conceptually `Send to AI`, while the configured Director/provider decides which model handles the request.

The visual player is an inspection surface. It must not become the source of truth.

A request such as:

```text
At 00:13.42, move this subtitle 0.5 seconds earlier.
```

should retain the structured context:

```text
project
shot
element
timestamp
user request
```

so the Director does not need to infer the target from pixels.

The implementation should prefer deterministic element identity supplied by the renderer/Remotion integration over computer vision or LLM guessing.

## Review mode

Eventually support:

```text
Review Before Submit = OFF

→ execute automatically
```

and:

```text
Review Before Submit = ON

→ show plan/changes/cost/time
→ Approve
→ Edit
→ Cancel
```

---

# Visual Review / Editing Cross-Phase Invariant

This capability is intentionally distributed across existing phases rather than becoming a new phase.

The minimum architectural chain is:

```text
Phase 01  stable project/shot/timeline/element identity
Phase 03  renderer emits inspectable element metadata
Phase 10  preview + cache + incremental rebuild
Phase 11  timestamps can map to temporal events
Phase 12  TUI exposes the review/control surface
Phase 13  feedback becomes Intent / ChangeSet
```

Both Web and TUI clients must consume the same core contracts.

### Required invariants

1. Project/Timeline remains the source of truth.
2. Rendered MP4 is an artifact, not authoritative state.
3. Visual elements have stable IDs whenever their identity is known by the renderer.
4. User feedback carries timestamp and target context whenever available.
5. AI proposes intent/changes; deterministic core code validates and applies them.
6. Review Before Submit semantics are shared with normal Director editing.
7. Only affected shots should be invalidated/rebuilt where dependency information permits.
8. The feature must not be coupled to Claude or any specific AI provider.
9. The TUI and Web preview must not implement separate mutation/business-logic paths.
10. Computer vision is a fallback for cases where deterministic renderer metadata cannot identify the target; it is not the default mechanism.

---

# PHASE 14 — LOCAL AI PROVIDERS

## Goal

Add real local providers behind the established interfaces.

Potential providers:

```text
InvokeAI
FreeVideo
OpenMotion
Wan
Qwen ASR
Whisper/faster-whisper
Gemma
Qwen
Ollama
```

Do not assume every provider will run efficiently on M1 Pro 16GB.

Provider capability declarations must include hardware requirements.

## Lead decisions under delegated routine-policy authority (2026-10-12)

- Implement safe local-provider adapters in a new `localprovider/` package over the
  existing provider/model/worker contracts. Adapters declare stable capabilities and
  use injected cooperative worker/model seams; they do not install, download, invoke,
  or pretend to benchmark real local models.
- Provider configuration uses trusted runtime CommandSpec/model profile facts, never
  request-supplied executables, argv, working directories, or environment. Preserve
  existing resource-manager/model-manager authority and worker protocol bounds.
- Include deterministic fixture adapters for generation/decision/ASR capability paths,
  model lifecycle handoff, cancellation, failure mapping, and metadata provenance. No
  provider name branches in core; descriptive local provider IDs are configuration.
- Preview generation means an explicit draft Quality request through the same worker
  boundary, not actual model generation. Mac compatibility documentation names Metal
  only as declared capability metadata, never a detected or guaranteed runtime.
- No model weights, package installs, cloud calls, paid operations, background polling,
  automatic model loading, or arbitrary process execution. Reviews: terra then luna.

## TODO

- [x] provider capability registry entries
- [x] local worker protocol (injected bounded worker seam)
- [x] process lifecycle (caller-owned worker lifecycle handoff)
- [x] model lifecycle (caller-owned model manager handoff)
- [x] resource leasing (caller-owned resource authority)
- [x] preview generation (draft request boundary only)
- [x] cancellation
- [x] failure handling
- [x] metadata
- [x] validation
- [x] provider-specific tests (deterministic fixtures)
- [x] Mac compatibility notes (declared Metal metadata only)
- [x] Agent review #1 (terra approved)
- [x] Fix (documentation scope closure)
- [x] Agent review #2 (luna approved)
- [x] Fix (README/evidence status closure)
- [x] Evidence
- [x] Docs
- [x] Commit (`phase(14): add local provider adapters`)

---

# PHASE 15 — CLOUD PROVIDERS

## Goal

Add cloud generation behind the same provider contracts.

## Lead decisions under delegated routine-policy authority (2026-10-12)

- Add a new `cloudprovider/` package with a credential source abstraction that returns
  opaque runtime credentials only to an injected transport. Credentials never appear
  in serializable requests/results/errors/audit records and are never written to disk.
- Define an explicit asynchronous job protocol: submit, bounded status poll, optional
  cancellation, and verified result download. All network behavior is injected through
  a deterministic test transport; no vendor endpoint, credential lookup, paid call,
  cloud SDK, or background polling loop is added.
- Reuse Provider capabilities/routing metadata and require explicit cost/latency
  declarations. Routing/admission remains external; a cloud adapter never converts
  estimates into spending authority. Rate limits/retries are bounded caller-driven
  policy, respecting Retry-After but never retrying invalid/auth/permanent responses.
- Download writes only to caller-provided controlled staging through an injected sink,
  verifies declared SHA-256 and size, then returns safe logical result metadata. It
  performs no cache publish/render/validation; existing Phase 09/10 boundaries own it.
- No automatic polling goroutines, persistent secrets, provider-specific model names,
  installation, paid generation, or external networking. Reviews: luna then luna6.

Potential providers:

```text
Veo
Runway
future providers
```

## TODO

- [x] credential abstraction (opaque injected source)
- [x] configuration
- [x] request serialization
- [x] async jobs
- [x] polling (caller-driven bounded)
- [x] cancellation where supported
- [x] retry
- [x] rate limits
- [x] cost metadata
- [x] latency metadata
- [x] provider status
- [x] result download
- [x] checksum
- [x] validation
- [x] secret-safe logging
- [x] tests
- [x] Agent review #1 (luna approved)
- [x] Fix (no findings)
- [x] Agent review #2 (terra approved; distinct model replacement for unavailable distinct Luna6)
- [x] Fix (no findings)
- [x] Evidence
- [x] Docs
- [x] Commit (`phase(15): add cloud provider contracts`)

---

# PHASE 16 — END-TO-END AUTONOMOUS PIPELINE

## Goal

Connect everything.

## Lead decisions under delegated routine-policy authority (2026-10-12)

- Add a new `pipeline/` package with one synchronous context-aware orchestrator. It
  consumes injected Director, Shot Intelligence, preview Coordinator, checkpoint, and
  optional approved executor seams; it never chooses/adopts a provider or spends money.
- Deterministic-only is the required executable path: prompt → plan → sequential shot
  analysis → cached preview rendering/validation → timeline composition → verified MP4.
  Any non-deterministic/local/cloud/mixed strategy is returned as a typed pending or
  externally-approved execution requirement, not silently generated.
- Resume reads Phase 10 checkpoint/artifact evidence and rebuilds only pending or
  invalidated shots. Partial failure retains completed artifact/checkpoint evidence;
  cancellation returns no fabricated final result. Retry is delegated only through an
  injected bounded policy/seam and never retries permanent/cancelled work.
- Approval gates are explicit: caller must enable approved execution and provide a
  pre-admitted executor for local/cloud strategies. No automatic paid/cloud action,
  model load, provider route mutation, or scheduler/goroutine is added.
- Preserve Phase 10 cache keys and artifact integrity; compose only verified returned
  shot media. Emit optional existing events as best-effort immutable observations.
  Tests use deterministic render fixtures and never require a cloud/local model.
- Reviews: terra then luna. Performance baseline is measured deterministic fixture
  timing only, not a hardware/model benchmark.

Target:

```text
Prompt
→ Director
→ VideoPlan
→ Shot Intelligence
→ Decision Engine
→ Provider Router
→ Resource Planner
→ Generation
→ Validation
→ Timeline
→ Render
→ MP4
```

## TODO

- [x] end-to-end orchestration
- [x] deterministic-only path
- [x] local-AI path (typed pending approval, no execution)
- [x] cloud path (typed pending approval, no execution)
- [x] mixed-provider path (typed pending approval, no execution)
- [x] preview path
- [x] approval path (explicit pending gate)
- [x] cancellation
- [x] resume
- [x] retry (bounded explicit retryable seam)
- [x] partial failure recovery
- [x] incremental rebuild
- [x] full project integration test (guarded real FFmpeg MP4 composition)
- [x] sample project (deterministic fixture)
- [x] performance baseline (deterministic fixture timing)
- [x] Agent review #1 (terra approved after fixes)
- [x] Fix (composition/output/retry/invalidation corrections)
- [x] Agent review #2 (luna approved)
- [x] Fix (no findings)
- [x] Evidence
- [x] Docs
- [x] Commit (`phase(16): implement end-to-end autonomous pipeline`)

## Required milestone

A real user prompt should be capable of producing a valid video artifact through the full system.

---

# PHASE 17 — PERFORMANCE / RELIABILITY / PACKAGING

## Goal

Make the system reliable enough for serious use.

## Lead decisions under delegated routine-policy authority (2026-10-12)

- Establish reproducible Go benchmark/stress baselines before changing hot paths.
  Report deterministic fixture timing/allocation results only; no model/provider
  benchmark is fabricated or requires loading models.
- Add a small dependency-free `observability/` package for bounded structured logs,
  in-process metrics, and trace correlation records. It is an injected sink/source,
  not Datadog/cloud telemetry, a durable log, or a secret-bearing request recorder.
- Improve the CLI only as a stable stdlib command shell exposing version/help, config
  validation, and TUI snapshot presentation. No daemon, interactive raw terminal loop,
  credentials, automatic generation, or hidden configuration discovery.
- Packaging consists of reproducible build metadata, documented macOS/Linux cross-build
  commands, and checked version injection. No binaries, release archive, installer, or
  publication is created in the repository.
- Reliability work must be test/measurement-led: bounded stress/race tests, large
  deterministic project coverage, cancellation/partial recovery invariants, and cache
  effectiveness counters. Resource scheduler policy is observed but not retuned without
  an evidenced bottleneck. Reviews: astra then terra.

## TODO

- [x] profiling
- [x] memory profiling
- [x] CPU profiling
- [x] concurrency profiling
- [x] resource scheduler tuning (retained after evidence)
- [x] startup optimization (no unsupported optimization claimed)
- [!] provider startup timing (unavailable without provider runtime)
- [!] model load timing (unavailable without model load)
- [x] cache effectiveness
- [x] failure recovery
- [x] structured logs
- [x] metrics
- [x] tracing strategy
- [x] deterministic test suite
- [x] race tests
- [x] stress tests
- [x] large project test
- [x] packaging
- [x] macOS binary (cross-build verified)
- [x] Linux binary (cross-build verified)
- [x] configuration
- [x] CLI
- [x] TUI startup (one-shot snapshot presentation)
- [x] Agent review #1 (astra high approved after evidence fixes)
- [x] Fix
- [x] Agent review #2 (terra high approved after portability fixes)
- [x] Fix
- [x] Evidence
- [x] Docs
- [x] Commit (`phase(17): add reliability and packaging`)

Do not optimize based on intuition alone.

Profile first.

---

# PHASE 18 — PUBLIC REPOSITORY / RELEASE READINESS

## Goal

Prepare the repository for public GitHub release.

Target:

```text
github.com/manish96170/<repo>
```

## TODO

- [x] final architecture review
- [x] security review
- [x] dependency review
- [x] license review
- [x] README
- [x] installation
- [x] quickstart
- [x] architecture diagram (textual architecture in README/ARCHITECTURE)
- [x] provider documentation
- [x] local hardware limitations
- [x] configuration docs
- [x] development docs
- [x] contributing guide
- [x] issue templates if useful (SECURITY policy provided; no issue template needed)
- [x] examples
- [x] sample project
- [x] sample provider
- [x] sample deterministic render
- [x] remove secrets
- [x] remove private data
- [x] remove large artifacts
- [x] check git history
- [x] build matrix
- [x] final test suite
- [x] final review #1 (astra high approved after fixes)
- [x] fix
- [x] final review #2 (terra high approved after fixes)
- [x] fix
- [x] final security review
- [x] final docs
- [x] release commit (`phase(18): prepare public release readiness`)

Do not publish until repository hygiene is confirmed.

---

# 21. Parallel Agent Work

Parallel work is encouraged only when boundaries are clear.

Good parallelization:

```text
Agent A → contracts
Agent B → documentation
Agent C → test strategy
Agent D → provider research
```

Bad parallelization:

```text
Agent A → edits Project model
Agent B → edits same Project model
Agent C → changes same interfaces
```

When multiple agents modify overlapping code, use Git worktrees.

Example:

```text
~/video-factory/
~/worktrees/video-foundation/
~/worktrees/video-renderer/
~/worktrees/video-provider/
```

Never assume two agents can safely edit the same working tree concurrently.

---

# 22. Agent Roles

The main session may delegate:

## Architect

Reviews boundaries, interfaces, dependencies, long-term extensibility.

## Implementer

Writes the requested phase code.

## Code Reviewer

Finds correctness and maintainability issues.

## Performance Reviewer

Looks for:

- unnecessary allocations
- memory growth
- excessive serialization
- blocking operations
- bad concurrency
- resource leaks
- unnecessary processes
- poor caching

## Reliability Reviewer

Looks for:

- cancellation failures
- deadlocks
- races
- retry storms
- orphaned workers
- corrupt state
- partial failure behavior

## Security Reviewer

Looks for:

- command injection
- unsafe process execution
- secret leakage
- path traversal
- untrusted provider data
- unsafe file writes
- credential exposure

## Test Engineer

Finds missing tests and creates deterministic fixtures.

---

# 23. Main Session Agent Protocol

For every phase:

## Step A — Understand

```text
Read:
- this PHASES.md
- AGENTS.md
- ARCHITECTURE.md
- DECISIONS.md
- current phase docs
- recent commits
```

## Step B — Inspect

Run:

```text
git status
git log --oneline -n 10
find relevant source directories
```

Do not assume the repository matches the roadmap exactly.

## Step C — Plan

Create a short internal phase plan.

Do not rewrite the entire roadmap.

## Step D — Implement

Delegate where useful.

Agents should receive:

- phase goal
- exact scope
- constraints
- expected files
- tests required
- non-goals

## Step E — Self-review

The main session reviews:

```text
git diff
```

and the architecture impact.

## Step F — Test

At minimum:

```text
go test ./...
go test -race ./...
go vet ./...
go build ./...
```

Use narrower commands during iteration, but the phase gate requires the relevant final checks.

## Step G — Review agent #1

Use a different model from the implementation agent.

Give it:

```text
- phase goal
- commit/diff
- architecture docs
- test results
- explicit request for adversarial review
```

Reviewer must not modify code unless explicitly asked.

## Step H — Fix

Main session decides which findings are valid.

Do not blindly implement all reviewer suggestions.

## Step I — Re-test

Run relevant tests again.

## Step J — Review agent #2

Use another different model.

Review the updated state.

## Step K — Fix and re-test

Resolve valid findings.

## Step L — Evidence

Write review files.

## Step M — Documentation

Update only documentation affected by implementation.

## Step N — Checklist

Mark TODOs complete.

## Step O — Commit

Create the phase commit.

## Step P — Verify

Run:

```text
git status
git log --oneline -n 3
```

Ensure the working tree is clean unless there is an explicitly documented next-phase change.

---

# 24. Review Prompt Template

Use this when invoking a reviewer.

```text
You are the independent reviewer for Phase <N> of the Video Factory project.

You are NOT the implementation agent.

Review the current repository state and the phase diff against:
- PHASES.md
- AGENTS.md
- ARCHITECTURE.md
- DECISIONS.md
- phase requirements

Review for:

1. correctness
2. architecture
3. API boundaries
4. hidden coupling
5. concurrency
6. cancellation
7. resource lifecycle
8. memory usage
9. error handling
10. tests
11. race conditions
12. portability
13. macOS/Apple Silicon compatibility where relevant
14. future provider extensibility
15. Lambda/control-plane compatibility
16. security
17. documentation accuracy

Do not make changes.

Return:

- BLOCKER findings
- HIGH findings
- MEDIUM findings
- LOW findings
- positive observations
- missing tests
- architecture concerns
- exact file/line references
- recommended fixes

If no findings exist, say exactly why you believe the implementation satisfies the phase requirements.

This review will be saved as formal review evidence.
```

---

# 25. Implementation Agent Prompt Template

```text
You are implementing Phase <N> of the Video Factory project.

Read PHASES.md first.

Scope:
<phase scope>

Rules:
- Follow the architecture in PHASES.md.
- Do not implement future-phase functionality unless required for a clean boundary.
- Keep core in Go.
- Do not embed Python.
- Keep provider-specific code behind interfaces.
- Use context.Context for cancellable operations.
- Prefer small interfaces.
- Avoid global mutable state.
- Write tests with the implementation.
- Do not commit unless explicitly instructed.
- Do not push.
- Do not introduce secrets.
- Do not silently change architecture.

Before finishing:
- run relevant tests
- inspect git diff
- report files changed
- report tests run
- report assumptions
- report unresolved issues
```

---

# 26. Context-Rollover Handoff Template

When context reaches approximately 80%, create:

```text
docs/handoffs/next-session-phase-XX.md
```

Use:

```markdown
# Next Session Handoff

## Current phase

Phase XX — <name>

## Current status

<summary>

## Completed

- ...

## Incomplete

- ...

## Current commit

<hash>

## Working tree

<clean/dirty + files>

## Tests

- ...

## Reviews

### Reviewer 1
- model:
- status:
- findings:

### Reviewer 2
- model:
- status:
- findings:

## Documentation updated

- ...

## Known issues

- ...

## Exact next actions

1. ...
2. ...
3. ...

## Do not redo

- ...

## Next-session prompt

Continue Phase XX from this handoff.

First:
1. Read PHASES.md.
2. Read this handoff.
3. Inspect git status and recent commits.
4. Verify the current implementation before changing anything.
5. Continue only from the incomplete TODOs.
6. Do not redo completed work.
7. Preserve the two-review requirement.
8. Complete the phase gate and commit the phase.
```

---

# 27. TODO State Rules

Use these states:

```text
[ ] NOT STARTED
[-] IN PROGRESS
[x] DONE
[!] BLOCKED
```

Do not mark an item `[x]` merely because code exists.

It is `[x]` only when verified.

A blocked item must include a reason in the phase notes.

---

# 28. Evidence Requirements

For each phase maintain evidence:

```text
docs/phases/phase-XX.md
```

Recommended content:

```markdown
# Phase XX — <name>

## Goal

## Implemented

## Design decisions

## Files changed

## Tests

## Review 1

## Review 2

## Findings resolved

## Known limitations

## Commit

## Follow-ups
```

This should be concise.

Do not duplicate the entire source code or roadmap.

---

# 29. First Milestone

The first meaningful product milestone is:

```text
Manual VideoPlan
    ↓
3 deterministic shots
    ↓
Deterministic renderer
    ↓
MP4
```

The second milestone:

```text
Prompt
    ↓
Director
    ↓
VideoPlan
    ↓
Deterministic renderer
    ↓
MP4
```

The third milestone:

```text
Prompt
    ↓
Director
    ↓
Shot Intelligence
    ↓
Decision Engine
    ↓
Deterministic / AI Provider
    ↓
Validation
    ↓
MP4
```

The fourth milestone:

```text
TUI
    ↓
Director Chat
    ↓
Plan
    ↓
Preview
    ↓
Approve
    ↓
Final
```

---

# 30. Definition of "Production-Ready Architecture"

The architecture is considered structurally successful when:

```text
A new video provider
```

can be added without changing:

```text
Project model
Timeline model
Director
TUI
Decision Engine
Resource Manager
```

and:

```text
A new flow
```

can be added without rewriting:

```text
Provider Router
Renderer
TUI
Resource Manager
```

and:

```text
A new framing
```

can be added without changing:

```text
Director core
Provider implementations
```

and:

```text
A new model
```

can be registered through capabilities/configuration without embedding model-specific logic throughout the codebase.

---

# 31. Final Quality Principles

The main session must repeatedly ask:

### Is this deterministic?

If yes, prefer deterministic code.

### Is AI actually required?

If no, don't use AI.

### Can we use a cheaper model?

If yes, use the cheaper model.

### Can the work be previewed first?

If yes, preview first.

### Can the result be cached?

If yes, cache it.

### Can only the affected shot be rebuilt?

If yes, do not rebuild the whole video.

### Can the operation exceed memory?

If yes, Resource Manager must prevent it.

### Can the worker be cancelled?

If no, fix the lifecycle.

### Can this provider be replaced?

If no, the abstraction is probably wrong.

### Can another agent understand this code?

If no, simplify/document it.

### Can this package run without a machine-specific environment?

If no, identify whether that coupling is genuinely required.

---

# 32. Current Execution State

The main session must maintain this section as the authoritative high-level progress tracker.

```text
[x] PHASE 00 — Foundation / Contracts
[x] PHASE 01 — Project State / Timeline
[x] PHASE 02 — Event System
[x] PHASE 03 — Deterministic Renderer
[x] PHASE 04 — Flows / Framing / Primitives
[x] PHASE 05 — Director / Video Planning
[x] PHASE 06 — Decision Engine
[x] PHASE 07 — Provider System / Router
[x] PHASE 08 — Model Manager / Resource Planner
[x] PHASE 09 — Validation / Shot Intelligence
[x] PHASE 10 — Preview / Checkpoint / Cache / Visual Review
[x] PHASE 11 — Audio / Timestamp / Temporal Intelligence
[x] PHASE 12 — TUI Control Plane / Visual Review
[x] PHASE 13 — Director Chat / ChangeSets / Visual Editing
[x] PHASE 14 — Local AI Providers
[x] PHASE 15 — Cloud Providers
[x] PHASE 16 — End-to-End Autonomous Pipeline
[x] PHASE 17 — Performance / Reliability / Packaging
[x] PHASE 18 — Public Repository / Release Readiness
```

Phase 05 resume (2026-10-09): implementation is present; baseline build,
vet, unit/integration tests, race tests, and formatting checks pass. The
main session orchestrates bounded agent tasks, independently inspects
diffs/tests, and owns integration, tracker updates, and the phase commit.
User-approved sequential reviewers: `luna`
(`amazon-bedrock/openai.gpt-5.6-luna`), then `astra`
(`amazon-bedrock/us.openai.gpt-6-astra`), verified against the local agent
roster and available-model catalog. Luna review #1 completed with two HIGH
and two MEDIUM findings; a bounded implementation agent fixed them and
an additional interpreter slice-ownership regression with red/green tests.
Luna confirmed approval, and the lead inspected diffs and reran the full
uncached build/vet/test/race gate (PASS). Astra reviewed
that updated state and found one MEDIUM standalone-validation issue and
one LOW test-coverage issue (no BLOCKER/HIGH). A bounded agent fixed
the validation wrapper and strengthened Phase 05 invariant/extension/
serialization/concurrency tests. The lead inspected diffs and reran the
full uncached gate (PASS), then obtained final Luna approval followed by
final Astra approval. No findings remain outstanding. A documentation
agent reconciled evidence/architecture/decisions/historical handoff, and
the lead verified the final full uncached test/race/build/vet gate and
real MP4 milestone (PASS). Final staged diff/whitespace and tracked-file
hygiene checks pass (no matching credential/home-path patterns or
unexpected generated artifacts). Phase 05 completion is recorded by the
single `phase(05): implement director / video planning` commit containing
this update. No automatic push.

Phase 06 completion (2026-10-09): clean baseline at `ed20587`; full uncached
test/race/build/vet gate passes. Read-only design scout completed; the owner
approved the execution policy above and Terra → Luna6 review rotation.
Implementation/docs are delegated with disjoint file ownership. No Phase
07 router, real model adapters, or pipeline integration is in scope. Phase 06
was implemented in bounded tasks; the lead inspected
production diffs and representative tests, and the initial full uncached
test/race/build/vet gate passes. A lead self-review regression for cancellation
during terminal audit publication failed before the fix and passes afterward:
the method now rechecks parent context after terminal publication, preserving
one attempted terminal record. Lead retest of full uncached unit/integration,
race, build, vet, and formatting passes. Terra formal review #1 completed
with two MEDIUM audit findings: one accepted provider-attribution fix and
one start-time cancellation claim disproven by regressions before production
edits and explicitly withdrawn by Terra. The bounded agent fixed confirmed
provider attribution with red/green tests; Terra confirmed approval, and
the lead inspected/retested the full uncached gate (PASS). Luna6 independent
review #2 completed with one MEDIUM post-call identity/error-path finding and
one LOW late-resolver-timeout coverage gap. Both are assigned to the bounded
implementation agent and now resolved: post-call ID checks precede the error
branch, with red/green provenance tests; late successful resolver timeout is
pinned by direct coverage. Lead inspected/retested full uncached gate (PASS),
then Terra final approval followed by Luna6 final approval. No findings remain.
A documentation agent closed evidence/ADR-019/as-built docs; the lead inspected
them and verified the final post-confirmation full uncached test/race/build/vet
gate and ten repeated decision runs (PASS), with clean formatting. Final staged
diff/whitespace and all 109 tracked-file hygiene checks pass (no matching
credential/private-path patterns or unexpected generated artifacts).
Completion is recorded by the single `phase(06): implement decision engine`
commit containing this update. Phase 06 is DONE.
No automatic push.

Phase 07 completion (2026-10-09): continuing under the owner's remaining-roadmap
authorization, from clean commit `a6325b5`. Read-only design scout is inspecting
provider/router contracts and policy gaps; fresh full uncached baseline gate
passes. Owner delegated conservative routine-policy decisions and named-model
rotation across remaining phases; lead Phase 07 decisions are recorded above.
Implementation/docs were delegated with disjoint file ownership. Implementation
is present and production diffs are lead-inspected; initial full uncached
unit/integration/race/build/vet gate passes. Lead self-review corrected discovery
error classification: stable adapter errors with empty capability results must
not become identity mismatches; actual adapter-ID drift still takes precedence.
Six regressions failed before the correction and pass afterward; the lead
inspected the fix/tests and reran the full uncached gate (PASS). Luna6 formal
review #1 found no production defect and one LOW clone-oracle test weakness.
The bounded agent fixed it; original test passed a no-copy mutant, new test
failed it, then production was restored and independently checked by the lead.
Luna6 confirmed closure; full lead uncached gate passes. Astra independent
review #2 found no production defect and one LOW Unwrap-oracle weakness.
The bounded agent strengthened cause isolation: original aliased-Unwrap mutant
passed, strengthened assertions failed all six cases; production restored.
Lead inspected/retested full uncached gate (PASS), then Luna6 final approval
followed by Astra final approval. No findings remain. Documentation closeout is
lead-inspected; final post-confirmation full uncached test/race/build/vet and
ten provider/decision runs pass, formatting clean. Final staged diff/whitespace
and all 120 tracked-file hygiene checks pass (no matching credential/private-path
patterns or unexpected generated artifacts). Completion is recorded by the
single `phase(07): implement provider registry and routing` commit containing
this update. Phase 07 is DONE; Phase 08 follows automatically under §0.2.
No automatic push.

Phase 08 start (2026-10-10): continuing automatically under §0.2 from clean
commit `dbc5ace`; Phase 07's final full gate validates the unchanged baseline.
A read-only design scout is inspecting existing resource/model/worker contracts;
fresh scoped resource/provider/decision checks pass. Routine conservative
policy is recorded above before implementation. Planned sequential reviewers:
terra (`amazon-bedrock/openai.gpt-5.6-terra`), then luna
(`amazon-bedrock/openai.gpt-5.6-luna`), verified in roster/catalog. No real model
downloads, paid generation, or infrastructure actions are authorized by this
phase. Shared-contract/resource core is implemented and API shapes are frozen;
lead inspection and fresh post-correction scoped unit/race/vet checks pass,
including targeted high-water retention. History rejects new keys at capacity
without forgetting peaks; shared API shapes remain frozen. ADR-021/draft evidence
and `docs/handoffs/next-session-phase-08.md` checkpoint exist. Disjoint model owner
`ses_ed959092cffegR0uDf06aQ0i0S` and worker owner
`ses_ed958fec0ffeJ5Tsea4RGVxRBn` are implementing their packages against these
contracts. Both formal reviews and phase completion remain pending.

Worker tranche is implemented; the lead inspected runner/protocol/monitor/platform
code and independently ran worker/resource/domain unit tests, worker race tests,
and vet (PASS). Typed-nil monitor constructor validation is corrected with
pointer/function red/green tests; lead inspected and independently reran worker
unit/race/vet (PASS). A supplementary read-only Astra worker reliability audit
completed with 3 MEDIUM protocol/monitoring findings and 1 LOW assertion weakness
(see `docs/reviews/phase-08-worker-audit-astra.md`). Worker owner corrected all
four with red/green regressions; lead scoped unit/race/vet rerun passes and
Astra supplemental confirmation approved on 2026-10-11 with independent
regressions passing. This audit does not replace formal Terra → Luna reviews.
Original model owner was canceled; five existing production files are preserved.
Lead inspected them and confirmed three bounded lifecycle tests pass with
independent model unit/race/vet checks. Production remains unchanged except
formatting; no rewrite. Two test-only owners now add missing coverage in disjoint
files: `ses_ed85889b0ffeyKfTmFiWxaXgu4` owns `model/lifecycle_test.go`, and
`ses_ed858898fffeCcwx9ONvCr0PkD` owns `model/ownership_test.go`. They may not
edit production; any failures require a single lead-assigned fix owner.
Both bounded test-only owners completed five focused tests each (PASS), with
no production fixes needed. Lead inspected source/representative assertions
and ran full uncached repository unit/integration tests, race tests, build,
vet, formatting, and whitespace checks (PASS). Code is frozen; no writer remains.
Formal Terra review #1 approved with no code defect and one LOW stale-gate
documentation finding, now corrected. Terra independently reran full gate (PASS).
Sequential Luna review #2 approved with no BLOCKER/HIGH/MEDIUM findings and
independent tests/race/vet/Linux+Windows build/diff checks passing. Both formal
reviews are complete; supplementary worker findings remain closed. Final lead
full uncached unit/integration/race/build/vet rerun also PASS, with clean
formatting/whitespace. Evidence/architecture/ADR-021 and checkpoint are reconciled;
final staged diff/whitespace and 157-tracked-file hygiene scan pass (no matching
credential/private-path patterns or unexpected generated artifacts). Completion
is recorded by the single `phase(08): implement resource and model management`
commit containing this update. Phase 08 is DONE; Phase 09 follows automatically
under §0.2. No automatic push. Linux runtime coverage remains a documented
follow-up, not a completed verification claim.

Historical Phase 09 start/checkpoint (2026-10-11; superseded on resume): automatic continuation from clean commit `61e078b`.
Phase 08 final full gate validates the unchanged baseline; fresh project/provider/
render checks pass. Read-only design scout completed, and conservative
lead policy is recorded above. Validation core precedes shot intelligence after
API freeze; formal reviewers astra (`amazon-bedrock/us.openai.gpt-6-astra`) then
terra (`amazon-bedrock/openai.gpt-5.6-terra`) verified in roster/catalog.
Validation core is implemented; lead inspected quality/native/security/probe
changes and independently reran validation/render unit/race/vet (PASS). Three
bounded self-review refinements are complete: domain.Quality reuse, nil-context
guards, and permanent-failure retry precedence, all with red/green regressions
and fresh lead scoped checks (PASS). ADR-022/draft evidence exists; validation API
is frozen. Intelligence source and four focused tests are implemented; audio-intent,
binding provenance/locality, and validation severity gaps were corrected and scoped
unit/race/vet passed before additional acceptance coverage. Test-only owner
`ses_ed65f4c94ffeTZ6HrDMiQvAj6g` completed five acceptance functions and found
HIGH P09-A1 caller-snapshot aliasing and MEDIUM P09-A2 retained router-policy aliasing.
Lead independently reproduced normal and race ownership failures; checkpoint
full build/vet PASS. Both bugs are UNFIXED;
no active writers. Phase 09 full test/race gate has not passed. Formal Astra → Terra
reviews have NOT STARTED. Owner requested context rollover at about 88%; docs and
`docs/handoffs/next-session-phase-09.md` checkpoint preserve exact continuation.
Phase 09 remains IN PROGRESS; no commit/push and no later-phase implementation.

Phase 09 completion (2026-10-11): the bounded Luna implementation corrected the
acceptance-found P09-A1 request snapshot and P09-A2 router-policy aliasing defects,
with the original regression retained RED→GREEN. Astra R1 found four in-scope MEDIUM
boundary issues (planner callback isolation, native permanent-error precedence,
decimal FPS frame rounding, and router-result sanitization); each was corrected with
focused regressions and Astra approved. Terra R2 found no production/test defect and
approved after formal-evidence and historical-checkpoint reconciliation. The final
lead uncached `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go build ./...`
gate passed; formatting, staged diff, and hygiene checks passed. Completion is the
single local `4326f6c` commit, `phase(09): implement validation and shot intelligence`.
No push. Phase 10 begins under §0.2 in a planning-only checkpoint.

---

# 33. How the Main Session Should Start

On the first session, do NOT immediately start coding.

First perform:

```text
1. Inspect repository.
2. Determine whether it is empty/new or contains existing work.
3. Read existing docs.
4. Read PHASES.md.
5. Establish the Go module.
6. Create/update AGENTS.md.
7. Create/update ARCHITECTURE.md.
8. Create/update DECISIONS.md.
9. Create/update ROADMAP.md.
10. Confirm Phase 00 scope.
11. Delegate implementation where useful.
12. Implement Phase 00.
13. Run tests.
14. Review with two different agents/models.
15. Resolve findings.
16. Write evidence.
17. Mark Phase 00 complete.
18. Commit Phase 00.
19. Only then start Phase 01 if context and quality allow.
```

The main session may complete multiple phases in one context if it remains safe.

Do not sacrifice review quality merely to finish more phases.

---

# 34. Session Completion Protocol

Before ending any session:

```text
1. Check current phase.
2. Check PHASES.md TODO state.
3. Inspect git status.
4. Inspect recent commits.
5. Run tests appropriate to current state.
6. Determine whether documentation is stale.
7. Update documentation if necessary.
8. If context is near 80%, create handoff.
9. If a phase is complete, finish both reviews and commit it.
10. Leave a clean working tree whenever practical.
11. Produce the next-session prompt if work remains.
```

The final session message should state:

```text
Completed:
- ...

Tests:
- ...

Reviews:
- ...

Commit:
- ...

Remaining:
- ...

Next session:
- ...
```

---

# 35. The Golden Rule

Do not optimize for:

```text
number of files created
number of phases completed
number of agents used
amount of code
```

Optimize for:

```text
correct architecture
small understandable components
deterministic behavior
safe resource usage
fast iteration
reliable cancellation
provider independence
incremental builds
strong tests
independent review
reproducibility
```

The desired system is:

```text
small core
+
strong contracts
+
deterministic rendering
+
pluggable providers
+
resource-aware execution
+
cheap previews
+
incremental rebuilds
+
independent reviews
+
excellent TUI
```

That is the Video Factory.
