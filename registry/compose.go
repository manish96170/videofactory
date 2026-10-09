// Composition: applying a Framing's layout to a set of named content
// Primitives, generically — the concrete proof that "framing is not
// embedded into business logic" (PHASES.md Phase 04 TODO). AssignSlots
// contains no reference to any specific FramingID; it only reads
// framing.Slots. Adding a new Framing to a FramingRegistry (built-in or
// registered at runtime) never requires changing this function —
// compose_test.go demonstrates this directly with a framing that
// isn't one of the five built-ins.
package registry

import (
	"fmt"

	"github.com/manish96170/videofactory/domain"
)

// AssignSlots maps content (keyed by slot name) onto framing's slots,
// returning one domain.Primitive per matched slot with
// Properties["bounds"] set to that slot's domain.Bounds (the same
// Properties key the render package's Image/Text primitives already
// read — PHASES.md Phase 03 / DECISIONS.md ADR-007).
//
// Every key in content must name a slot that exists in framing; a
// framing's slots may be left unfilled (e.g. an optional "overlay"
// slot with no corresponding content). Returns an error naming the
// unknown slot if content references one framing.Slots doesn't have —
// this is deliberately strict, so a typo'd slot name fails loudly
// instead of being silently dropped.
func AssignSlots(framing domain.Framing, content map[string]domain.Primitive) ([]domain.Primitive, error) {
	slotsByName := make(map[string]domain.Bounds, len(framing.Slots))
	for _, slot := range framing.Slots {
		slotsByName[slot.Name] = slot.Bounds
	}

	for name := range content {
		if _, ok := slotsByName[name]; !ok {
			return nil, fmt.Errorf("registry: framing %q has no slot named %q", framing.ID, name)
		}
	}

	// Iterate framing.Slots (not content) so the result's order is
	// determined by the framing's own slot order, not Go's randomized
	// map iteration order.
	var result []domain.Primitive
	for _, slot := range framing.Slots {
		prim, ok := content[slot.Name]
		if !ok {
			continue
		}
		assigned := prim
		props := make(map[string]any, len(prim.Properties)+1)
		for k, v := range prim.Properties {
			props[k] = v
		}
		props["bounds"] = slot.Bounds
		assigned.Properties = props
		result = append(result, assigned)
	}
	return result, nil
}

// SlotNames returns framing's slot names, in slot order.
func SlotNames(framing domain.Framing) []string {
	names := make([]string, 0, len(framing.Slots))
	for _, slot := range framing.Slots {
		names = append(names, slot.Name)
	}
	return names
}
