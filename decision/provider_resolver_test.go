package decision

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/event"
	"github.com/manish96170/videofactory/provider"
)

func bridgeCaps(id string) domain.ProviderCapabilities {
	return domain.ProviderCapabilities{ProviderID: id, Local: true, Tags: []string{"semantic-decision"}, Hardware: &domain.HardwareRequirements{}}
}
func bridgeAdapter(id string, result domain.DecisionResult, err error) *adapter {
	p := constantAdapter(id, result, err)
	p.caps = func(context.Context) (domain.ProviderCapabilities, error) { return bridgeCaps(id), nil }
	return p
}
func bridgeRegister(t *testing.T, r *provider.Registry, p provider.Provider) {
	t.Helper()
	if err := r.Register(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := r.SetHealth(context.Background(), p.ID(), domain.ProviderHealthHealthy); err != nil {
		t.Fatal(err)
	}
}
func bridgeRouter(t *testing.T, r *provider.Registry) *provider.Router {
	t.Helper()
	router, err := provider.NewRouter(r)
	if err != nil {
		t.Fatal(err)
	}
	return router
}
func bridgeResolver(t *testing.T, router *provider.Router, req provider.RouteRequest) *ProviderResolver {
	t.Helper()
	resolver, err := NewProviderResolver(router, req)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}
func bridgePtr[T any](v T) *T { return &v }

// Declaring a semantic-decision tag does not supply a Decide implementation.
type metadataOnly struct {
	id   string
	tags []string
}

func (p *metadataOnly) ID() string { return p.id }
func (p *metadataOnly) Capabilities(context.Context) (domain.ProviderCapabilities, error) {
	c := bridgeCaps(p.id)
	c.Tags = p.tags
	return c, nil
}

func TestProviderResolverPolicyOwnershipAndSemanticAdmission(t *testing.T) {
	r := provider.NewRegistry()
	p := bridgeAdapter("p", testResult(1), nil)
	p.caps = func(context.Context) (domain.ProviderCapabilities, error) {
		c := bridgeCaps("p")
		c.Tags = append(c.Tags, "extra")
		c.CostUnit = "USD/decision"
		c.CostPerUnit = 1
		c.Hardware.MinMemoryGB, c.Hardware.RequiresGPU = 4, true
		c.EstimatedLatencyMs = bridgePtr(int64(50))
		return c, nil
	}
	bridgeRegister(t, r, p)
	router := bridgeRouter(t, r)
	policy := provider.RouteRequest{RequiredTags: []string{"extra"}, PreferredIDs: []string{"p"}, MaxCost: &provider.CostLimit{Amount: 1, Unit: "USD/decision"}, MaxLatencyMs: bridgePtr(int64(50)), Hardware: provider.HardwareFacts{AvailableMemoryGB: bridgePtr(float64(4)), HasGPU: bridgePtr(true)}}
	before := policy.Clone()
	resolver := bridgeResolver(t, router, policy)
	if !reflect.DeepEqual(policy, before) {
		t.Fatal("constructor modified caller policy")
	}
	policy.RequiredTags[0], policy.PreferredIDs[0], policy.MaxCost.Amount, policy.MaxCost.Unit, *policy.MaxLatencyMs, *policy.Hardware.AvailableMemoryGB, *policy.Hardware.HasGPU = "PRIVATE-changed", "changed", 0, "changed", 0, 0, false
	if got, err := resolver.Resolve(context.Background(), "p"); err != nil || got != p {
		t.Fatalf("policy alias: %v", err)
	}
	if !reflect.DeepEqual(resolver.policy.RequiredTags, []string{"extra", "semantic-decision"}) || resolver.policy.PreferredIDs[0] != "p" {
		t.Fatal("nested policy not copied")
	}
	// Removing semantic capability after construction prevents resolution.
	p.caps = func(context.Context) (domain.ProviderCapabilities, error) {
		c := bridgeCaps("p")
		c.Tags = []string{"extra"}
		return c, nil
	}
	if err := r.Refresh(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	if got, err := resolver.Resolve(context.Background(), "p"); got != nil || !errors.Is(err, ErrInvalidProvider) || !errors.Is(err, provider.ErrIneligible) {
		t.Fatalf("semantic admission: %v", err)
	}
	for _, req := range []provider.RouteRequest{{RequiredTags: []string{" "}}, {PreferredIDs: []string{"p", "p"}}, {MaxLatencyMs: bridgePtr(int64(-1))}} {
		if _, err := NewProviderResolver(router, req); !errors.Is(err, ErrInvalidConfig) {
			t.Fatal(err)
		}
	}
	if _, err := NewProviderResolver(nil, provider.RouteRequest{}); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
}

func TestProviderResolverFixtureEngineIntegration(t *testing.T) {
	f, err := NewFixtureProvider("fixture", map[string]domain.DecisionResult{"kind": {Decision: domain.DecisionNo, Score: .95}})
	if err != nil {
		t.Fatal(err)
	}
	r := provider.NewRegistry()
	bridgeRegister(t, r, f)
	router := bridgeRouter(t, r)
	req := provider.RouteRequest{RequiredTags: []string{"semantic-decision"}, MaxCost: &provider.CostLimit{Amount: 0, Unit: "USD/decision"}}
	routed, err := router.Route(context.Background(), req)
	if err != nil || !reflect.DeepEqual(routed.EligibleIDs, []string{"fixture"}) {
		t.Fatalf("%+v %v", routed, err)
	}
	resolver := bridgeResolver(t, router, req)
	e, err := NewEngine(resolver, DefaultConfig(routed.EligibleIDs...))
	if err != nil {
		t.Fatal(err)
	}
	got, err := e.Decide(context.Background(), testRequest())
	if err != nil || got.Decision != domain.DecisionYes || got.Score != .95 || got.Provider != "fixture" {
		t.Fatalf("%+v %v", got, err)
	}
	caps, _ := f.Capabilities(context.Background())
	if !caps.Supports("deterministic") || caps.Hardware == nil || caps.CostUnit != "USD/decision" || caps.CostPerUnit != 0 || caps.EstimatedLatencyMs != nil {
		t.Fatalf("fixture declaration: %+v", caps)
	}
	caps.Hardware.MinMemoryGB = 100
	caps.Tags[1] = "changed"
	caps, _ = f.Capabilities(context.Background())
	if caps.Hardware.MinMemoryGB != 0 || !caps.Supports("deterministic") {
		t.Fatal("fixture metadata retained mutation")
	}
}

func TestProviderResolverEnginePermanentAdmissionAndIdentityDoNotRetry(t *testing.T) {
	for _, failure := range []string{"bad interface", "identity", "health", "capability", "missing"} {
		t.Run(failure, func(t *testing.T) {
			r := provider.NewRegistry()
			p := bridgeAdapter("p", testResult(1), nil)
			decideCalls := 0
			p.decide = func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
				decideCalls++
				return testResult(1), nil
			}
			if failure == "bad interface" {
				bridgeRegister(t, r, &metadataOnly{id: "p", tags: []string{"semantic-decision"}})
			} else if failure != "missing" {
				bridgeRegister(t, r, p)
			}
			next := bridgeAdapter("next", testResult(1), nil)
			bridgeRegister(t, r, next)
			router := bridgeRouter(t, r)
			resolver := bridgeResolver(t, router, provider.RouteRequest{})
			if failure == "identity" {
				p.id = "PRIVATE-drift"
			}
			if failure == "health" {
				if err := r.SetHealth(context.Background(), "p", domain.ProviderHealthDisabled); err != nil {
					t.Fatal(err)
				}
			}
			if failure == "capability" {
				p.caps = func(context.Context) (domain.ProviderCapabilities, error) {
					c := bridgeCaps("p")
					c.Tags = []string{"video"}
					return c, nil
				}
				if err := r.Refresh(context.Background(), "p"); err != nil {
					t.Fatal(err)
				}
			}
			want := ErrInvalidProvider
			outcome := OutcomeInvalidProvider
			if failure == "identity" {
				want, outcome = ErrIdentityMismatch, OutcomeIdentityMismatch
			}
			if failure == "missing" {
				want, outcome = ErrProviderNotFound, OutcomeNotFound
			}
			if got, err := resolver.Resolve(context.Background(), "p"); got != nil || !errors.Is(err, want) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("exact resolution: %v", err)
			}
			pub := &recordPublisher{}
			cfg := DefaultConfig("p", "next")
			cfg.MaxAttempts, cfg.RetryDelay, cfg.Publisher = 3, 0, pub
			e, err := NewEngine(resolver, cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := e.Decide(context.Background(), testRequest())
			if err != nil || got.Provider != "next" || decideCalls != 0 {
				t.Fatalf("%+v %v calls=%d", got, err, decideCalls)
			}
			if len(pub.events) != 4 || pub.events[1].Type != event.TypeDecisionAttemptCompleted || pub.events[1].Payload["outcome"] != string(outcome) || pub.events[1].Payload["action"] != "fallback" {
				t.Fatalf("permanent failure retried: %+v", pub.events)
			}
			cfg.Providers = []string{"p"}
			cfg.Publisher = nil
			e, err = NewEngine(resolver, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.Decide(context.Background(), testRequest()); !errors.Is(err, ErrExhausted) || !errors.Is(err, want) {
				t.Fatal(err)
			}
		})
	}
}

type bridgePrivateCause struct{}

func (*bridgePrivateCause) Error() string { return "PRIVATE-original-cause" }

func TestProviderResolutionErrorMappingPreservesCausesAndPermanentPrecedence(t *testing.T) {
	for _, tc := range []struct{ generic, want error }{
		{provider.ErrIdentityMismatch, ErrIdentityMismatch}, {provider.ErrProviderNotFound, ErrProviderNotFound},
		{provider.ErrInvalidProvider, ErrInvalidProvider}, {provider.ErrIneligible, ErrInvalidProvider},
		{provider.ErrInvalidRequest, ErrInvalidProvider}, {provider.ErrInvalidConfig, ErrInvalidProvider}, {provider.ErrNoEligibleProviders, ErrInvalidProvider},
	} {
		private := &bridgePrivateCause{}
		mapped := providerResolutionError(errors.Join(tc.generic, ErrTransient, private))
		var original *bridgePrivateCause
		if !errors.Is(mapped, tc.generic) || !errors.Is(mapped, tc.want) || !errors.Is(mapped, ErrTransient) || !errors.As(mapped, &original) || original != private || strings.Contains(mapped.Error(), "PRIVATE") {
			t.Fatal(mapped)
		}
		calls := 0
		cfg := DefaultConfig("p")
		cfg.MaxAttempts, cfg.RetryDelay = 3, 0
		e, err := NewEngine(resolverFunc(func(context.Context, string) (Provider, error) { calls++; return nil, mapped }), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Decide(context.Background(), testRequest()); !errors.Is(err, tc.want) || calls != 1 {
			t.Fatalf("invalid adapter retried %d: %v", calls, err)
		}
	}
}

func TestProviderRouterBridgePreservesRetryUncertaintyInputAndAuditRedaction(t *testing.T) {
	for _, finalFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "certain fallback", true: "saved uncertainty"}[finalFailure], func(t *testing.T) {
			r := provider.NewRegistry()
			retryCalls, uncertainCalls, finalCalls := 0, 0, 0
			checkInput := func(req domain.DecisionRequest) error {
				if req.Input["nested"].(map[string]any)["list"].([]any)[0] != "PRIVATE-input" {
					return errors.New("input was not isolated")
				}
				req.Input["nested"].(map[string]any)["list"].([]any)[0] = "mutated"
				return nil
			}
			retry := bridgeAdapter("retry", testResult(1), nil)
			retry.decide = func(_ context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
				retryCalls++
				if err := checkInput(req); err != nil {
					return domain.DecisionResult{}, err
				}
				return domain.DecisionResult{}, errors.Join(ErrTransient, &bridgePrivateCause{})
			}
			uncertain := bridgeAdapter("uncertain", testResult(.5), nil)
			uncertain.decide = func(_ context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
				uncertainCalls++
				if err := checkInput(req); err != nil {
					return domain.DecisionResult{}, err
				}
				result := testResult(.5)
				result.ReasonCode = "PRIVATE-reason"
				return result, nil
			}
			last := bridgeAdapter("last", testResult(.95), nil)
			last.decide = func(_ context.Context, req domain.DecisionRequest) (domain.DecisionResult, error) {
				finalCalls++
				if err := checkInput(req); err != nil {
					return domain.DecisionResult{}, err
				}
				if finalFailure {
					return domain.DecisionResult{}, &bridgePrivateCause{}
				}
				return testResult(.95), nil
			}
			for _, p := range []provider.Provider{last, uncertain, retry} {
				bridgeRegister(t, r, p)
			}
			router := bridgeRouter(t, r)
			policy := provider.RouteRequest{RequiredTags: []string{"semantic-decision"}, PreferredIDs: []string{"retry", "uncertain", "last"}}
			routed, err := router.Route(context.Background(), policy)
			if err != nil {
				t.Fatal(err)
			}
			pub := &recordPublisher{}
			cfg := DefaultConfig(routed.EligibleIDs...)
			cfg.RetryDelay, cfg.Publisher = 0, pub
			e, err := NewEngine(bridgeResolver(t, router, policy), cfg)
			if err != nil {
				t.Fatal(err)
			}
			req := testRequest()
			req.Kind = "PRIVATE-kind"
			req.Input = map[string]any{"nested": map[string]any{"list": []any{"PRIVATE-input"}}}
			before, _ := json.Marshal(req.Input)
			got, err := e.Decide(context.Background(), req)
			after, _ := json.Marshal(req.Input)
			wantProvider := "last"
			wantVerdict := domain.DecisionYes
			if finalFailure {
				wantProvider, wantVerdict = "uncertain", domain.DecisionUncertain
			}
			if err != nil || got.Provider != wantProvider || got.Decision != wantVerdict || retryCalls != 2 || uncertainCalls != 1 || finalCalls != 1 || string(before) != string(after) {
				t.Fatalf("%+v %v calls %d/%d/%d", got, err, retryCalls, uncertainCalls, finalCalls)
			}
			data, err := json.Marshal(pub.events)
			if err != nil || strings.Contains(string(data), "PRIVATE") || strings.Contains(string(data), "mutated") {
				t.Fatalf("audit unsafe: %s %v", data, err)
			}
			if len(pub.events) != 6 || pub.events[1].Payload["action"] != "retry" || pub.events[3].Payload["action"] != "escalate" {
				t.Fatal("retry/uncertainty policy changed")
			}
		})
	}
}

type bridgeIdentityAdapter struct {
	*adapter
	identity func() string
}

func (p *bridgeIdentityAdapter) ID() string { return p.identity() }

func TestProviderResolverPostIdentityCancellationAndDrift(t *testing.T) {
	for _, cancelAt := range []int{1, 2} {
		r := provider.NewRegistry()
		p := &bridgeIdentityAdapter{adapter: bridgeAdapter("p", testResult(1), nil), identity: func() string { return "p" }}
		bridgeRegister(t, r, p)
		resolver := bridgeResolver(t, bridgeRouter(t, r), provider.RouteRequest{})
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		p.identity = func() string {
			calls++
			if calls == cancelAt {
				cancel()
			}
			return "p"
		}
		if got, err := resolver.Resolve(ctx, "p"); got != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("identity call %d lost cancellation: %v", cancelAt, err)
		}
		cancel()
	}
	r := provider.NewRegistry()
	p := &bridgeIdentityAdapter{adapter: bridgeAdapter("p", testResult(1), nil), identity: func() string { return "p" }}
	bridgeRegister(t, r, p)
	resolver := bridgeResolver(t, bridgeRouter(t, r), provider.RouteRequest{})
	calls := 0
	p.identity = func() string {
		calls++
		if calls == 2 {
			return "PRIVATE-drift"
		}
		return "p"
	}
	if got, err := resolver.Resolve(context.Background(), "p"); got != nil || !errors.Is(err, ErrIdentityMismatch) || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal(err)
	}
}

func TestProviderRouterBridgePostDecideIdentityBeatsTransientCause(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		r := provider.NewRegistry()
		calls := 0
		private := &bridgePrivateCause{}
		p := bridgeAdapter("p", testResult(1), nil)
		p.decide = func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
			calls++
			p.id = "PRIVATE-drift"
			return testResult(1), errors.Join(ErrTransient, private)
		}
		bridgeRegister(t, r, p)
		next := bridgeAdapter("next", testResult(1), nil)
		bridgeRegister(t, r, next)
		cfg := DefaultConfig("p")
		cfg.MaxAttempts, cfg.RetryDelay = 3, 0
		if fallback {
			cfg.Providers = []string{"p", "next"}
		}
		pub := &recordPublisher{}
		cfg.Publisher = pub
		e, err := NewEngine(bridgeResolver(t, bridgeRouter(t, r), provider.RouteRequest{}), cfg)
		if err != nil {
			t.Fatal(err)
		}
		got, err := e.Decide(context.Background(), testRequest())
		if calls != 1 {
			t.Fatalf("identity failure retried %d times", calls)
		}
		if fallback {
			if err != nil || got.Provider != "next" {
				t.Fatalf("%+v %v", got, err)
			}
		} else {
			var original *bridgePrivateCause
			if !errors.Is(err, ErrExhausted) || !errors.Is(err, ErrIdentityMismatch) || !errors.Is(err, ErrTransient) || !errors.As(err, &original) || original != private || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("provenance/causes: %v", err)
			}
		}
		if pub.events[1].Payload["outcome"] != string(OutcomeIdentityMismatch) {
			t.Fatal("identity classification lost")
		}
		data, err := json.Marshal(pub.events)
		if err != nil || strings.Contains(string(data), "PRIVATE") {
			t.Fatalf("%s %v", data, err)
		}
	}
}
