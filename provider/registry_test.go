package provider

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
)

type catalogProvider struct {
	id     string
	idFn   func() string
	capsFn func(context.Context) (domain.ProviderCapabilities, error)
}

func (p *catalogProvider) ID() string {
	if p.idFn != nil {
		return p.idFn()
	}
	return p.id
}
func (p *catalogProvider) Capabilities(ctx context.Context) (domain.ProviderCapabilities, error) {
	return p.capsFn(ctx)
}

func declared(id string) domain.ProviderCapabilities {
	return domain.ProviderCapabilities{ProviderID: id, Tags: []string{"video"}, Local: true, Hardware: &domain.HardwareRequirements{}}
}
func catalog(c domain.ProviderCapabilities) *catalogProvider {
	return &catalogProvider{id: c.ProviderID, capsFn: func(context.Context) (domain.ProviderCapabilities, error) { return c, nil }}
}
func ptr[T any](v T) *T { return &v }

func mustRegister(t *testing.T, r *Registry, p Provider, healthy bool) {
	t.Helper()
	if err := r.Register(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if healthy {
		if err := r.SetHealth(context.Background(), p.ID(), domain.ProviderHealthHealthy); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRegistryAdmissionValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*domain.ProviderCapabilities)
		want   error
	}{
		{"blank ID", func(c *domain.ProviderCapabilities) { c.ProviderID = " " }, ErrInvalidProvider},
		{"blank tag", func(c *domain.ProviderCapabilities) { c.Tags = []string{"video", " "} }, ErrInvalidProvider},
		{"negative cost", func(c *domain.ProviderCapabilities) { c.CostPerUnit = -1 }, ErrInvalidProvider},
		{"nan cost", func(c *domain.ProviderCapabilities) { c.CostPerUnit = math.NaN() }, ErrInvalidProvider},
		{"infinite cost", func(c *domain.ProviderCapabilities) { c.CostPerUnit = math.Inf(1) }, ErrInvalidProvider},
		{"unlabelled cost", func(c *domain.ProviderCapabilities) { c.CostPerUnit = 1 }, ErrInvalidProvider},
		{"blank unit", func(c *domain.ProviderCapabilities) { c.CostUnit = " " }, ErrInvalidProvider},
		{"negative latency", func(c *domain.ProviderCapabilities) { c.EstimatedLatencyMs = ptr(int64(-1)) }, ErrInvalidProvider},
		{"negative memory", func(c *domain.ProviderCapabilities) { c.Hardware.MinMemoryGB = -1 }, ErrInvalidProvider},
		{"nan memory", func(c *domain.ProviderCapabilities) { c.Hardware.MinMemoryGB = math.NaN() }, ErrInvalidProvider},
		{"infinite memory", func(c *domain.ProviderCapabilities) { c.Hardware.MinMemoryGB = math.Inf(-1) }, ErrInvalidProvider},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r Registry
			c := declared("p")
			tc.change(&c)
			if err := r.Register(context.Background(), catalog(c)); !errors.Is(err, tc.want) {
				t.Fatal(err)
			}
			if list, err := r.List(context.Background()); err != nil || len(list) != 0 {
				t.Fatalf("partial admission: %+v %v", list, err)
			}
		})
	}
	var r Registry
	for _, p := range []Provider{nil, (*catalogProvider)(nil)} {
		if err := r.Register(context.Background(), p); !errors.Is(err, ErrInvalidProvider) {
			t.Fatal(err)
		}
	}
	c := declared("p")
	p := catalog(c)
	p.id = "other"
	if err := r.Register(context.Background(), p); !errors.Is(err, ErrIdentityMismatch) {
		t.Fatal(err)
	}
	// Unknown estimates and hardware are valid declarations, not eligibility.
	c.Hardware = nil
	mustRegister(t, &r, catalog(c), false)
	_, got, err := r.Get(context.Background(), "p")
	if err != nil || got.Health != domain.ProviderHealthUnknown {
		t.Fatalf("%+v %v", got, err)
	}
	if err := r.Register(context.Background(), catalog(c)); !errors.Is(err, ErrDuplicateProvider) {
		t.Fatal(err)
	}
	if _, _, err := r.Get(context.Background(), "missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatal(err)
	}
	if err := r.Refresh(context.Background(), "missing"); !errors.Is(err, ErrProviderNotFound) {
		t.Fatal(err)
	}
	if err := r.SetHealth(context.Background(), "missing", domain.ProviderHealthHealthy); !errors.Is(err, ErrProviderNotFound) {
		t.Fatal(err)
	}
	if err := r.SetHealth(context.Background(), "p", ""); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, _, err := r.Get(context.Background(), " "); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
}

func TestRegistryCopiesEveryMetadataFieldAndJSON(t *testing.T) {
	c := domain.ProviderCapabilities{
		ProviderID: "p", Tags: []string{"video", "deterministic"}, Local: true,
		CostUnit: "USD/video", CostPerUnit: 2, EstimatedLatencyMs: ptr(int64(50)),
		Hardware: &domain.HardwareRequirements{MinMemoryGB: 4, RequiresGPU: true},
	}
	// Independent literals are the oracle: never copy expected metadata using
	// production clone helpers, which could alias the same mutated source.
	want := Snapshot{
		Capabilities: domain.ProviderCapabilities{
			ProviderID: "p", Tags: []string{"video", "deterministic"}, Local: true,
			CostUnit: "USD/video", CostPerUnit: 2, EstimatedLatencyMs: ptr(int64(50)),
			Hardware: &domain.HardwareRequirements{MinMemoryGB: 4, RequiresGPU: true},
		},
		Health: domain.ProviderHealthHealthy,
	}
	r := NewRegistry()
	p := &catalogProvider{id: "p", capsFn: func(context.Context) (domain.ProviderCapabilities, error) { return c, nil }}
	mustRegister(t, r, p, true)
	assertStored := func(boundary string, expected Snapshot) {
		t.Helper()
		adapter, got, err := r.Get(context.Background(), "p")
		if err != nil || adapter != p || !reflect.DeepEqual(got, expected) {
			t.Fatalf("%s Get changed: got %+v, want %+v, error %v", boundary, got, expected, err)
		}
		list, err := r.List(context.Background())
		if err != nil || !reflect.DeepEqual(list, []Snapshot{expected}) {
			t.Fatalf("%s List changed: got %+v, want %+v, error %v", boundary, list, expected, err)
		}
	}
	mutateCapabilities := func(c *domain.ProviderCapabilities) {
		c.ProviderID, c.Local, c.CostUnit, c.CostPerUnit = "changed", !c.Local, "changed/unit", 99
		for i := range c.Tags {
			c.Tags[i] = "changed"
		}
		c.Hardware.MinMemoryGB, c.Hardware.RequiresGPU, *c.EstimatedLatencyMs = 99, !c.Hardware.RequiresGPU, 99
	}
	assertStored("registration", want)
	mutateCapabilities(&c)
	assertStored("adapter mutation after Register", want)
	mutateReturns := func(expected Snapshot) {
		t.Helper()
		_, got, err := r.Get(context.Background(), "p")
		if err != nil {
			t.Fatal(err)
		}
		mutateCapabilities(&got.Capabilities)
		got.Health = domain.ProviderHealthDisabled
		assertStored("Get result mutation", expected)
		list, err := r.List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		mutateCapabilities(&list[0].Capabilities)
		list[0].Health = domain.ProviderHealthUnhealthy
		assertStored("List result mutation", expected)
	}
	mutateReturns(want)
	// Refresh also takes ownership of all nested metadata independently.
	refreshed := domain.ProviderCapabilities{
		ProviderID: "p", Tags: []string{"video", "image"}, Local: false,
		CostUnit: "EUR/video", CostPerUnit: 3, EstimatedLatencyMs: ptr(int64(60)),
		Hardware: &domain.HardwareRequirements{MinMemoryGB: 5, RequiresGPU: false},
	}
	refreshWant := Snapshot{
		Capabilities: domain.ProviderCapabilities{
			ProviderID: "p", Tags: []string{"video", "image"}, Local: false,
			CostUnit: "EUR/video", CostPerUnit: 3, EstimatedLatencyMs: ptr(int64(60)),
			Hardware: &domain.HardwareRequirements{MinMemoryGB: 5, RequiresGPU: false},
		},
		Health: domain.ProviderHealthHealthy,
	}
	p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) { return refreshed, nil }
	if err := r.Refresh(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	assertStored("refresh", refreshWant)
	mutateCapabilities(&refreshed)
	assertStored("adapter mutation after Refresh", refreshWant)
	mutateReturns(refreshWant)
	for _, health := range []domain.ProviderHealth{domain.ProviderHealthUnknown, domain.ProviderHealthHealthy, domain.ProviderHealthUnhealthy, domain.ProviderHealthDisabled} {
		for _, latency := range []*int64{nil, ptr(int64(0)), ptr(int64(50))} {
			for _, hardware := range []*domain.HardwareRequirements{nil, {}, {MinMemoryGB: 4, RequiresGPU: true}} {
				original := want
				original.Health = health
				original.Capabilities.EstimatedLatencyMs = latency
				original.Capabilities.Hardware = hardware
				data, err := json.Marshal(original)
				if err != nil {
					t.Fatal(err)
				}
				var decoded Snapshot
				if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(original, decoded) {
					t.Fatalf("JSON: %s %+v %v", data, decoded, err)
				}
				if strings.Contains(string(data), "adapter") {
					t.Fatal("adapter serialized")
				}
			}
		}
	}
	// Explicit free vs unknown cost survives omitempty's zero encoding.
	for _, unit := range []string{"", "USD/video"} {
		free := declared("free")
		free.CostUnit = unit
		data, _ := json.Marshal(free)
		var decoded domain.ProviderCapabilities
		if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(free, decoded) {
			t.Fatalf("%s %v", data, err)
		}
	}
}

type privateCause struct{}

func (*privateCause) Error() string { return "PRIVATE-adapter-detail" }

func TestDiscoveryIdentityPrecedesErrorAndPreservesCauses(t *testing.T) {
	transient := errors.New("PRIVATE-transient")
	private := &privateCause{}
	cause := errors.Join(transient, private)
	for _, failure := range []string{"drift", "drift empty caps", "wrong caps", "empty caps", "malformed caps", "stable failure"} {
		t.Run(failure, func(t *testing.T) {
			p := catalog(declared("p"))
			p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) {
				c := declared("p")
				switch failure {
				case "drift":
					p.id = "PRIVATE-other"
				case "drift empty caps":
					p.id = "PRIVATE-other"
					c = domain.ProviderCapabilities{}
				case "wrong caps":
					c.ProviderID = "PRIVATE-other"
				case "empty caps":
					c = domain.ProviderCapabilities{}
				case "malformed caps":
					c.ProviderID, c.Tags, c.CostPerUnit = "PRIVATE-other", []string{" "}, math.NaN()
				}
				return c, cause
			}
			r := NewRegistry()
			err := r.Register(context.Background(), p)
			want := CodeDiscoveryFailed
			if strings.HasPrefix(failure, "drift") {
				want = CodeIdentityMismatch
			}
			var detail *Error
			var original *privateCause
			if !errors.As(err, &detail) || detail.Code() != want || !errors.Is(err, transient) || !errors.As(err, &original) || original != private || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatalf("causes/classification: %v", err)
			}
			if want == CodeDiscoveryFailed && (!errors.Is(err, ErrInvalidProvider) || errors.Is(err, ErrIdentityMismatch)) {
				t.Fatalf("failed payload must not imply identity mismatch: %v", err)
			}
			if want == CodeIdentityMismatch && !errors.Is(err, ErrIdentityMismatch) {
				t.Fatalf("actual ID drift lost permanent identity cause: %v", err)
			}
			if snapshots, err := r.List(context.Background()); err != nil || len(snapshots) != 0 {
				t.Fatalf("failed discovery published metadata: %+v %v", snapshots, err)
			}
			causes := detail.Unwrap()
			for i := range causes {
				causes[i] = nil
			}
			permanent := ErrInvalidProvider
			if want == CodeIdentityMismatch {
				permanent = ErrIdentityMismatch
			}
			original = nil
			if !errors.Is(err, permanent) || !errors.Is(err, transient) || !errors.Is(err, private) || !errors.As(err, &original) || original != private {
				t.Fatal("mutating Unwrap result lost permanent or original causes")
			}
		})
	}
}

func TestRefreshDiscoveryErrorIgnoresPayloadAndPreservesSnapshot(t *testing.T) {
	for _, payload := range []string{"zero", "wrong identity", "malformed"} {
		t.Run(payload, func(t *testing.T) {
			r := NewRegistry()
			p := catalog(declared("p"))
			mustRegister(t, r, p, true)
			if err := r.SetHealth(context.Background(), "p", domain.ProviderHealthDisabled); err != nil {
				t.Fatal(err)
			}
			_, before, err := r.Get(context.Background(), "p")
			if err != nil {
				t.Fatal(err)
			}
			cause := &privateCause{}
			p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) {
				c := domain.ProviderCapabilities{}
				if payload != "zero" {
					c = declared("PRIVATE-other")
				}
				if payload == "malformed" {
					c.Tags = []string{" "}
					c.EstimatedLatencyMs = ptr(int64(-1))
					c.Hardware.MinMemoryGB = math.NaN()
				}
				return c, cause
			}
			err = r.Refresh(context.Background(), "p")
			var detail *Error
			var original *privateCause
			if !errors.Is(err, ErrInvalidProvider) || errors.Is(err, ErrIdentityMismatch) || !errors.Is(err, cause) || !errors.As(err, &original) || original != cause || !errors.As(err, &detail) || detail.Code() != CodeDiscoveryFailed || err.Error() != "provider: discovery_failed" {
				t.Fatalf("failed payload misclassified: %v", err)
			}
			_, after, err := r.Get(context.Background(), "p")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("failed refresh changed metadata/health: %+v %v", after, err)
			}
		})
	}
}

func TestRegistryRefreshIsAtomicAndPreservesHealth(t *testing.T) {
	for _, failure := range []string{"metadata", "drift", "error", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			r := NewRegistry()
			c := declared("p")
			p := catalog(c)
			mustRegister(t, r, p, true)
			_, before, _ := r.Get(context.Background(), "p")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) {
				next := declared("p")
				next.Tags = []string{"new"}
				next.CostUnit = "USD/video"
				next.CostPerUnit = 7
				switch failure {
				case "metadata":
					next.Hardware.MinMemoryGB = math.NaN()
				case "drift":
					p.id = "other"
				case "error":
					return next, &privateCause{}
				case "cancel":
					cancel()
				}
				return next, nil
			}
			if err := r.Refresh(ctx, "p"); err == nil {
				t.Fatal("failed refresh succeeded")
			}
			_, after, err := r.Get(context.Background(), "p")
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("partial refresh: %+v %v", after, err)
			}
		})
	}
	// Two overlapping discoveries publish independently; the later valid
	// publication wins, without restoring health captured before discovery.
	r := NewRegistry()
	p := catalog(declared("p"))
	mustRegister(t, r, p, true)
	entered := make(chan int, 2)
	release := []chan struct{}{make(chan struct{}), make(chan struct{})}
	var calls atomic.Int64
	p.capsFn = func(ctx context.Context) (domain.ProviderCapabilities, error) {
		n := int(calls.Add(1)) - 1
		entered <- n
		<-release[n]
		c := declared("p")
		c.Tags = []string{fmt.Sprintf("generation-%d", n)}
		return c, nil
	}
	done := make(chan error, 2)
	go func() { done <- r.Refresh(context.Background(), "p") }()
	if n := <-entered; n != 0 {
		t.Fatal(n)
	}
	go func() { done <- r.Refresh(context.Background(), "p") }()
	if n := <-entered; n != 1 {
		t.Fatal(n)
	}
	if err := r.SetHealth(context.Background(), "p", domain.ProviderHealthDisabled); err != nil {
		t.Fatal(err)
	}
	_, old, _ := r.Get(context.Background(), "p")
	if !old.Capabilities.Supports("video") || old.Health != domain.ProviderHealthDisabled {
		t.Fatal("discovery changed stored state")
	}
	close(release[1])
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, middle, _ := r.Get(context.Background(), "p")
	if !middle.Capabilities.Supports("generation-1") || middle.Health != domain.ProviderHealthDisabled {
		t.Fatal(middle)
	}
	if err := r.SetHealth(context.Background(), "p", domain.ProviderHealthUnhealthy); err != nil {
		t.Fatal(err)
	}
	close(release[0])
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	_, last, _ := r.Get(context.Background(), "p")
	if !last.Capabilities.Supports("generation-0") || last.Health != domain.ProviderHealthUnhealthy {
		t.Fatal(last)
	}
}

func TestRegistryReentrantDiscoveryAndConcurrentOperations(t *testing.T) {
	r := NewRegistry()
	p := catalog(declared("p"))
	p.capsFn = func(ctx context.Context) (domain.ProviderCapabilities, error) {
		if _, err := r.List(ctx); err != nil {
			return domain.ProviderCapabilities{}, err
		}
		_, _, _ = r.Get(ctx, "p")
		_ = r.SetHealth(ctx, "p", domain.ProviderHealthHealthy)
		return declared("p"), nil
	}
	mustRegister(t, r, p, false)
	if err := r.Refresh(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	const n = 24
	start := make(chan struct{})
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); <-start; errs <- r.Register(context.Background(), catalog(declared("same"))) }()
	}
	close(start)
	wg.Wait()
	close(errs)
	success := 0
	for err := range errs {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrDuplicateProvider) {
			t.Error(err)
		}
	}
	if success != 1 {
		t.Fatalf("accepted %d duplicates", success)
	}
	var sequence atomic.Int64
	p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) {
		n := sequence.Add(1)
		c := declared("p")
		c.CostPerUnit = float64(n)
		c.CostUnit = "USD/video"
		c.EstimatedLatencyMs = ptr(n)
		c.Hardware.MinMemoryGB = float64(n)
		return c, nil
	}
	errs = make(chan error, n)
	start = make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for j := 0; j < 20; j++ {
				if i%3 == 0 {
					if err := r.Refresh(context.Background(), "p"); err != nil {
						errs <- err
						return
					}
				}
				if i%3 == 1 {
					if err := r.SetHealth(context.Background(), "p", domain.ProviderHealthUnknown); err != nil {
						errs <- err
						return
					}
				}
				list, err := r.List(context.Background())
				if err != nil {
					errs <- err
					return
				}
				for _, s := range list {
					c := s.Capabilities
					if c.ProviderID == "p" && c.EstimatedLatencyMs != nil && (c.CostPerUnit != float64(*c.EstimatedLatencyMs) || c.CostPerUnit != c.Hardware.MinMemoryGB) {
						errs <- errors.New("torn snapshot")
						return
					}
				}
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestFailedRefreshRetainsConcurrentHealthUpdate(t *testing.T) {
	r := NewRegistry()
	p := catalog(declared("p"))
	mustRegister(t, r, p, true)
	entered, release := make(chan struct{}), make(chan struct{})
	cause := &privateCause{}
	p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) {
		close(entered)
		<-release
		c := declared("p")
		c.Tags = []string{"partial-new"}
		c.CostUnit = "USD/video"
		c.CostPerUnit = 100
		return c, cause
	}
	done := make(chan error, 1)
	go func() { done <- r.Refresh(context.Background(), "p") }()
	<-entered
	if err := r.SetHealth(context.Background(), "p", domain.ProviderHealthDisabled); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; !errors.Is(err, cause) {
		t.Fatal(err)
	}
	_, got, err := r.Get(context.Background(), "p")
	want := Snapshot{Capabilities: declared("p"), Health: domain.ProviderHealthDisabled}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("failed refresh lost health or published partial metadata: %+v %v", got, err)
	}
}

func TestRegistryCancellationBoundaries(t *testing.T) {
	r := NewRegistry()
	p := catalog(declared("p"))
	mustRegister(t, r, p, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, err := range []error{r.Register(ctx, p), r.Refresh(ctx, "p"), r.SetHealth(ctx, "p", domain.ProviderHealthHealthy)} {
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if _, _, err := r.Get(ctx, "p"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := r.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, boundary := range []string{"initial identity", "before discovery identity", "during discovery", "after discovery identity", "late success"} {
		t.Run(boundary, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if boundary == "late success" {
				var timeoutCancel context.CancelFunc
				ctx, timeoutCancel = context.WithTimeout(ctx, 20*time.Millisecond)
				defer timeoutCancel()
			}
			p := catalog(declared("new"))
			identityCalls, capCalls := 0, 0
			p.idFn = func() string {
				identityCalls++
				if (boundary == "initial identity" && identityCalls == 1) || (boundary == "before discovery identity" && identityCalls == 2) || (boundary == "after discovery identity" && identityCalls == 3) {
					cancel()
				}
				return "new"
			}
			cause := &privateCause{}
			p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) {
				capCalls++
				if boundary == "late success" {
					<-ctx.Done()
					return declared("new"), nil
				}
				if boundary == "during discovery" {
					cancel()
					return declared("new"), cause
				}
				return declared("new"), nil
			}
			err := r.Register(ctx, p)
			want := error(context.Canceled)
			if boundary == "late success" {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			if boundary == "during discovery" && !errors.Is(err, cause) {
				t.Fatal("cause lost")
			}
			if (boundary == "initial identity" || boundary == "before discovery identity") && capCalls != 0 {
				t.Fatal("called discovery after identity canceled")
			}
			if _, _, err := r.Get(context.Background(), "new"); !errors.Is(err, ErrProviderNotFound) {
				t.Fatal("late metadata published")
			}
		})
	}
}
