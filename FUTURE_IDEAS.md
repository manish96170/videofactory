# Future Ideas

Non-binding ideas recorded during implementation. They are not current-phase scope or
commit criteria.

- Add a Phase 12 client that consumes the Phase 10 preview read model for playback,
  scrubbing, and element selection without duplicating project mutation logic.
- Consider a future measured peak-memory reporter at the renderer/worker boundary;
  Phase 10 intentionally represents unavailable measurement as nil.
