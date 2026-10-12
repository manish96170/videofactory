// Package changeset validates and applies optimistic, deterministic project edits.
//
// A Proposal is untrusted serializable input. Validate produces an opaque,
// process-local Validated handle; only that handle can be applied or cancelled.
// Apply returns a fully copied project or no result. It does not persist it:
// callers must serialize their own read/apply/save transaction (FileStore is
// last-writer-wins). Hashes cover all persisted project fields, including times,
// asset locations and visual metadata, rather than just pixel-affecting fields.
// Times are UTC without monotonic data, object keys are sorted, empty aggregate
// slices normalize to nil, slice order is significant, and numbers use
// encoding/json's deterministic representation. Extension values are copied
// without converting integer types through float64. The serialized project size
// limit is an admission bound, not a hard allocation or memory-containment limit.
// Project timestamps are preserved; the caller owns persistence timestamps.
//
// The closed mutation vocabulary deliberately excludes asset paths, executables,
// provider/model/resource settings, arbitrary properties and timeline retiming.
// Text/color/font-size edits target a primitive ID plus its explicit shot ID;
// renderer element IDs currently equal primitive IDs. Matching timeline metadata
// is refreshed, but metadata never authorizes a mutation by itself. Invalidation
// is advisory: no cache deletion, rendering, resource leasing or execution occurs.
// Resource impact is explicitly unknown rather than an invented RAM/cost estimate.
//
// Inputs must not be mutated during a call. Providers receive independent copies,
// must obey context cooperatively, and must not mutate returned values after
// return. Engine calls are concurrent-safe when the injected provider is safe.
// No background goroutines, retries, clocks or durable audit storage are used.
package changeset
