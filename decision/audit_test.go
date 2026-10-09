package decision

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
)

type recordPublisher struct {
	mu     sync.Mutex
	events []event.Event
	hook   func(context.Context, event.Event) error
}

type publisherFunc func(context.Context, event.Event) error

func (f publisherFunc) Publish(ctx context.Context, evt event.Event) error { return f(ctx, evt) }

func (p *recordPublisher) Publish(ctx context.Context, evt event.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hook != nil {
		if err := p.hook(ctx, evt); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.events = append(p.events, evt)
	return nil
}

func TestAuditOrderRedactionAndImmutableRecords(t *testing.T) {
	pub := &recordPublisher{}
	var calls int
	p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
		calls++
		if calls == 1 {
			return domain.DecisionResult{}, fmt.Errorf("SECRET-error: %w", ErrTransient)
		}
		result := testResult(.5)
		result.ReasonCode = "SECRET-reason"
		return result, nil
	}}
	provided := testResult(.95)
	provided.ReasonCode = "SECRET-reason"
	cfg := DefaultConfig("p", "next")
	cfg.Publisher, cfg.RetryDelay = pub, 0
	e := testEngine(t, cfg, p, constantAdapter("next", provided, nil))
	req := testRequest()
	req.Kind = "SECRET-kind"
	req.Input = map[string]any{"secret": "SECRET-input"}
	result, err := e.Decide(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	result.Score, result.ReasonCode = .01, "mutated"
	req.Input["secret"] = "mutated"
	var types []event.Type
	var actions []any
	for i, evt := range pub.events {
		types = append(types, evt.Type)
		actions = append(actions, evt.Payload["action"])
		if evt.ID == "" || evt.OccurredAt.IsZero() || evt.ProjectID != req.ProjectID || evt.ShotID != req.ShotID || evt.Payload["requestId"] != string(req.ID) || evt.Payload["yesThreshold"] != .85 || evt.Payload["noThreshold"] != .30 {
			t.Fatalf("missing audit context %d: %+v", i, evt)
		}
		data, err := json.Marshal(evt)
		if err != nil || strings.Contains(string(data), "SECRET") || strings.Contains(string(data), "mutated") {
			t.Fatalf("unsafe audit: %s %v", data, err)
		}
		for _, value := range evt.Payload {
			switch value.(type) {
			case string, int, float64:
			default:
				t.Errorf("non-scalar %T", value)
			}
		}
		if i > 0 && reflect.ValueOf(evt.Payload).Pointer() == reflect.ValueOf(pub.events[i-1].Payload).Pointer() {
			t.Fatal("payload reused")
		}
	}
	if !reflect.DeepEqual(types, []event.Type{event.TypeDecisionStarted, event.TypeDecisionAttemptCompleted, event.TypeDecisionAttemptCompleted, event.TypeDecisionAttemptCompleted, event.TypeDecisionCompleted}) ||
		!reflect.DeepEqual(actions, []any{"start", "retry", "escalate", "return", "return"}) {
		t.Fatalf("types=%v actions=%v", types, actions)
	}
	for i := 1; i <= 3; i++ {
		if pub.events[i].Payload["attempt"] != i {
			t.Fatalf("ordinal %d", i)
		}
	}
	if _, exists := pub.events[1].Payload["score"]; exists {
		t.Fatal("invalid score audited")
	}
	if pub.events[2].Payload["score"] != .5 || pub.events[2].Payload["verdict"] != "uncertain" || pub.events[4].Payload["score"] != .95 || pub.events[4].Payload["verdict"] != "yes" || pub.events[4].Payload["provider"] != "next" {
		t.Fatal("validated result or immutable payload changed")
	}
	// All earlier records remain unchanged across another Decide call.
	before, _ := json.Marshal(pub.events)
	if _, err := e.Decide(context.Background(), testRequest()); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(pub.events[:5])
	if string(before) != string(after) {
		t.Fatal("prior records mutated")
	}
}

func TestAuditFailureCountNeverReplacesDecision(t *testing.T) {
	for _, fail := range []string{"error", "deadline", "late success"} {
		t.Run(fail, func(t *testing.T) {
			pub := &recordPublisher{hook: func(ctx context.Context, _ event.Event) error {
				if fail == "error" {
					return errors.New("SECRET-publisher-error")
				}
				<-ctx.Done()
				if fail == "late success" {
					return nil
				}
				return ctx.Err()
			}}
			cfg := DefaultConfig("p")
			cfg.Publisher, cfg.AuditTimeout = pub, 20*time.Millisecond
			e := testEngine(t, cfg, constantAdapter("p", testResult(1), nil))
			got, err := e.Decide(context.Background(), testRequest())
			if err != nil || got.Decision != domain.DecisionYes || e.AuditFailures() != 3 {
				t.Fatalf("%+v %v failures=%d", got, err, e.AuditFailures())
			}
		})
	}
}

func TestCanceledTerminalAuditHasIndependentBoundedContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pub := &recordPublisher{hook: func(auditCtx context.Context, evt event.Event) error {
		if evt.Type == event.TypeDecisionFailed {
			if auditCtx.Err() != nil {
				return errors.New("terminal inherits cancellation")
			}
			if _, ok := auditCtx.Deadline(); !ok {
				return errors.New("terminal unbounded")
			}
		}
		return nil
	}}
	p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
		cancel()
		return testResult(1), nil
	}}
	cfg := DefaultConfig("p")
	cfg.Publisher = pub
	e := testEngine(t, cfg, p)
	_, err := e.Decide(ctx, testRequest())
	if !errors.Is(err, context.Canceled) || len(pub.events) != 2 || pub.events[1].Type != event.TypeDecisionFailed || pub.events[1].Payload["outcome"] != string(OutcomeCanceled) || e.AuditFailures() != 1 {
		t.Fatalf("error=%v events=%+v failures=%d", err, pub.events, e.AuditFailures())
	}
}

func TestStartedAuditCancellationStillPublishesTerminal(t *testing.T) {
	for _, cooperativeWait := range []bool{false, true} {
		t.Run(fmt.Sprintf("cooperative_wait=%v", cooperativeWait), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var events []event.Event
			var terminalContextErr error
			terminalBounded, terminalIndependent := false, false
			cfg := DefaultConfig("p")
			cfg.Publisher = publisherFunc(func(publishCtx context.Context, evt event.Event) error {
				events = append(events, evt)
				if evt.Type == event.TypeDecisionStarted {
					cancel()
					if cooperativeWait {
						<-publishCtx.Done()
						return publishCtx.Err()
					}
				} else if evt.Type == event.TypeDecisionFailed {
					terminalContextErr = publishCtx.Err()
					_, terminalBounded = publishCtx.Deadline()
					terminalIndependent = publishCtx.Done() != ctx.Done()
				}
				return nil
			})
			resolveCalls, providerCalls := 0, 0
			p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				providerCalls++
				return testResult(1), nil
			}}
			e, err := NewEngine(resolverFunc(func(context.Context, string) (Provider, error) {
				resolveCalls++
				return p, nil
			}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.Decide(ctx, testRequest())
			if got != (domain.DecisionResult{}) || !errors.Is(err, context.Canceled) || resolveCalls != 0 || providerCalls != 0 {
				t.Fatalf("got=%+v error=%v resolveCalls=%d providerCalls=%d", got, err, resolveCalls, providerCalls)
			}
			if len(events) != 2 || events[0].Type != event.TypeDecisionStarted || events[1].Type != event.TypeDecisionFailed {
				t.Fatalf("expected Started then exactly one Failed, without attempts: %+v", events)
			}
			terminal := events[1]
			if terminal.Payload["provider"] != "" || terminal.Payload["attempt"] != 0 || terminal.Payload["outcome"] != string(OutcomeCanceled) || terminalContextErr != nil || !terminalBounded || !terminalIndependent {
				t.Fatalf("incorrect no-attempt cancellation terminal: %+v contextErr=%v bounded=%v independent=%v", terminal, terminalContextErr, terminalBounded, terminalIndependent)
			}
			if e.AuditFailures() != 1 {
				t.Fatalf("publication failures=%d", e.AuditFailures())
			}
		})
	}
}

func TestFailedTerminalAuditUsesFinalAttemptedHandle(t *testing.T) {
	for _, mode := range []string{"exhausted provider", "failed resolver", "in-attempt cancellation", "provenance mismatch"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pub := &recordPublisher{}
			cfg := DefaultConfig("first", "final")
			cfg.Publisher, cfg.RetryDelay = pub, 0
			cause := errors.New("SECRET-provider-error")
			first := constantAdapter("first", domain.DecisionResult{}, ErrTransient)
			final := &adapter{id: "final", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				if mode == "in-attempt cancellation" {
					cancel()
				}
				if mode == "provenance mismatch" {
					result := testResult(1)
					result.Provider = "SECRET-untrusted-provider"
					return result, nil
				}
				return domain.DecisionResult{}, cause
			}}
			var resolved []string
			e, err := NewEngine(resolverFunc(func(_ context.Context, id string) (Provider, error) {
				resolved = append(resolved, id)
				if id == "first" {
					return first, nil
				}
				if mode == "failed resolver" {
					return nil, cause
				}
				return final, nil
			}), cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.Decide(ctx, testRequest())
			wantErr, wantOutcome := ErrExhausted, OutcomeExhausted
			if mode == "in-attempt cancellation" {
				wantErr, wantOutcome = context.Canceled, OutcomeCanceled
			}
			if got != (domain.DecisionResult{}) || !errors.Is(err, wantErr) || !reflect.DeepEqual(resolved, []string{"first", "first", "final"}) {
				t.Fatalf("got=%+v error=%v resolved=%v", got, err, resolved)
			}
			terminalCount := 0
			for _, evt := range pub.events {
				if evt.Type == event.TypeDecisionFailed || evt.Type == event.TypeDecisionCompleted {
					terminalCount++
				}
				data, marshalErr := json.Marshal(evt)
				if marshalErr != nil || strings.Contains(string(data), "SECRET") {
					t.Fatalf("unsafe audit: %s %v", data, marshalErr)
				}
			}
			if terminalCount != 1 {
				t.Fatalf("terminal count=%d", terminalCount)
			}
			terminal := pub.events[len(pub.events)-1]
			if terminal.Type != event.TypeDecisionFailed || terminal.Payload["provider"] != "final" || terminal.Payload["attempt"] != 3 || terminal.Payload["outcome"] != string(wantOutcome) {
				t.Fatalf("failed terminal must identify final configured attempt (final, 3): %+v", terminal)
			}
		})
	}
}

func TestCompletedAuditKeepsSavedUncertainSource(t *testing.T) {
	pub := &recordPublisher{}
	cfg := DefaultConfig("source", "failed-fallback")
	cfg.Publisher = pub
	e := testEngine(t, cfg, constantAdapter("source", testResult(.5), nil), constantAdapter("failed-fallback", domain.DecisionResult{}, ErrUnsupportedKind))
	got, err := e.Decide(context.Background(), testRequest())
	if err != nil || got.Provider != "source" || got.Decision != domain.DecisionUncertain {
		t.Fatalf("got=%+v error=%v", got, err)
	}
	if len(pub.events) != 4 {
		t.Fatalf("events=%+v", pub.events)
	}
	terminal := pub.events[3]
	if terminal.Type != event.TypeDecisionCompleted || terminal.Payload["provider"] != "source" || terminal.Payload["attempt"] != 2 || terminal.Payload["verdict"] != "uncertain" {
		t.Fatalf("Completed must preserve successful source, not last failed handle: %+v", terminal)
	}
}

func TestCancellationDuringTerminalAuditWins(t *testing.T) {
	for _, mode := range []string{"certain", "saved uncertain", "exhausted"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cause := errors.New("SECRET-provider-failure")
			terminalType, terminalOutcome := event.TypeDecisionCompleted, OutcomeValid
			provided := testResult(1)
			var providerErr error
			cfg := DefaultConfig("p")
			cfg.MaxAttempts = 1
			wantCalls := 1
			if mode == "saved uncertain" {
				provided.Score = .5
				cfg.Providers = append(cfg.Providers, "fallback")
				wantCalls = 2
			} else if mode == "exhausted" {
				providerErr = cause
				terminalType, terminalOutcome = event.TypeDecisionFailed, OutcomeExhausted
			}
			var events []event.Event
			terminalAttempts, calls := 0, 0
			cfg.Publisher = publisherFunc(func(_ context.Context, evt event.Event) error {
				events = append(events, evt)
				if evt.Type == event.TypeDecisionCompleted || evt.Type == event.TypeDecisionFailed {
					terminalAttempts++
					cancel()
				}
				return nil // cancellation is caller state, not a publisher error
			})
			p := &adapter{id: "p", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				calls++
				return provided, providerErr
			}}
			fallback := &adapter{id: "fallback", decide: func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				calls++
				return domain.DecisionResult{}, cause
			}}
			e := testEngine(t, cfg, p, fallback)
			got, err := e.Decide(ctx, testRequest())
			if !errors.Is(err, context.Canceled) || got != (domain.DecisionResult{}) {
				t.Errorf("parent cancellation lost during terminal audit: result=%+v error=%v", got, err)
			}
			var detail *Error
			if !errors.As(err, &detail) || detail.Outcome() != OutcomeCanceled || strings.Contains(err.Error(), "SECRET") {
				t.Errorf("unsafe or incorrect returned outcome: %v", err)
			}
			if mode == "exhausted" && !errors.Is(err, cause) {
				t.Error("original provider cause lost")
			}
			if terminalAttempts != 1 || calls != wantCalls || len(events) != wantCalls+2 {
				t.Fatalf("unexpected retry/second terminal audit: terminals=%d calls=%d events=%d", terminalAttempts, calls, len(events))
			}
			terminal := events[len(events)-1]
			if terminal.Type != terminalType || terminal.Payload["outcome"] != string(terminalOutcome) {
				t.Fatalf("terminal record must describe state selected before publication: %+v", terminal)
			}
			if terminalType == event.TypeDecisionCompleted && terminal.Payload["score"] != provided.Score {
				t.Fatal("returned cancellation mutated the published terminal record")
			}
		})
	}
}

func TestAuditOutcomesAndRejectedRequests(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code Outcome
	}{
		{"canceled adapter", context.Canceled, OutcomeAdapterCanceled},
		{"adapter deadline", context.DeadlineExceeded, OutcomeProviderFailure},
		{"transient", ErrTransient, OutcomeTransient},
		{"malformed", ErrMalformedResult, OutcomeMalformed},
		{"provenance", ErrIdentityMismatch, OutcomeIdentityMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pub := &recordPublisher{}
			cfg := DefaultConfig("p")
			cfg.Publisher, cfg.MaxAttempts = pub, 1
			e := testEngine(t, cfg, constantAdapter("p", domain.DecisionResult{}, tc.err))
			_, err := e.Decide(context.Background(), testRequest())
			if !errors.Is(err, ErrExhausted) || len(pub.events) != 3 || pub.events[1].Payload["outcome"] != string(tc.code) || pub.events[2].Type != event.TypeDecisionFailed {
				t.Fatalf("%v %+v", err, pub.events)
			}
		})
	}
	pub := &recordPublisher{}
	cfg := DefaultConfig("p")
	cfg.Publisher, cfg.MaxAttempts, cfg.AttemptTimeout = pub, 1, 20*time.Millisecond
	p := &adapter{id: "p", decide: func(ctx context.Context, _ domain.DecisionRequest) (domain.DecisionResult, error) {
		<-ctx.Done()
		return testResult(1), nil
	}}
	e := testEngine(t, cfg, p)
	_, err := e.Decide(context.Background(), testRequest())
	if !errors.Is(err, context.DeadlineExceeded) || pub.events[1].Payload["outcome"] != string(OutcomeTimeout) {
		t.Fatalf("%v %+v", err, pub.events)
	}
	before := len(pub.events)
	_, _ = e.Decide(context.Background(), domain.DecisionRequest{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = e.Decide(ctx, testRequest())
	if len(pub.events) != before {
		t.Fatal("rejected requests audited")
	}
}

func TestAuditWithClosedAndFullEventBus(t *testing.T) {
	for _, closed := range []bool{false, true} {
		bus := event.NewBus(event.WithBufferSize(1))
		defer bus.Close()
		ch, err := bus.Subscribe(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if closed {
			if err := bus.Close(); err != nil {
				t.Fatal(err)
			}
		}
		cfg := DefaultConfig("p")
		cfg.Publisher = bus
		e := testEngine(t, cfg, constantAdapter("p", testResult(1), nil))
		got, err := e.Decide(context.Background(), testRequest())
		if err != nil || got.Decision != domain.DecisionYes {
			t.Fatalf("%+v %v", got, err)
		}
		if closed {
			if e.AuditFailures() != 3 {
				t.Fatalf("closed failures=%d", e.AuditFailures())
			}
		} else {
			if e.AuditFailures() != 0 || bus.TotalDropped() != 2 {
				t.Fatalf("full failures=%d drops=%d", e.AuditFailures(), bus.TotalDropped())
			}
			if evt := <-ch; evt.Type != event.TypeDecisionStarted {
				t.Fatalf("old event dropped: %+v", evt)
			}
		}
	}
}

func TestConcurrentAuditFailureCounter(t *testing.T) {
	pub := &recordPublisher{hook: func(context.Context, event.Event) error { return errors.New("publisher failure") }}
	cfg := DefaultConfig("p")
	cfg.Publisher = pub
	e := testEngine(t, cfg, constantAdapter("p", testResult(1), nil))
	const n = 24
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := e.Decide(context.Background(), testRequest())
			if err == nil && got.Decision != domain.DecisionYes {
				err = errors.New("wrong classification")
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if e.AuditFailures() != 3*n {
		t.Fatalf("lost failure increments: %d", e.AuditFailures())
	}
}
