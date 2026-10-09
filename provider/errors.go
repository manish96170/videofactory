package provider

import "errors"

var (
	ErrInvalidConfig       = errors.New("provider: invalid configuration")
	ErrInvalidRequest      = errors.New("provider: invalid request")
	ErrInvalidProvider     = errors.New("provider: invalid provider")
	ErrIdentityMismatch    = errors.New("provider: identity mismatch")
	ErrDuplicateProvider   = errors.New("provider: duplicate provider")
	ErrProviderNotFound    = errors.New("provider: provider not found")
	ErrIneligible          = errors.New("provider: ineligible provider")
	ErrNoEligibleProviders = errors.New("provider: no eligible providers")
)

// Code is a core-owned safe failure classification, never adapter text.
type Code string

const (
	CodeInvalidConfig    Code = "invalid_config"
	CodeInvalidRequest   Code = "invalid_request"
	CodeInvalidProvider  Code = "invalid_provider"
	CodeIdentityMismatch Code = "identity_mismatch"
	CodeDiscoveryFailed  Code = "discovery_failed"
	CodeDuplicate        Code = "duplicate_provider"
	CodeNotFound         Code = "provider_not_found"
	CodeIneligible       Code = "ineligible_provider"
	CodeNoEligible       Code = "no_eligible_providers"
	CodeCanceled         Code = "canceled"
)

// Error's string contains only a core code. Causes preserve errors.Is/As;
// callers must redact those causes themselves before logging them.
type Error struct {
	code   Code
	causes []error
}

func (e *Error) Error() string   { return "provider: " + string(e.code) }
func (e *Error) Code() Code      { return e.code }
func (e *Error) Unwrap() []error { return append([]error(nil), e.causes...) }

func safeError(code Code, causes ...error) *Error {
	e := &Error{code: code}
	for _, cause := range causes {
		if cause != nil {
			e.causes = append(e.causes, cause)
		}
	}
	return e
}
