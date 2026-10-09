package registry

import (
	"fmt"
	"sync"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

// TestRegistriesAreSafeForConcurrentUse exercises concurrent
// registration and lookup against all three registries under -race,
// mirroring the concurrency testing established for event.Bus
// (Phase 02).
func TestRegistriesAreSafeForConcurrentUse(t *testing.T) {
	flows := NewFlowRegistry()
	framings := NewFramingRegistry()
	primitives := NewPrimitiveRegistry()

	const n = 50
	var wg sync.WaitGroup

	for i := 0; i < n; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()

			_ = flows.Register(domain.Flow{ID: domain.ID(fmt.Sprintf("flow-%d", i)), Name: "x"})
			_, _ = flows.Get("developer-explainer")
			_ = flows.List()

			_ = framings.Register(domain.Framing{
				ID:    domain.ID(fmt.Sprintf("framing-%d", i)),
				Name:  "x",
				Slots: []domain.FramingSlot{{Name: "main", Bounds: domain.Bounds{Width: 1, Height: 1}}},
			})
			_, _ = framings.Get("fullscreen")
			_ = framings.List()

			_ = primitives.Register(domain.PrimitiveCapabilities{Type: domain.PrimitiveType(fmt.Sprintf("Custom%d", i))})
			_, _ = primitives.Get(domain.PrimitiveTypeText)
			_ = primitives.List()
		}()
	}
	wg.Wait()

	if len(flows.List()) != n+2 {
		t.Fatalf("expected %d flows, got %d", n+2, len(flows.List()))
	}
	if len(framings.List()) != n+5 {
		t.Fatalf("expected %d framings, got %d", n+5, len(framings.List()))
	}
	if len(primitives.List()) != n+15 {
		t.Fatalf("expected %d primitives, got %d", n+15, len(primitives.List()))
	}
}
