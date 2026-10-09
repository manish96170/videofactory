package decision

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func TestRegistryValidationAndCancellation(t *testing.T) {
	var registry Registry // zero value supported
	cause := errors.New("SECRET-capabilities-error")
	for _, tc := range []struct {
		name string
		p    CapabilityAdapter
		want error
	}{
		{"nil", nil, ErrInvalidProvider},
		{"typed nil", (*adapter)(nil), ErrInvalidProvider},
		{"empty id", &adapter{}, ErrInvalidProvider},
		{"blank id", &adapter{id: " "}, ErrInvalidProvider},
		{"mismatched caps", &adapter{id: "p", caps: func(context.Context) (domain.ProviderCapabilities, error) {
			return domain.ProviderCapabilities{ProviderID: "other", Tags: []string{"semantic-decision"}}, nil
		}}, ErrIdentityMismatch},
		{"missing tag", &adapter{id: "p", caps: func(context.Context) (domain.ProviderCapabilities, error) {
			return domain.ProviderCapabilities{ProviderID: "p", Tags: []string{"video"}}, nil
		}}, ErrInvalidProvider},
		{"caps failure", &adapter{id: "p", caps: func(context.Context) (domain.ProviderCapabilities, error) {
			return domain.ProviderCapabilities{}, cause
		}}, cause},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := registry.Register(context.Background(), tc.p); !errors.Is(err, tc.want) {
				t.Fatalf("%v", err)
			}
		})
	}
	p := constantAdapter("p", testResult(1), nil)
	if err := registry.Register(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(context.Background(), p); !errors.Is(err, ErrDuplicateProvider) {
		t.Fatal(err)
	}
	got, err := registry.Resolve(context.Background(), "p")
	if err != nil || got != p {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := registry.Resolve(context.Background(), "missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := registry.Register(ctx, p); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(ctx, "p"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	cancels := &adapter{id: "canceled", caps: func(context.Context) (domain.ProviderCapabilities, error) {
		cancel()
		return domain.ProviderCapabilities{ProviderID: "canceled", Tags: []string{"semantic-decision"}}, nil
	}}
	if err := registry.Register(ctx, cancels); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := registry.Resolve(context.Background(), "canceled"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatal("canceled adapter registered")
	}
	unstable := &adapter{id: "unstable"}
	unstable.caps = func(context.Context) (domain.ProviderCapabilities, error) {
		unstable.id = "changed"
		return domain.ProviderCapabilities{ProviderID: "unstable", Tags: []string{"semantic-decision"}}, nil
	}
	if err := registry.Register(context.Background(), unstable); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatal(err)
	}
}

func TestRegistryCallsAdaptersOutsideLockAndConcurrentAccess(t *testing.T) {
	r := NewRegistry()
	// Reentrant registry access from Capabilities would deadlock under the lock.
	p := constantAdapter("p", testResult(1), nil)
	p.caps = func(ctx context.Context) (domain.ProviderCapabilities, error) {
		_, _ = r.Resolve(ctx, "missing")
		return domain.ProviderCapabilities{ProviderID: "p", Tags: []string{"semantic-decision"}}, nil
	}
	if err := r.Register(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	const n = 32
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			id := fmt.Sprintf("p-%d", i)
			p := constantAdapter(id, testResult(1), nil)
			if err := r.Register(context.Background(), p); err != nil {
				errs <- err
				return
			}
			got, err := r.Resolve(context.Background(), id)
			if err == nil && got != p {
				err = errors.New("wrong reference")
			}
			errs <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	// Concurrent duplicate registration accepts exactly one reference.
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errCh <- r.Register(context.Background(), constantAdapter("same", testResult(1), nil))
		}()
	}
	wg.Wait()
	close(errCh)
	success := 0
	for err := range errCh {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrDuplicateProvider) {
			t.Error(err)
		}
	}
	if success != 1 {
		t.Fatalf("success=%d", success)
	}
}

func TestResolverValidationAndAdapterIdentityChanges(t *testing.T) {
	for _, resolved := range []Provider{nil, (*adapter)(nil)} {
		e, err := NewEngine(resolverFunc(func(context.Context, string) (Provider, error) { return resolved, nil }), DefaultConfig("p"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Decide(context.Background(), testRequest()); !errors.Is(err, ErrInvalidProvider) {
			t.Fatal(err)
		}
	}
	var nilResolver *Registry
	if _, err := NewEngine(nilResolver, DefaultConfig("p")); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	p := constantAdapter("p", testResult(1), nil)
	p.decide = func(context.Context, domain.DecisionRequest) (domain.DecisionResult, error) {
		p.id = "changed"
		return testResult(1), nil
	}
	e := testEngine(t, DefaultConfig("p"), p)
	if _, err := e.Decide(context.Background(), testRequest()); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatal(err)
	}
}

func TestFixtureProviderTableOwnershipAndCapabilities(t *testing.T) {
	table := map[string]domain.DecisionResult{"kind": {Decision: domain.DecisionNo, Score: .95, ReasonCode: "table-code"}}
	f, err := NewFixtureProvider("fixture", table)
	if err != nil {
		t.Fatal(err)
	}
	table["kind"] = testResult(.01)
	delete(table, "kind")
	for i := 0; i < 3; i++ {
		got, err := f.Decide(context.Background(), testRequest())
		if err != nil || got.Decision != domain.DecisionNo || got.Score != .95 || got.ReasonCode != "table-code" || got.Provider != "fixture" {
			t.Fatalf("%+v %v", got, err)
		}
		got.Score = 0 // returned scalar value cannot mutate the table
	}
	caps, err := f.Capabilities(context.Background())
	if err != nil || caps.ProviderID != f.ID() || !caps.Supports("semantic-decision") {
		t.Fatalf("%+v %v", caps, err)
	}
	caps.Tags[0] = "changed"
	caps, _ = f.Capabilities(context.Background())
	if !caps.Supports("semantic-decision") {
		t.Fatal("capability alias")
	}
	req := testRequest()
	req.Kind = "SECRET-unknown"
	if _, err := f.Decide(context.Background(), req); !errors.Is(err, ErrUnsupportedKind) || err.Error() != "decision: unsupported_kind" {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Decide(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := f.Capabilities(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	r := NewRegistry()
	if err := r.Register(context.Background(), f); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(r, DefaultConfig("fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := e.Decide(context.Background(), testRequest()); err != nil || got.Decision != domain.DecisionYes {
		t.Fatalf("%+v %v", got, err)
	}
	for _, tc := range []struct {
		id    string
		table map[string]domain.DecisionResult
		want  error
	}{
		{" ", nil, ErrInvalidProvider}, {"p", map[string]domain.DecisionResult{" ": testResult(1)}, ErrMalformedResult},
		{"p", map[string]domain.DecisionResult{"kind": {}}, ErrMalformedResult},
		{"p", map[string]domain.DecisionResult{"kind": {Decision: domain.DecisionYes, Provider: "other"}}, ErrIdentityMismatch},
	} {
		if _, err := NewFixtureProvider(tc.id, tc.table); !errors.Is(err, tc.want) {
			t.Fatal(err)
		}
	}
}
