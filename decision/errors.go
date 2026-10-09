package decision

import "errors"

var (
	ErrInvalidConfig     = errors.New("decision: invalid configuration")
	ErrInvalidRequest    = errors.New("decision: invalid request")
	ErrInvalidProvider   = errors.New("decision: invalid provider")
	ErrIdentityMismatch  = errors.New("decision: provider identity mismatch")
	ErrDuplicateProvider = errors.New("decision: duplicate provider")
	ErrProviderNotFound  = errors.New("decision: provider not found")
	ErrMalformedResult   = errors.New("decision: malformed result")
	ErrProviderFailure   = errors.New("decision: provider failure")
	ErrExhausted         = errors.New("decision: providers exhausted")
	ErrUnsupportedKind   = errors.New("decision: unsupported kind")
	// ErrTransient explicitly marks an adapter error as retryable. Unmarked
	// errors (including adapter cancellation with an active parent) are not.
	ErrTransient = errors.New("decision: transient failure")
)

// Outcome is an engine-owned, nonsecret code used in errors and audit records.
type Outcome string

const (
	OutcomeStarted          Outcome = "started"
	OutcomeValid            Outcome = "valid"
	OutcomeInvalidConfig    Outcome = "invalid_config"
	OutcomeInvalidRequest   Outcome = "invalid_request"
	OutcomeInvalidProvider  Outcome = "invalid_provider"
	OutcomeIdentityMismatch Outcome = "identity_mismatch"
	OutcomeDuplicate        Outcome = "duplicate_provider"
	OutcomeNotFound         Outcome = "provider_not_found"
	OutcomeMalformed        Outcome = "malformed_result"
	OutcomeTimeout          Outcome = "attempt_timeout"
	OutcomeCanceled         Outcome = "parent_canceled"
	OutcomeAdapterCanceled  Outcome = "adapter_canceled"
	OutcomeTransient        Outcome = "transient_failure"
	OutcomeProviderFailure  Outcome = "provider_failure"
	OutcomeExhausted        Outcome = "exhausted"
	OutcomeUnsupportedKind  Outcome = "unsupported_kind"
)

// Error formats only an engine-owned code, never adapter/request text. Original
// causes remain reachable via errors.Is/As; callers must not log those causes
// without their own redaction. Unwrap returns a copy to keep the error stable.
type Error struct {
	code   Outcome
	causes []error
}

func (e *Error) Error() string { return "decision: " + string(e.code) }

// Outcome returns the engine's safe failure classification.
func (e *Error) Outcome() Outcome { return e.code }
func (e *Error) Unwrap() []error  { return append([]error(nil), e.causes...) }

func safeError(code Outcome, causes ...error) *Error {
	e := &Error{code: code}
	for _, cause := range causes {
		if cause != nil {
			e.causes = append(e.causes, cause)
		}
	}
	return e
}
