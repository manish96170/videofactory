package observability

import (
	"context"
)

// TraceContext contains caller-supplied W3C-sized hexadecimal correlation IDs.
// IDs must be nonzero and must not contain secret or user-provided payload data.
type TraceContext struct {
	TraceID string
	SpanID  string
}

type traceContextKey struct{}

// WithTrace returns a context carrying a validated trace/span correlation.
// It creates no IDs and starts no spans or goroutines.
func WithTrace(ctx context.Context, trace TraceContext) (context.Context, error) {
	if ctx == nil || !validHexID(trace.TraceID, 32) || !validHexID(trace.SpanID, 16) {
		return nil, ErrInvalid
	}
	return context.WithValue(ctx, traceContextKey{}, trace), nil
}

// TraceFromContext returns a detached correlation value, if one is present.
func TraceFromContext(ctx context.Context) (TraceContext, bool) {
	if ctx == nil {
		return TraceContext{}, false
	}
	trace, ok := ctx.Value(traceContextKey{}).(TraceContext)
	if !ok || !validHexID(trace.TraceID, 32) || !validHexID(trace.SpanID, 16) {
		return TraceContext{}, false
	}
	return trace, true
}

func validHexID(value string, size int) bool {
	if len(value) != size {
		return false
	}
	allZero := true
	for i := 0; i < len(value); i++ {
		c := value[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
		if c != '0' {
			allZero = false
		}
	}
	return !allZero
}
