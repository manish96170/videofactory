package provider

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func mustRouter(t *testing.T, r *Registry) *Router {
	t.Helper()
	router, err := NewRouter(r)
	if err != nil {
		t.Fatal(err)
	}
	return router
}
func videoRequest() RouteRequest { return RouteRequest{RequiredTags: []string{"video"}} }

func TestRouteRankingPreferencesAndFallbackInsertionIndependent(t *testing.T) {
	var caps []domain.ProviderCapabilities
	for _, id := range []string{"z-local", "a-local", "z-deterministic", "a-deterministic", "remote", "remote-deterministic", "unhealthy", "wrong-tag"} {
		c := declared(id)
		if strings.Contains(id, "deterministic") {
			c.Tags = append(c.Tags, "deterministic")
		}
		if strings.HasPrefix(id, "remote") {
			c.Local = false
			c.Hardware = nil
		}
		if id == "wrong-tag" {
			c.Tags = []string{"audio"}
		}
		caps = append(caps, c)
	}
	for _, reverse := range []bool{false, true} {
		r := NewRegistry()
		for i := range caps {
			index := i
			if reverse {
				index = len(caps) - i - 1
			}
			c := caps[index]
			mustRegister(t, r, catalog(c), c.ProviderID != "unhealthy")
		}
		router := mustRouter(t, r)
		for _, tc := range []struct {
			name      string
			preferred []string
			want      []string
		}{
			{"default tiers", nil, []string{"a-deterministic", "remote-deterministic", "z-deterministic", "a-local", "z-local", "remote"}},
			{"preferences first", []string{"remote", "z-local"}, []string{"remote", "z-local", "a-deterministic", "remote-deterministic", "z-deterministic", "a-local"}},
			{"ineligible and unknown ignored", []string{"unknown", "wrong-tag", "unhealthy", "z-deterministic"}, []string{"z-deterministic", "a-deterministic", "remote-deterministic", "a-local", "z-local", "remote"}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				req := videoRequest()
				req.Locality = AnyLocality
				req.PreferredIDs = tc.preferred
				got, err := router.Route(context.Background(), req)
				wantRejections := []Rejection{{ProviderID: "unhealthy", Codes: []RejectionCode{RejectHealth}}, {ProviderID: "wrong-tag", Codes: []RejectionCode{RejectTags}}}
				if err != nil || !reflect.DeepEqual(got.EligibleIDs, tc.want) || !reflect.DeepEqual(got.FallbackIDs, tc.want[1:]) || !reflect.DeepEqual(got.Rejections, wantRejections) {
					t.Fatalf("reverse=%v: %+v %v", reverse, got, err)
				}
				got.EligibleIDs[1] = "mutated"
				if got.FallbackIDs[0] != tc.want[1] {
					t.Fatal("fallback aliases eligible IDs")
				}
				got.Rejections[0].Codes[0] = "mutated"
				again, _ := router.Route(context.Background(), req)
				if !reflect.DeepEqual(again.EligibleIDs, tc.want) || !reflect.DeepEqual(again.Rejections, wantRejections) {
					t.Fatal("returned result aliases state")
				}
			})
		}
	}
}

func TestRouteConstraintSemantics(t *testing.T) {
	for _, tc := range []struct {
		name       string
		changeCaps func(*domain.ProviderCapabilities)
		changeReq  func(*RouteRequest)
		want       []RejectionCode
	}{
		{name: "unconstrained unknown cost latency", want: nil},
		{name: "ALL tags", changeCaps: func(c *domain.ProviderCapabilities) { c.Tags = []string{"video", "image"} }, changeReq: func(r *RouteRequest) { r.RequiredTags = []string{"video", "image"} }},
		{name: "missing one tag", changeReq: func(r *RouteRequest) { r.RequiredTags = []string{"video", "image"} }, want: []RejectionCode{RejectTags}},
		{name: "case exact", changeReq: func(r *RouteRequest) { r.RequiredTags = []string{"Video"} }, want: []RejectionCode{RejectTags}},
		{name: "no trimming", changeReq: func(r *RouteRequest) { r.RequiredTags = []string{" video"} }, want: []RejectionCode{RejectTags}},
		{name: "remote default denied", changeCaps: func(c *domain.ProviderCapabilities) { c.Local = false }, want: []RejectionCode{RejectLocality}},
		{name: "remote explicit local denied", changeReq: func(r *RouteRequest) { r.Locality = RemoteOnly }, want: []RejectionCode{RejectLocality}},
		{name: "remote only", changeCaps: func(c *domain.ProviderCapabilities) { c.Local = false }, changeReq: func(r *RouteRequest) { r.Locality = RemoteOnly }},
		{name: "any remote", changeCaps: func(c *domain.ProviderCapabilities) { c.Local = false }, changeReq: func(r *RouteRequest) { r.Locality = AnyLocality }},
		{name: "unlabelled zero unknown", changeReq: func(r *RouteRequest) { r.MaxCost = &CostLimit{Amount: 0, Unit: "USD/video"} }, want: []RejectionCode{RejectCostUnknown}},
		{name: "explicit free", changeCaps: func(c *domain.ProviderCapabilities) { c.CostUnit = "USD/video" }, changeReq: func(r *RouteRequest) { r.MaxCost = &CostLimit{Amount: 0, Unit: "USD/video"} }},
		{name: "inclusive cost", changeCaps: func(c *domain.ProviderCapabilities) { c.CostUnit = "USD/video"; c.CostPerUnit = 2 }, changeReq: func(r *RouteRequest) { r.MaxCost = &CostLimit{Amount: 2, Unit: "USD/video"} }},
		{name: "cost exceeded", changeCaps: func(c *domain.ProviderCapabilities) { c.CostUnit = "USD/video"; c.CostPerUnit = 2 }, changeReq: func(r *RouteRequest) { r.MaxCost = &CostLimit{Amount: 1, Unit: "USD/video"} }, want: []RejectionCode{RejectCostLimit}},
		{name: "currency mismatch even free", changeCaps: func(c *domain.ProviderCapabilities) { c.CostUnit = "EUR/video" }, changeReq: func(r *RouteRequest) { r.MaxCost = &CostLimit{Amount: 5, Unit: "USD/video"} }, want: []RejectionCode{RejectCostUnit}},
		{name: "unit case exact", changeCaps: func(c *domain.ProviderCapabilities) { c.CostUnit = "USD/Video" }, changeReq: func(r *RouteRequest) { r.MaxCost = &CostLimit{Unit: "USD/video"} }, want: []RejectionCode{RejectCostUnit}},
		{name: "unit whitespace exact", changeCaps: func(c *domain.ProviderCapabilities) { c.CostUnit = "USD/video " }, changeReq: func(r *RouteRequest) { r.MaxCost = &CostLimit{Unit: "USD/video"} }, want: []RejectionCode{RejectCostUnit}},
		{name: "latency unknown", changeReq: func(r *RouteRequest) { r.MaxLatencyMs = ptr(int64(100)) }, want: []RejectionCode{RejectLatencyUnknown}},
		{name: "latency inclusive", changeCaps: func(c *domain.ProviderCapabilities) { c.EstimatedLatencyMs = ptr(int64(100)) }, changeReq: func(r *RouteRequest) { r.MaxLatencyMs = ptr(int64(100)) }},
		{name: "latency known zero", changeCaps: func(c *domain.ProviderCapabilities) { c.EstimatedLatencyMs = ptr(int64(0)) }, changeReq: func(r *RouteRequest) { r.MaxLatencyMs = ptr(int64(0)) }},
		{name: "latency exceeded", changeCaps: func(c *domain.ProviderCapabilities) { c.EstimatedLatencyMs = ptr(int64(101)) }, changeReq: func(r *RouteRequest) { r.MaxLatencyMs = ptr(int64(100)) }, want: []RejectionCode{RejectLatencyLimit}},
		{name: "hardware unknown", changeCaps: func(c *domain.ProviderCapabilities) { c.Hardware = nil }, want: []RejectionCode{RejectHardwareUnknown}},
		{name: "explicit hardware empty unknown caller allowed"},
		{name: "positive memory unknown caller", changeCaps: func(c *domain.ProviderCapabilities) { c.Hardware.MinMemoryGB = 4 }, want: []RejectionCode{RejectMemoryUnknown}},
		{name: "memory inclusive", changeCaps: func(c *domain.ProviderCapabilities) { c.Hardware.MinMemoryGB = 4 }, changeReq: func(r *RouteRequest) { r.Hardware.AvailableMemoryGB = ptr(float64(4)) }},
		{name: "memory insufficient", changeCaps: func(c *domain.ProviderCapabilities) { c.Hardware.MinMemoryGB = 4 }, changeReq: func(r *RouteRequest) { r.Hardware.AvailableMemoryGB = ptr(3.99) }, want: []RejectionCode{RejectMemoryLimit}},
		{name: "known zero memory", changeCaps: func(c *domain.ProviderCapabilities) { c.Hardware.MinMemoryGB = 4 }, changeReq: func(r *RouteRequest) { r.Hardware.AvailableMemoryGB = ptr(float64(0)) }, want: []RejectionCode{RejectMemoryLimit}},
		{name: "GPU unknown", changeCaps: func(c *domain.ProviderCapabilities) { c.Hardware.RequiresGPU = true }, want: []RejectionCode{RejectGPUUnknown}},
		{name: "GPU false", changeCaps: func(c *domain.ProviderCapabilities) { c.Hardware.RequiresGPU = true }, changeReq: func(r *RouteRequest) { r.Hardware.HasGPU = ptr(false) }, want: []RejectionCode{RejectGPUUnavailable}},
		{name: "GPU true", changeCaps: func(c *domain.ProviderCapabilities) { c.Hardware.RequiresGPU = true }, changeReq: func(r *RouteRequest) { r.Hardware.HasGPU = ptr(true) }},
		{name: "remote ignores enormous HW", changeCaps: func(c *domain.ProviderCapabilities) {
			c.Local = false
			c.Hardware = &domain.HardwareRequirements{MinMemoryGB: 10000, RequiresGPU: true}
		}, changeReq: func(r *RouteRequest) {
			r.Locality = AnyLocality
			r.Hardware.AvailableMemoryGB = ptr(float64(0))
			r.Hardware.HasGPU = ptr(false)
		}},
		{name: "remote unknown HW", changeCaps: func(c *domain.ProviderCapabilities) { c.Local = false; c.Hardware = nil }, changeReq: func(r *RouteRequest) { r.Locality = AnyLocality }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := declared("p")
			req := videoRequest()
			if tc.changeCaps != nil {
				tc.changeCaps(&c)
			}
			if tc.changeReq != nil {
				tc.changeReq(&req)
			}
			r := NewRegistry()
			mustRegister(t, r, catalog(c), true)
			got, err := mustRouter(t, r).Route(context.Background(), req)
			if len(tc.want) == 0 {
				if err != nil || !reflect.DeepEqual(got.EligibleIDs, []string{"p"}) || len(got.Rejections) != 0 {
					t.Fatalf("%+v %v", got, err)
				}
			} else if !errors.Is(err, ErrNoEligibleProviders) || len(got.EligibleIDs) != 0 || !reflect.DeepEqual(got.Rejections, []Rejection{{ProviderID: "p", Codes: tc.want}}) {
				t.Fatalf("%+v %v", got, err)
			}
		})
	}
}

func TestHealthTransitionsAndRoutingCallsNoAdapterMethods(t *testing.T) {
	r := NewRegistry()
	c := declared("p")
	var ids, discoveries int
	p := catalog(c)
	p.idFn = func() string { ids++; return "p" }
	p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) { discoveries++; return c, nil }
	mustRegister(t, r, p, false)
	router := mustRouter(t, r)
	for _, health := range []domain.ProviderHealth{domain.ProviderHealthUnknown, domain.ProviderHealthHealthy, domain.ProviderHealthUnhealthy, domain.ProviderHealthDisabled, domain.ProviderHealthHealthy} {
		if err := r.SetHealth(context.Background(), "p", health); err != nil {
			t.Fatal(err)
		}
		beforeIDs, beforeDiscoveries := ids, discoveries
		_, _ = r.List(context.Background())
		_, _, _ = r.Get(context.Background(), "p")
		got, err := router.Route(context.Background(), videoRequest())
		if ids != beforeIDs || discoveries != beforeDiscoveries {
			t.Fatal("routing/read/health called adapter")
		}
		if health == domain.ProviderHealthHealthy {
			if err != nil || len(got.EligibleIDs) != 1 {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrNoEligibleProviders) || !reflect.DeepEqual(got.Rejections[0].Codes, []RejectionCode{RejectHealth}) {
			t.Fatalf("%+v %v", got, err)
		}
	}
	// Every failed gate is reported in safe fixed order, never raw metadata.
	c = declared("private-metadata")
	c.Tags = []string{"PRIVATE-tag"}
	c.Local = false
	c.CostUnit = "PRIVATE-unit"
	mustRegister(t, r, catalog(c), false)
	req := videoRequest()
	req.MaxCost = &CostLimit{Unit: "USD/video"}
	req.MaxLatencyMs = ptr(int64(0))
	got, _ := router.Route(context.Background(), req)
	want := []Rejection{{ProviderID: "p", Codes: []RejectionCode{RejectCostUnknown, RejectLatencyUnknown}}, {ProviderID: "private-metadata", Codes: []RejectionCode{RejectHealth, RejectTags, RejectLocality, RejectCostUnit, RejectLatencyUnknown}}}
	if !reflect.DeepEqual(got.Rejections, want) {
		t.Fatal(got)
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "PRIVATE") {
		t.Fatal("raw metadata leaked")
	}
}

func TestRouteRequestValidationCloneAndSerialization(t *testing.T) {
	for name, change := range map[string]func(*RouteRequest){
		"empty tags":            func(r *RouteRequest) { r.RequiredTags = nil },
		"blank tag":             func(r *RouteRequest) { r.RequiredTags = []string{" "} },
		"blank preference":      func(r *RouteRequest) { r.PreferredIDs = []string{" "} },
		"duplicate preference":  func(r *RouteRequest) { r.PreferredIDs = []string{"p", "p"} },
		"locality":              func(r *RouteRequest) { r.Locality = "PRIVATE-locality" },
		"negative cost":         func(r *RouteRequest) { r.MaxCost = &CostLimit{Amount: -1, Unit: "USD/video"} },
		"nan cost":              func(r *RouteRequest) { r.MaxCost = &CostLimit{Amount: math.NaN(), Unit: "USD/video"} },
		"inf cost":              func(r *RouteRequest) { r.MaxCost = &CostLimit{Amount: math.Inf(1), Unit: "USD/video"} },
		"zero cap without unit": func(r *RouteRequest) { r.MaxCost = &CostLimit{} },
		"blank unit":            func(r *RouteRequest) { r.MaxCost = &CostLimit{Unit: " "} },
		"negative latency":      func(r *RouteRequest) { r.MaxLatencyMs = ptr(int64(-1)) },
		"negative memory":       func(r *RouteRequest) { r.Hardware.AvailableMemoryGB = ptr(float64(-1)) },
		"nan memory":            func(r *RouteRequest) { r.Hardware.AvailableMemoryGB = ptr(math.NaN()) },
		"inf memory":            func(r *RouteRequest) { r.Hardware.AvailableMemoryGB = ptr(math.Inf(1)) },
	} {
		t.Run(name, func(t *testing.T) {
			req := videoRequest()
			change(&req)
			router := mustRouter(t, NewRegistry())
			if _, err := router.Route(context.Background(), req); !errors.Is(err, ErrInvalidRequest) || strings.Contains(err.Error(), "PRIVATE") {
				t.Fatal(err)
			}
			if _, err := router.Resolve(context.Background(), "p", req); !errors.Is(err, ErrInvalidRequest) {
				t.Fatal(err)
			}
		})
	}
	req := RouteRequest{RequiredTags: []string{"video"}, PreferredIDs: []string{"p"}, Locality: AnyLocality, MaxCost: &CostLimit{Amount: 2, Unit: "USD/video"}, MaxLatencyMs: ptr(int64(100)), Hardware: HardwareFacts{AvailableMemoryGB: ptr(float64(8)), HasGPU: ptr(true)}}
	want := req.Clone()
	copy := req.Clone()
	req.RequiredTags[0], req.PreferredIDs[0], req.MaxCost.Amount, *req.MaxLatencyMs, *req.Hardware.AvailableMemoryGB, *req.Hardware.HasGPU = "changed", "changed", 99, 99, 99, false
	if !reflect.DeepEqual(copy, want) {
		t.Fatal("request Clone alias")
	}
	data, err := json.Marshal(copy)
	var decoded RouteRequest
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(copy, decoded) {
		t.Fatalf("%s %+v %v", data, decoded, err)
	}
	if _, err := NewRouter(nil); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	var invalid Router
	if _, err := invalid.Route(context.Background(), videoRequest()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := invalid.Resolve(context.Background(), "p", videoRequest()); !errors.Is(err, ErrInvalidConfig) {
		t.Fatal(err)
	}
	if _, err := mustRouter(t, NewRegistry()).Route(context.Background(), videoRequest()); !errors.Is(err, ErrNoEligibleProviders) {
		t.Fatal(err)
	}
}

func TestExactResolveLatestEligibilityIdentityAndCancellation(t *testing.T) {
	r := NewRegistry()
	p := catalog(declared("p"))
	next := catalog(declared("next"))
	mustRegister(t, r, p, true)
	mustRegister(t, r, next, true)
	router := mustRouter(t, r)
	req := videoRequest()
	if got, err := router.Resolve(context.Background(), "p", req); err != nil || got != p {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := router.Resolve(context.Background(), "unknown", req); got != nil || !errors.Is(err, ErrProviderNotFound) {
		t.Fatalf("substituted missing ID: %v", err)
	}
	if err := r.SetHealth(context.Background(), "p", domain.ProviderHealthDisabled); err != nil {
		t.Fatal(err)
	}
	if got, err := router.Resolve(context.Background(), "p", req); got != nil || !errors.Is(err, ErrIneligible) {
		t.Fatalf("substituted ineligible ID: %v", err)
	}
	if err := r.SetHealth(context.Background(), "p", domain.ProviderHealthHealthy); err != nil {
		t.Fatal(err)
	}
	p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) {
		c := declared("p")
		c.Tags = []string{"audio"}
		return c, nil
	}
	if err := r.Refresh(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	if got, err := router.Resolve(context.Background(), "p", req); got != nil || !errors.Is(err, ErrIneligible) {
		t.Fatalf("stale metadata: %v", err)
	}
	p.capsFn = func(context.Context) (domain.ProviderCapabilities, error) { return declared("p"), nil }
	if err := r.Refresh(context.Background(), "p"); err != nil {
		t.Fatal(err)
	}
	p.id = "PRIVATE-drift"
	if got, err := router.Resolve(context.Background(), "p", req); got != nil || !errors.Is(err, ErrIdentityMismatch) || strings.Contains(err.Error(), "PRIVATE") {
		t.Fatal(err)
	}
	p.id = "p"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := router.Route(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := router.Resolve(ctx, "p", req); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	p.idFn = func() string { cancel(); return "p" }
	if got, err := router.Resolve(ctx, "p", req); got != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation after ID lost")
	}
}

// Err is checked when registry access begins, after the router's entry check
// and request copy. A synchronous hook pins ownership without racy caller
// mutation, goroutine scheduling, or sleeps.
type requestCopyContext struct {
	context.Context
	checks    int
	afterCopy func()
}

func (c *requestCopyContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.afterCopy()
	}
	return c.Context.Err()
}

func TestRouterCallsOwnRequestSlicesAndNestedPointers(t *testing.T) {
	r := NewRegistry()
	for _, id := range []string{"p", "q"} {
		c := declared(id)
		c.CostUnit, c.CostPerUnit = "USD/video", 2
		c.EstimatedLatencyMs = ptr(int64(100))
		c.Hardware.MinMemoryGB, c.Hardware.RequiresGPU = 4, true
		mustRegister(t, r, catalog(c), true)
	}
	router := mustRouter(t, r)
	for _, operation := range []string{"route", "resolve"} {
		req := RouteRequest{RequiredTags: []string{"video"}, PreferredIDs: []string{"q"}, MaxCost: &CostLimit{Amount: 2, Unit: "USD/video"}, MaxLatencyMs: ptr(int64(100)), Hardware: HardwareFacts{AvailableMemoryGB: ptr(float64(4)), HasGPU: ptr(true)}}
		ctx := &requestCopyContext{Context: context.Background(), afterCopy: func() {
			req.RequiredTags[0], req.PreferredIDs[0], req.MaxCost.Amount, req.MaxCost.Unit, *req.MaxLatencyMs, *req.Hardware.AvailableMemoryGB, *req.Hardware.HasGPU = "PRIVATE-change", "p", 0, "changed", 0, 0, false
		}}
		if operation == "route" {
			got, err := router.Route(ctx, req)
			if err != nil || !reflect.DeepEqual(got.EligibleIDs, []string{"q", "p"}) {
				t.Fatalf("request alias: %+v %v", got, err)
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var decoded RouteResult
			if err := json.Unmarshal(data, &decoded); err != nil || !reflect.DeepEqual(got, decoded) {
				t.Fatalf("route result JSON: %s %v", data, err)
			}
		} else if got, err := router.Resolve(ctx, "p", req); err != nil || got == nil {
			t.Fatalf("Resolve request alias: %v", err)
		}
	}
}
