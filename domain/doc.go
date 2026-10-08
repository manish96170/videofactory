// Package domain defines the Video Factory's core domain contracts: the
// stable data model shared by every phase (PHASES.md section 5).
//
// This package intentionally contains no business logic beyond small,
// pure helper methods on its own types. Providers, routing, resource
// enforcement, and rendering live in their own packages and are
// introduced in later phases; they depend on these contracts, not the
// other way around.
//
// Extension fields typed map[string]any (e.g. Primitive.Properties,
// Change.Payload, DecisionRequest.Input, Asset.Metadata,
// GenerationRequest.Params, GenerationResult.Metadata,
// ValidationResult.Details, VisualElement.EditableProperties) hold
// JSON-compatible values only. encoding/json decodes all JSON numbers in
// such fields as float64, which loses precision for integers outside
// float64's exact range; see DECISIONS.md ADR-008 for the accepted
// tradeoff and what later phases must do if they need lossless numbers
// or typed payloads.
package domain
