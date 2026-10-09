package decision

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
)

// Config is explicit: NewEngine does not replace zero values with defaults.
// Use DefaultConfig to start from recommended settings. Providers is an ordered
// chain of 1..8 distinct nonblank, nonsecret handles. MaxAttempts includes the
// initial call and is limited to 1..3. RetryDelay may be zero, not negative.
type Config struct {
	Providers         []string
	Policy            Policy
	AttemptTimeout    time.Duration
	MaxAttempts       int
	RetryDelay        time.Duration
	EscalateUncertain bool
	Publisher         event.Publisher
	AuditTimeout      time.Duration
}

// DefaultConfig copies the explicit provider chain and applies the recommended
// thresholds, timeouts, retry budget/delay, and uncertainty escalation.
func DefaultConfig(providers ...string) Config {
	return Config{
		Providers: append([]string(nil), providers...),
		Policy:    DefaultPolicy(), AttemptTimeout: 5 * time.Second,
		MaxAttempts: 2, RetryDelay: 100 * time.Millisecond,
		EscalateUncertain: true, AuditTimeout: 200 * time.Millisecond,
	}
}

// Engine is safe for concurrent Decide calls when its caller-owned resolver,
// providers, and optional publisher are safe. It does not serialize adapters or
// own their lifecycle. Do not copy an Engine after use; construct with NewEngine.
type Engine struct {
	resolver      Resolver
	cfg           Config
	auditFailures atomic.Uint64
}

// NewEngine validates configuration without resolving/calling providers and
// copies the provider chain. Even with audit disabled, AuditTimeout must be valid.
func NewEngine(resolver Resolver, cfg Config) (*Engine, error) {
	if nilInterface(resolver) || len(cfg.Providers) == 0 || len(cfg.Providers) > 8 ||
		cfg.AttemptTimeout <= 0 || cfg.MaxAttempts < 1 || cfg.MaxAttempts > 3 ||
		cfg.RetryDelay < 0 || cfg.AuditTimeout <= 0 {
		return nil, safeError(OutcomeInvalidConfig, ErrInvalidConfig)
	}
	if err := cfg.Policy.Validate(); err != nil {
		return nil, safeError(OutcomeInvalidConfig, ErrInvalidConfig, err)
	}
	seen := make(map[string]bool, len(cfg.Providers))
	for _, id := range cfg.Providers {
		if strings.TrimSpace(id) == "" || seen[id] {
			return nil, safeError(OutcomeInvalidConfig, ErrInvalidConfig)
		}
		seen[id] = true
	}
	cfg.Providers = append([]string(nil), cfg.Providers...)
	return &Engine{resolver: resolver, cfg: cfg}, nil
}

// Decide validates and snapshots Input via encoding/json (ADR-008, numbers
// decode as float64), then gives each attempt an independent decoded copy.
// Callers must not mutate input during snapshotting. Adapters must not retain
// or asynchronously mutate it after returning. No caller-owned input is audited.
// Requests are accepted only after validation/snapshotting with an active ctx.
// Provider/resolver calls are synchronous and cooperative: no goroutine hides a
// noncooperative adapter. Late success after a child timeout is rejected.
// The single attempted terminal audit describes state selected when publication
// starts. Parent cancellation during that cooperative publication can change the
// returned outcome; publication and caller cancellation are not atomic, and no
// second terminal event is emitted to revise the already-published record.
func (e *Engine) Decide(ctx context.Context, req domain.DecisionRequest) (result domain.DecisionResult, err error) {
	if err := ctx.Err(); err != nil {
		return domain.DecisionResult{}, safeError(OutcomeCanceled, err)
	}
	if strings.TrimSpace(string(req.ID)) == "" || strings.TrimSpace(req.Kind) == "" {
		return domain.DecisionResult{}, safeError(OutcomeInvalidRequest, ErrInvalidRequest)
	}
	snapshot, snapshotErr := json.Marshal(req.Input)
	if snapshotErr != nil {
		return domain.DecisionResult{}, safeError(OutcomeInvalidRequest, ErrInvalidRequest, snapshotErr)
	}
	if err := ctx.Err(); err != nil {
		return domain.DecisionResult{}, safeError(OutcomeCanceled, err)
	}
	e.audit(ctx, event.TypeDecisionStarted, req, "", 0, OutcomeStarted, "start", nil)
	ordinal := 0
	lastProvider := "" // configured handle of the final recorded attempt, if any
	defer func() {
		if err != nil {
			code := OutcomeExhausted
			var detail *Error
			if errors.As(err, &detail) {
				code = detail.Outcome()
			}
			// A canceled parent still gets bounded cooperative terminal cleanup.
			e.audit(context.WithoutCancel(ctx), event.TypeDecisionFailed, req, lastProvider, ordinal, code, "stop", nil)
		} else {
			e.audit(ctx, event.TypeDecisionCompleted, req, result.Provider, ordinal, OutcomeValid, "return", &result)
		}
		if parentErr := ctx.Err(); parentErr != nil {
			result = domain.DecisionResult{}
			err = safeError(OutcomeCanceled, parentErr, err)
		}
	}()

	var uncertain domain.DecisionResult
	hasUncertain := false
	// At most 8*3 failed attempts; errors never concatenate untrusted text.
	causes := make([]error, 0, len(e.cfg.Providers)*e.cfg.MaxAttempts)
	for providerIndex, id := range e.cfg.Providers {
		for attempt := 1; attempt <= e.cfg.MaxAttempts; attempt++ {
			if parentErr := ctx.Err(); parentErr != nil {
				return domain.DecisionResult{}, safeError(OutcomeCanceled, parentErr)
			}
			ordinal++
			lastProvider = id
			var input map[string]any
			// Marshal already verified this snapshot; decoding is still checked.
			if decodeErr := json.Unmarshal(snapshot, &input); decodeErr != nil {
				return domain.DecisionResult{}, safeError(OutcomeInvalidRequest, ErrInvalidRequest, decodeErr)
			}
			attemptReq := req
			attemptReq.Input = input
			child, cancel := context.WithTimeout(ctx, e.cfg.AttemptTimeout)
			got, callErr := e.call(child, id, attemptReq)
			// Inspect both contexts BEFORE our own cancel changes child status.
			childErr, parentErr := child.Err(), ctx.Err()
			cancel()
			if parentErr != nil {
				e.audit(ctx, event.TypeDecisionAttemptCompleted, req, id, ordinal, OutcomeCanceled, "stop", nil)
				return domain.DecisionResult{}, safeError(OutcomeCanceled, parentErr, callErr)
			}
			code, retry := OutcomeValid, false
			if childErr == context.DeadlineExceeded {
				code, retry = OutcomeTimeout, true
				callErr = safeError(code, childErr, callErr)
			} else if childErr != nil {
				code = OutcomeAdapterCanceled
				callErr = safeError(code, childErr, callErr)
			} else if callErr != nil {
				code = failureOutcome(callErr)
				if code == OutcomeProviderFailure && errors.Is(callErr, context.Canceled) {
					code = OutcomeAdapterCanceled
				} else if code == OutcomeProviderFailure && errors.Is(callErr, ErrTransient) {
					code, retry = OutcomeTransient, true
				}
				callErr = safeError(code, ErrProviderFailure, callErr)
			}
			if callErr == nil {
				if got.Decision != domain.DecisionUncertain || !e.cfg.EscalateUncertain {
					e.audit(ctx, event.TypeDecisionAttemptCompleted, req, id, ordinal, code, "return", &got)
					if parentErr := ctx.Err(); parentErr != nil {
						return domain.DecisionResult{}, safeError(OutcomeCanceled, parentErr)
					}
					return got, nil
				}
				uncertain, hasUncertain = got, true
				action := "escalate"
				if providerIndex == len(e.cfg.Providers)-1 {
					action = "return"
				}
				e.audit(ctx, event.TypeDecisionAttemptCompleted, req, id, ordinal, code, action, &got)
				break // valid uncertainty never retries the same provider
			}
			causes = append(causes, callErr)
			retry = retry && attempt < e.cfg.MaxAttempts
			action := "fallback"
			if retry {
				action = "retry"
			} else if providerIndex == len(e.cfg.Providers)-1 {
				action = "stop"
				if hasUncertain {
					action = "return"
				}
			}
			e.audit(ctx, event.TypeDecisionAttemptCompleted, req, id, ordinal, code, action, nil)
			if !retry {
				break
			}
			if delayErr := wait(ctx, e.cfg.RetryDelay); delayErr != nil {
				return domain.DecisionResult{}, safeError(OutcomeCanceled, delayErr, callErr)
			}
		}
	}
	if parentErr := ctx.Err(); parentErr != nil {
		return domain.DecisionResult{}, safeError(OutcomeCanceled, parentErr)
	}
	if hasUncertain {
		return uncertain, nil
	}
	return domain.DecisionResult{}, safeError(OutcomeExhausted, append([]error{ErrExhausted}, causes...)...)
}

func (e *Engine) call(ctx context.Context, id string, req domain.DecisionRequest) (domain.DecisionResult, error) {
	if err := ctx.Err(); err != nil {
		return domain.DecisionResult{}, err
	}
	p, err := e.resolver.Resolve(ctx, id)
	if err != nil {
		return domain.DecisionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return domain.DecisionResult{}, err
	}
	if nilInterface(p) {
		return domain.DecisionResult{}, safeError(OutcomeInvalidProvider, ErrInvalidProvider)
	}
	if p.ID() != id {
		return domain.DecisionResult{}, safeError(OutcomeIdentityMismatch, ErrIdentityMismatch)
	}
	if err := ctx.Err(); err != nil {
		return domain.DecisionResult{}, err
	}
	result, err := p.Decide(ctx, req)
	if p.ID() != id {
		return domain.DecisionResult{}, safeError(OutcomeIdentityMismatch, ErrIdentityMismatch, err)
	}
	if err != nil {
		return domain.DecisionResult{}, err
	}
	if result.Provider != "" && result.Provider != id {
		return domain.DecisionResult{}, safeError(OutcomeIdentityMismatch, ErrIdentityMismatch)
	}
	if !validResult(result) {
		return domain.DecisionResult{}, safeError(OutcomeMalformed, ErrMalformedResult)
	}
	result.Provider = id
	result.Decision = e.cfg.Policy.Evaluate(result.Score)
	return result, nil
}

func validResult(result domain.DecisionResult) bool {
	return (result.Decision == domain.DecisionYes || result.Decision == domain.DecisionNo ||
		result.Decision == domain.DecisionUncertain) && !math.IsNaN(result.Score) &&
		!math.IsInf(result.Score, 0) && result.Score >= 0 && result.Score <= 1
}

// Never take outcome text from an adapter, even a wrapped *Error: classification
// uses only core constants. Permanent validation/provenance codes beat retry.
func failureOutcome(err error) Outcome {
	switch {
	case errors.Is(err, ErrIdentityMismatch):
		return OutcomeIdentityMismatch
	case errors.Is(err, ErrMalformedResult):
		return OutcomeMalformed
	case errors.Is(err, ErrInvalidProvider):
		return OutcomeInvalidProvider
	case errors.Is(err, ErrProviderNotFound):
		return OutcomeNotFound
	case errors.Is(err, ErrUnsupportedKind):
		return OutcomeUnsupportedKind
	default:
		return OutcomeProviderFailure
	}
}

func wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}
