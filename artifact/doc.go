// Package artifact provides pure content-key and invalidation calculations.
// It owns the cache-key encoding, not project state, rendering, or storage.
//
// Callers project resolved, pixel-affecting configuration into KeyInput. This
// boundary deliberately does not accept whole projects, generation results,
// arbitrary JSON, output paths, correlation IDs, wall-clock times, measurements,
// events, or review feedback. Source files (including fonts) and direct dependency
// artifacts are represented by caller-verified SHA-256 content digests. The
// caller owns completeness of that projection and digest verification; this
// package performs no I/O and cannot discover omitted render inputs.
//
// Functions snapshot all consumed mutable input and return independently owned
// output. Callers must keep input stable during a call (including Clone); copies
// do not make concurrent mutation of a caller's maps or slices safe. Calls with
// stable inputs can run concurrently, without shared state or callbacks.
package artifact
