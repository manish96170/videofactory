package domain

// ValidationStatus is the outcome of validating a shot/asset/render
// (PHASES.md section 9 / Phase 09).
type ValidationStatus string

const (
	ValidationPassed  ValidationStatus = "passed"
	ValidationFailed  ValidationStatus = "failed"
	ValidationWarning ValidationStatus = "warning"
)

// ValidationResult records the outcome of validating a target entity.
type ValidationResult struct {
	ID               ID               `json:"id"`
	TargetID         ID               `json:"targetId"`
	Status           ValidationStatus `json:"status"`
	FailureClass     string           `json:"failureClass,omitempty"`
	RetryRecommended bool             `json:"retryRecommended,omitempty"`
	Details          map[string]any   `json:"details,omitempty"`
}
