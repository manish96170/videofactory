package model

import "errors"

var (
	ErrInvalidConfig    = errors.New("model: invalid_config")
	ErrInvalidRequest   = errors.New("model: invalid_request")
	ErrInvalidKey       = errors.New("model: invalid_key")
	ErrInvalidModel     = errors.New("model: invalid_model")
	ErrDuplicate        = errors.New("model: duplicate")
	ErrNotFound         = errors.New("model: not_found")
	ErrBusy             = errors.New("model: busy")
	ErrNeedsUnload      = errors.New("model: needs_unload")
	ErrQuarantined      = errors.New("model: quarantined")
	ErrIdentityMismatch = errors.New("model: identity_mismatch")
	ErrReservation      = errors.New("model: reservation_failed")
	ErrLoadFailed       = errors.New("model: load_failed")
	ErrUnloadFailed     = errors.New("model: unload_failed")
	ErrNotStopped       = errors.New("model: stop_unverified")
	ErrLeaseRelease     = errors.New("model: lease_release_failed")
	ErrHistoryRecord    = errors.New("model: history_record_failed")
	ErrReleased         = errors.New("model: handle_released")
)

type Code string

const (
	CodeInvalidConfig    Code = "invalid_config"
	CodeInvalidRequest   Code = "invalid_request"
	CodeInvalidModel     Code = "invalid_model"
	CodeDuplicate        Code = "duplicate"
	CodeNotFound         Code = "not_found"
	CodeBusy             Code = "busy"
	CodeNeedsUnload      Code = "needs_unload"
	CodeQuarantined      Code = "quarantined"
	CodeIdentityMismatch Code = "identity_mismatch"
	CodeReservation      Code = "reservation_failed"
	CodeLoadFailed       Code = "load_failed"
	CodeUnloadFailed     Code = "unload_failed"
	CodeNotStopped       Code = "stop_unverified"
	CodeLeaseRelease     Code = "lease_release_failed"
	CodeHistoryRecord    Code = "history_record_failed"
	CodeReleased         Code = "handle_released"
	CodeCanceled         Code = "canceled"
	CodeTimeout          Code = "timeout"
)

// Error prints core-owned codes only. Causes preserve errors.Is/As, but may
// contain sensitive adapter diagnostics: callers MUST redact before logging.
type Error struct {
	code   Code
	causes []error
}

func (e *Error) Error() string   { return "model: " + string(e.code) }
func (e *Error) Code() Code      { return e.code }
func (e *Error) Unwrap() []error { return append([]error(nil), e.causes...) }
func failure(code Code, causes ...error) error {
	e := &Error{code: code}
	for _, cause := range causes {
		if cause != nil {
			e.causes = append(e.causes, cause)
		}
	}
	return e
}
