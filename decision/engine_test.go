package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
)

type resolverFunc func(context.Context, string) (Provider, error)

func (f resolverFunc) Resolve(ctx context.Context, id string) (Provider, error) { return f(ctx, id) }

type adapter struct {
	id     string
	decide func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error)
	caps   func(context.Context) (domain.ProviderCapabilities, error)
}

func (p *adapter) ID() string { return p.id }
func (p *adapter) Decide(ctx context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
	return p.decide(ctx, req)
}
func (p *adapter) Capabilities(ctx context.Context) (domain.ProviderCapabilities, error) {
	if p.caps != nil {
		return p.caps(ctx)
	}
	return domain.ProviderCapabilities{ProviderID: p.id, Tags: []string{"semantic-decision"}}, nil
}

func testRequest() domain.DecisionRequest {
	return domain.DecisionRequest{ID: "request", Kind: "kind", ProjectID: "project", ShotID: "shot"}
}
func testResult(score float64) domain.DecisionResult {
	return domain.DecisionResult{Decision: domain.DecisionYes, Score: score}
}
func testEngine(t *testing.T, cfg Config, providers ...*adapter) *Engine {
	t.Helper()
	r := NewRegistry()
	for _, p := range providers {
		if err := r.Register(context.Background(), p); err != nil {
			t.Fatal(err)
		}
	}
	e, err := NewEngine(r, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func constantAdapter(id string, result domain.DecisionResult, err error) *adapter {
	return &adapter{id: id, decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
		return result, err
	}}
}

func TestEngineThresholdAuthority(t *testing.T) {
	for _, tc := range []struct {
		policy Policy
		score  float64
		want   domain.DecisionVerdict
	}{
		{DefaultPolicy(), .85, domain.DecisionYes},
		{DefaultPolicy(), .30, domain.DecisionNo},
		{DefaultPolicy(), .5, domain.DecisionUncertain},
		{Policy{YesThreshold: .5, NoThreshold: .5}, .5, domain.DecisionYes},
		{Policy{YesThreshold: .5, NoThreshold: .5}, .49, domain.DecisionNo},
		{Policy{}, 0, domain.DecisionYes},
		{Policy{YesThreshold: 1}, 1, domain.DecisionYes},
		{Policy{YesThreshold: 1}, 0, domain.DecisionNo},
	} {
		cfg := DefaultConfig("p")
		cfg.Policy = tc.policy
		provided := testResult(tc.score)
		provided.Decision = domain.DecisionNo // core, not provider verdict, decides
		provided.ReasonCode = "optional-metadata"
		e := testEngine(t, cfg, constantAdapter("p", provided, nil))
		got, err := e.Decide(context.Background(), testRequest())
		if err != nil || got.Decision != tc.want || got.Provider != "p" || got.Score != tc.score || got.ReasonCode != provided.ReasonCode {
			t.Fatalf("policy %+v score %v: got %+v, %v", tc.policy, tc.score, got, err)
		}
	}
}

func TestConfigValidationAndOwnership(t *testing.T) {
	r := NewRegistry()
	for name, mutate := range map[string]func(*Config){
		"empty":            func(c *Config) { c.Providers = nil },
		"blank":            func(c *Config) { c.Providers = []string{" "} },
		"duplicate":        func(c *Config) { c.Providers = []string{"p", "p"} },
		"too many":         func(c *Config) { c.Providers = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"} },
		"timeout zero":     func(c *Config) { c.AttemptTimeout = 0 },
		"timeout negative": func(c *Config) { c.AttemptTimeout = -1 },
		"no attempts":      func(c *Config) { c.MaxAttempts = 0 },
		"over cap":         func(c *Config) { c.MaxAttempts = 4 },
		"negative delay":   func(c *Config) { c.RetryDelay = -1 },
		"audit zero":       func(c *Config) { c.AuditTimeout = 0 },
		"audit negative":   func(c *Config) { c.AuditTimeout = -1 },
		"nan":              func(c *Config) { c.Policy.YesThreshold = math.NaN() },
		"inf":              func(c *Config) { c.Policy.NoThreshold = math.Inf(1) },
		"inverted":         func(c *Config) { c.Policy = Policy{YesThreshold: .2, NoThreshold: .8} },
		"out of range":     func(c *Config) { c.Policy.YesThreshold = 2 },
	} {
		t.Run(name, func(t *testing.T) {
			cfg := DefaultConfig("p")
			mutate(&cfg)
			if _, err := NewEngine(r, cfg); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := NewEngine(nil, DefaultConfig("p")); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("nil resolver: %v", err)
	}
	ids := []string{"p"}
	cfg := DefaultConfig(ids...)
	ids[0] = "changed"
	if cfg.Providers[0] != "p" || cfg.Policy != DefaultPolicy() || cfg.AttemptTimeout != 5*time.Second || cfg.MaxAttempts != 2 || cfg.RetryDelay != 100*time.Millisecond || !cfg.EscalateUncertain || cfg.AuditTimeout != 200*time.Millisecond {
		t.Fatalf("bad defaults: %+v", cfg)
	}
	cfg.RetryDelay = 0
	e := testEngine(t, cfg, constantAdapter("p", testResult(1), nil))
	cfg.Providers[0] = "changed"
	if _, err := e.Decide(context.Background(), testRequest()); err != nil {
		t.Fatal(err)
	}
}

func TestMalformedAndProvenanceNeverRetry(t *testing.T) {
	for name, result := range map[string]domain.DecisionResult{
		"nan": testResult(math.NaN()), "positive inf": testResult(math.Inf(1)),
		"negative inf": testResult(math.Inf(-1)), "negative": testResult(-.01),
		"above one": testResult(1.01), "unknown verdict": {Decision: "secret-verdict", Score: .9},
		"empty verdict": {Score: .9}, "wrong provider": {Decision: domain.DecisionYes, Score: .9, Provider: "secret-provider"},
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				calls++
				return result, nil
			}}
			cfg := DefaultConfig("p", "next")
			cfg.RetryDelay = 0
			e := testEngine(t, cfg, p, constantAdapter("next", testResult(1), nil))
			got, err := e.Decide(context.Background(), testRequest())
			if err != nil || got.Provider != "next" || calls != 1 {
				t.Fatalf("%+v, %v, calls=%d", got, err, calls)
			}
			cfg.Providers = []string{"p"}
			e = testEngine(t, cfg, p)
			_, err = e.Decide(context.Background(), testRequest())
			want := ErrMalformedResult
			if name == "wrong provider" {
				want = ErrIdentityMismatch
			}
			if !errors.Is(err, ErrExhausted) || !errors.Is(err, want) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	wrong := constantAdapter("wrong", testResult(1), nil)
	e, err := NewEngine(resolverFunc(func(context.Context, string) (Provider, error) { return wrong, nil }), DefaultConfig("p"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Decide(context.Background(), testRequest())
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatal(err)
	}
}

func TestIdentityChangeOnErrorNeverRetries(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprintf("fallback=%v", fallback), func(t *testing.T) {
			cause := &privateFailure{"SECRET-private-cause"}
			wrapped := fmt.Errorf("SECRET-error-wrapper: %w: %w", ErrTransient, cause)
			pub := &recordPublisher{}
			cfg := DefaultConfig("selected")
			cfg.MaxAttempts, cfg.RetryDelay, cfg.Publisher = 3, 0, pub
			if fallback {
				cfg.Providers = append(cfg.Providers, "next")
			}
			var resolved, calls []string
			e, err := NewEngine(resolverFunc(func(_ context.Context, id string) (Provider, error) {
				resolved = append(resolved, id)
				// A resolver may supply a fresh reference for each attempt. Each
				// reference violates stable identity on its failing Decide call.
				p := &adapter{id: id}
				p.decide = func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
					calls = append(calls, id)
					if id == "selected" {
						p.id = "SECRET-untrusted-provider"
						return domain.DecisionResult{}, wrapped
					}
					return testResult(1), nil
				}
				return p, nil
			}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.Decide(context.Background(), testRequest())
			wantCalls := []string{"selected"}
			if fallback {
				wantCalls = append(wantCalls, "next")
				if err != nil || got.Provider != "next" || got.Decision != domain.DecisionYes {
					t.Errorf("fallback: got=%+v error=%v", got, err)
				}
			} else {
				var original *privateFailure
				if got != (domain.DecisionResult{}) || !errors.Is(err, ErrExhausted) || !errors.Is(err, ErrIdentityMismatch) || !errors.Is(err, ErrTransient) || !errors.Is(err, cause) || !errors.As(err, &original) || original != cause {
					t.Errorf("provenance failure or original causes missing: got=%+v error=%v", got, err)
				}
				if err != nil && strings.Contains(err.Error(), "SECRET") {
					t.Errorf("unsafe public error: %v", err)
				}
			}
			if !reflect.DeepEqual(calls, wantCalls) || !reflect.DeepEqual(resolved, wantCalls) {
				t.Errorf("identity mismatch retried: provider calls=%v resolved=%v want=%v", calls, resolved, wantCalls)
			}
			if len(pub.events) != len(wantCalls)+2 {
				t.Errorf("unexpected audit attempts: got %d records want %d", len(pub.events), len(wantCalls)+2)
			}
			first := pub.events[1]
			wantAction := "stop"
			if fallback {
				wantAction = "fallback"
			}
			if first.Type != event.TypeDecisionAttemptCompleted || first.Payload["provider"] != "selected" || first.Payload["attempt"] != 1 || first.Payload["outcome"] != string(OutcomeIdentityMismatch) || first.Payload["action"] != wantAction {
				t.Errorf("incorrect provenance audit: %+v", first)
			}
			for _, evt := range pub.events {
				data, marshalErr := json.Marshal(evt)
				if marshalErr != nil || strings.Contains(string(data), "SECRET") {
					t.Errorf("unsafe audit: %s %v", data, marshalErr)
				}
			}
		})
	}
}

func TestExpiredResolverProviderIsNeverInvoked(t *testing.T) {
	for _, cap := range []int{1, 2} {
		t.Run(fmt.Sprintf("max_attempts=%d", cap), func(t *testing.T) {
			var resolved []string
			slowCalls, nextCalls := 0, 0
			slow := &adapter{id: "slow", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				slowCalls++
				return testResult(1), nil
			}}
			next := &adapter{id: "next", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				nextCalls++
				return testResult(1), nil
			}}
			pub := &recordPublisher{}
			cfg := DefaultConfig("slow", "next")
			cfg.MaxAttempts, cfg.RetryDelay, cfg.AttemptTimeout, cfg.Publisher = cap, 0, 25*time.Millisecond, pub
			e, err := NewEngine(resolverFunc(func(ctx context.Context, id string) (Provider, error) {
				resolved = append(resolved, id)
				if id == "slow" {
					<-ctx.Done()
					return slow, nil // successful resolution after the child deadline
				}
				return next, nil
			}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.Decide(context.Background(), testRequest())
			wantOrder := make([]string, cap)
			for i := range wantOrder {
				wantOrder[i] = "slow"
			}
			wantOrder = append(wantOrder, "next")
			if err != nil || got.Provider != "next" || slowCalls != 0 || nextCalls != 1 || !reflect.DeepEqual(resolved, wantOrder) {
				t.Fatalf("got=%+v error=%v slowCalls=%d nextCalls=%d resolved=%v want=%v", got, err, slowCalls, nextCalls, resolved, wantOrder)
			}
			if len(pub.events) != cap+3 {
				t.Fatalf("events=%+v", pub.events)
			}
			for i := 1; i <= cap; i++ {
				attempt := pub.events[i]
				action := "retry"
				if i == cap {
					action = "fallback"
				}
				if attempt.Payload["provider"] != "slow" || attempt.Payload["attempt"] != i || attempt.Payload["outcome"] != string(OutcomeTimeout) || attempt.Payload["action"] != action {
					t.Fatalf("incorrect timeout attempt: %+v", attempt)
				}
			}
		})
	}
}

func TestResultAccompanyingErrorRemainsIgnored(t *testing.T) {
	pub := &recordPublisher{}
	calls := 0
	p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
		calls++
		if calls == 1 {
			return domain.DecisionResult{Decision: "SECRET-invalid-verdict", Score: math.NaN(), Provider: "SECRET-untrusted-provider"}, ErrTransient
		}
		return testResult(1), nil
	}}
	cfg := DefaultConfig("p")
	cfg.RetryDelay, cfg.Publisher = 0, pub
	e := testEngine(t, cfg, p)
	got, err := e.Decide(context.Background(), testRequest())
	if err != nil || got.Provider != "p" || got.Decision != domain.DecisionYes || calls != 2 {
		t.Fatalf("failed-call result was accepted/validated: got=%+v error=%v calls=%d", got, err, calls)
	}
	if pub.events[1].Payload["outcome"] != string(OutcomeTransient) || pub.events[1].Payload["action"] != "retry" {
		t.Fatalf("incorrect failed-call audit: %+v", pub.events[1])
	}
	if _, exists := pub.events[1].Payload["score"]; exists {
		t.Fatal("failed-call score audited")
	}
}

func TestIdentityChangeOnErrorPreservesContextPrecedence(t *testing.T) {
	for _, parentCanceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("parent_canceled=%v", parentCanceled), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pub := &recordPublisher{}
			cfg := DefaultConfig("selected", "next")
			cfg.RetryDelay, cfg.AttemptTimeout, cfg.Publisher = 0, 25*time.Millisecond, pub
			var calls []string
			e, err := NewEngine(resolverFunc(func(_ context.Context, id string) (Provider, error) {
				p := &adapter{id: id}
				p.decide = func(child context.Context, _ domain.DecisionRequest) (domain.DecisionResult, error) {
					calls = append(calls, id)
					if id == "next" {
						return testResult(1), nil
					}
					if parentCanceled {
						cancel()
					} else {
						<-child.Done()
					}
					p.id = "SECRET-untrusted-provider"
					return domain.DecisionResult{}, ErrTransient
				}
				return p, nil
			}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.Decide(ctx, testRequest())
			if parentCanceled {
				if !errors.Is(err, context.Canceled) || got != (domain.DecisionResult{}) || !reflect.DeepEqual(calls, []string{"selected"}) || pub.events[len(pub.events)-1].Payload["outcome"] != string(OutcomeCanceled) {
					t.Fatalf("parent cancellation lost: got=%+v error=%v calls=%v events=%+v", got, err, calls, pub.events)
				}
			} else {
				if err != nil || got.Provider != "next" || !reflect.DeepEqual(calls, []string{"selected", "selected", "next"}) || pub.events[1].Payload["outcome"] != string(OutcomeTimeout) || pub.events[2].Payload["outcome"] != string(OutcomeTimeout) {
					t.Fatalf("child timeout precedence changed: got=%+v error=%v calls=%v events=%+v", got, err, calls, pub.events)
				}
			}
		})
	}
}

func TestRetryCapsAndFallbackOrder(t *testing.T) {
	var calls []string
	makeFail := func(id string, err error) *adapter {
		return &adapter{id: id, decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
			calls = append(calls, id)
			return domain.DecisionResult{}, err
		}}
	}
	for _, cap := range []int{1, 2, 3} {
		calls = nil
		cfg := DefaultConfig("transient", "permanent", "malformed", "canceled", "deadline")
		cfg.MaxAttempts, cfg.RetryDelay = cap, 0
		e := testEngine(t, cfg,
			makeFail("transient", fmt.Errorf("private detail: %w", ErrTransient)),
			makeFail("permanent", errors.New("private failure")),
			makeFail("malformed", errors.Join(ErrTransient, ErrMalformedResult)),
			makeFail("canceled", context.Canceled),
			makeFail("deadline", context.DeadlineExceeded)) // adapter deadline isn't child timeout
		got, err := e.Decide(context.Background(), testRequest())
		want := make([]string, cap)
		for i := range want {
			want[i] = "transient"
		}
		want = append(want, "permanent", "malformed", "canceled", "deadline")
		if !reflect.DeepEqual(calls, want) || got != (domain.DecisionResult{}) || !errors.Is(err, ErrExhausted) {
			t.Fatalf("cap %d: %v != %v, %+v, %v", cap, calls, want, got, err)
		}
		var detail *Error
		if !errors.As(err, &detail) || detail.Outcome() != OutcomeExhausted || len(detail.Unwrap()) != len(want)+1 {
			t.Fatalf("unbounded/missing causes: %v", err)
		}
	}
	var count int
	p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
		count++
		if count == 1 {
			return domain.DecisionResult{}, ErrTransient
		}
		return testResult(.9), nil
	}}
	cfg := DefaultConfig("p", "must-not-run")
	cfg.RetryDelay = 0
	e := testEngine(t, cfg, p)
	if got, err := e.Decide(context.Background(), testRequest()); err != nil || got.Provider != "p" || count != 2 {
		t.Fatalf("retry success: %+v %v count=%d", got, err, count)
	}
}

func TestUncertaintyEscalationPreservesLastSuccess(t *testing.T) {
	for _, escalate := range []bool{false, true} {
		var calls []string
		makeProvider := func(id string, score float64, err error) *adapter {
			return &adapter{id: id, decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				calls = append(calls, id)
				return testResult(score), err
			}}
		}
		cfg := DefaultConfig("first", "second", "failure", "abstain")
		cfg.EscalateUncertain, cfg.RetryDelay = escalate, 0
		e := testEngine(t, cfg, makeProvider("first", .4, nil), makeProvider("second", .6, nil),
			makeProvider("failure", 0, errors.New("private")), makeProvider("abstain", 0, ErrUnsupportedKind))
		got, err := e.Decide(context.Background(), testRequest())
		wantProvider, wantScore, wantCalls := "first", .4, []string{"first"}
		if escalate {
			wantProvider, wantScore, wantCalls = "second", .6, []string{"first", "second", "failure", "abstain"}
		}
		if err != nil || got.Provider != wantProvider || got.Score != wantScore || got.Decision != domain.DecisionUncertain || !reflect.DeepEqual(calls, wantCalls) {
			t.Fatalf("escalate %v: %+v, %v, %v", escalate, got, err, calls)
		}
	}
	cfg := DefaultConfig("uncertain", "certain")
	e := testEngine(t, cfg, constantAdapter("uncertain", testResult(.5), nil), constantAdapter("certain", testResult(.1), nil))
	if got, err := e.Decide(context.Background(), testRequest()); err != nil || got.Provider != "certain" || got.Decision != domain.DecisionNo {
		t.Fatalf("certain fallback: %+v %v", got, err)
	}
}

func TestCancellationAndTimeouts(t *testing.T) {
	t.Run("entry", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var calls int
		p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
			calls++
			return testResult(1), nil
		}}
		e := testEngine(t, DefaultConfig("p"), p)
		got, err := e.Decide(ctx, testRequest())
		if !errors.Is(err, context.Canceled) || calls != 0 || got != (domain.DecisionResult{}) {
			t.Fatalf("%+v %v %d", got, err, calls)
		}
	})
	t.Run("in call saved uncertainty", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var calls int
		p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
			calls++
			cancel()
			return testResult(1), errors.New("private")
		}}
		e := testEngine(t, DefaultConfig("uncertain", "p", "next"), constantAdapter("uncertain", testResult(.5), nil), p)
		got, err := e.Decide(ctx, testRequest())
		if !errors.Is(err, context.Canceled) || calls != 1 || got != (domain.DecisionResult{}) {
			t.Fatalf("%+v %v %d", got, err, calls)
		}
	})
	t.Run("child timeout and late success", func(t *testing.T) {
		for _, lateSuccess := range []bool{false, true} {
			var calls int
			p := &adapter{id: "p", decide: func(ctx context.Context, _ domain.DecisionRequest) (domain.DecisionResult, error) {
				calls++
				<-ctx.Done()
				if lateSuccess {
					return testResult(1), nil
				}
				return domain.DecisionResult{}, ctx.Err()
			}}
			cfg := DefaultConfig("p", "next")
			cfg.AttemptTimeout, cfg.RetryDelay = 20*time.Millisecond, 0
			e := testEngine(t, cfg, p, constantAdapter("next", testResult(.1), nil))
			got, err := e.Decide(context.Background(), testRequest())
			if err != nil || calls != 2 || got.Provider != "next" {
				t.Fatalf("late %v: %+v %v calls=%d", lateSuccess, got, err, calls)
			}
		}
	})
	t.Run("retry delay", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		retrying, canceled := make(chan struct{}), make(chan struct{})
		pub := &recordPublisher{}
		pub.hook = func(ctx context.Context, evt event.Event) error {
			if evt.Payload["action"] == "retry" {
				close(retrying)
			}
			return nil
		}
		go func() {
			defer close(canceled)
			select {
			case <-retrying:
				time.Sleep(20 * time.Millisecond) // cancel while the one-second delay is pending
				cancel()
			case <-ctx.Done():
			}
		}()
		var calls int
		p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
			calls++
			return domain.DecisionResult{}, ErrTransient
		}}
		cfg := DefaultConfig("p", "next")
		cfg.RetryDelay, cfg.Publisher = time.Second, pub
		e := testEngine(t, cfg, p)
		got, err := e.Decide(ctx, testRequest())
		<-canceled
		if !errors.Is(err, context.Canceled) || calls != 1 || got != (domain.DecisionResult{}) {
			t.Fatalf("%+v %v calls=%d", got, err, calls)
		}
	})
	t.Run("parent deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		p := &adapter{id: "p", decide: func(ctx context.Context, _ domain.DecisionRequest) (domain.DecisionResult, error) {
			<-ctx.Done()
			return testResult(1), nil
		}}
		e := testEngine(t, DefaultConfig("p", "next"), p)
		got, err := e.Decide(ctx, testRequest())
		if !errors.Is(err, context.DeadlineExceeded) || got != (domain.DecisionResult{}) {
			t.Fatalf("%+v %v", got, err)
		}
	})
}

type privateFailure struct{ detail string }

func (e *privateFailure) Error() string { return e.detail }

func TestSafeErrorsPreserveCauseIdentity(t *testing.T) {
	cause := &privateFailure{"SECRET-input-kind-provider-error"}
	wrapped := fmt.Errorf("SECRET-wrapper: %w: %w", ErrTransient, cause)
	cfg := DefaultConfig("SECRET-provider")
	cfg.RetryDelay = 0
	e := testEngine(t, cfg, constantAdapter("SECRET-provider", domain.DecisionResult{}, wrapped))
	req := testRequest()
	req.Kind = "SECRET-kind"
	req.Input = map[string]any{"SECRET": "SECRET-input"}
	_, err := e.Decide(context.Background(), req)
	var original *privateFailure
	var detail *Error
	if !errors.Is(err, cause) || !errors.Is(err, ErrTransient) || !errors.As(err, &original) || original != cause || !errors.As(err, &detail) || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("cause lost or leaked: %v", err)
	}
	copy := detail.Unwrap()
	copy[0] = cause
	if !errors.Is(err, ErrExhausted) {
		t.Fatal("mutable causes")
	}
	// An adapter-supplied safe wrapper cannot inject its outcome text into audit.
	pub := &recordPublisher{}
	cfg.Publisher = pub
	e = testEngine(t, cfg, constantAdapter("SECRET-provider", domain.DecisionResult{}, safeError(Outcome("SECRET-code"), cause)))
	_, err = e.Decide(context.Background(), req)
	if strings.Contains(err.Error(), "SECRET") || pub.events[1].Payload["outcome"] != string(OutcomeProviderFailure) {
		t.Fatalf("unsafe classification: %v", err)
	}
}

func TestInputValidationSnapshotAndAttemptIsolation(t *testing.T) {
	var calls int
	p := &adapter{id: "p", decide: func(_ context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
		calls++
		nested := req.Input["nested"].(map[string]any)
		items := nested["items"].([]any)
		if items[0] != "original" || req.Input["number"] != float64(42) || nested["flag"] != true {
			return domain.DecisionResult{}, errors.New("input alias or non-JSON number")
		}
		items[0] = "changed"
		nested["flag"] = false
		req.Input["added"] = true
		if calls == 1 {
			return domain.DecisionResult{}, ErrTransient
		}
		return testResult(1), nil
	}}
	cfg := DefaultConfig("p")
	cfg.RetryDelay = 0
	e := testEngine(t, cfg, p)
	req := testRequest()
	req.Input = map[string]any{"nested": map[string]any{"items": []string{"original"}, "flag": true}, "number": 42}
	before, _ := json.Marshal(req)
	got, err := e.Decide(context.Background(), req)
	after, _ := json.Marshal(req)
	if err != nil || calls != 2 || got.Decision != domain.DecisionYes || string(before) != string(after) {
		t.Fatalf("%+v %v calls=%d input %s", got, err, calls, after)
	}
	for _, input := range []map[string]any{{"bad": make(chan int)}, {"bad": math.NaN()}, {"bad": func() {}}} {
		req.Input = input
		if _, err := e.Decide(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("serialization: %v", err)
		}
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	req.Input = cycle
	if _, err := e.Decide(context.Background(), req); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("cycle: %v", err)
	}
	for _, bad := range []domain.DecisionRequest{{Kind: "kind"}, {ID: " ", Kind: "kind"}, {ID: "id"}, {ID: "id", Kind: " "}} {
		if _, err := e.Decide(context.Background(), bad); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("validation: %v", err)
		}
	}
	if calls != 2 {
		t.Fatalf("invalid request invoked adapter: %d", calls)
	}
}

func TestFallbackInputIsolationAndMaximumBudget(t *testing.T) {
	req := testRequest()
	req.Input = map[string]any{"nested": map[string]any{"value": "original"}}
	first := &adapter{id: "first", decide: func(_ context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
		req.Input["nested"].(map[string]any)["value"] = "changed"
		return domain.DecisionResult{}, errors.New("permanent")
	}}
	second := &adapter{id: "second", decide: func(_ context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
		if req.Input["nested"].(map[string]any)["value"] != "original" {
			return domain.DecisionResult{}, errors.New("fallback aliased input")
		}
		return testResult(1), nil
	}}
	e := testEngine(t, DefaultConfig("first", "second"), first, second)
	if _, err := e.Decide(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	if req.Input["nested"].(map[string]any)["value"] != "original" {
		t.Fatal("caller input changed")
	}
	var ids, calls []string
	var providers []*adapter
	for i := 0; i < 8; i++ {
		id := fmt.Sprintf("p%d", i)
		ids = append(ids, id)
		providers = append(providers, &adapter{id: id, decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
			calls = append(calls, id)
			return domain.DecisionResult{}, ErrTransient
		}})
	}
	cfg := DefaultConfig(ids...)
	cfg.MaxAttempts, cfg.RetryDelay = 3, 0
	e = testEngine(t, cfg, providers...)
	_, err := e.Decide(context.Background(), testRequest())
	var detail *Error
	if !errors.Is(err, ErrExhausted) || len(calls) != 24 || !errors.As(err, &detail) || len(detail.Unwrap()) != 25 {
		t.Fatalf("budget calls=%d, %v", len(calls), err)
	}
	for i, id := range calls {
		if id != ids[i/3] {
			t.Fatalf("order at %d: %s", i, id)
		}
	}
}

func TestSharedEngineConcurrentDecisions(t *testing.T) {
	var calls atomic.Int64
	p := &adapter{id: "p", decide: func(_ context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
		calls.Add(1)
		if req.Input["value"] != "original" {
			return domain.DecisionResult{}, errors.New("mutated")
		}
		req.Input["value"] = "changed"
		return testResult(1), nil
	}}
	pub := &recordPublisher{}
	cfg := DefaultConfig("p")
	cfg.Publisher = pub
	e := testEngine(t, cfg, p)
	req := testRequest()
	req.Input = map[string]any{"value": "original"}
	const n = 32
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := e.Decide(context.Background(), req)
			if err == nil && (got.Decision != domain.DecisionYes || got.Provider != "p") {
				err = errors.New("wrong result")
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if calls.Load() != n || req.Input["value"] != "original" || len(pub.events) != 3*n || e.AuditFailures() != 0 {
		t.Fatalf("calls=%d audit=%d failures=%d", calls.Load(), len(pub.events), e.AuditFailures())
	}
}
