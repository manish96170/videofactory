# Phase 16 — End-to-End Autonomous Pipeline

## Implemented

`pipeline` synchronously coordinates the deterministic path from prompt through
Director, Shot Intelligence, verified cached preview rendering, checkpoint recovery,
and controlled atomic FFmpeg final MP4 composition. It composes only verified artifact
media and validates the final result. Partial/cancelled runs preserve prior completed
checkpoint/artifact evidence; retry is bounded and explicit.

Local, cloud, and mixed strategies return typed pending approval requirements. They do
not invoke a provider, load a model, download data, or create a paid operation.

## Reviews and tests

Terra found real-composition/output-safety, retry, and invalidation-event gaps; all were
fixed and re-approved. Luna approved the resulting implementation. Final full test,
race, vet, build, and guarded real-FFmpeg composition gates pass.
