package registry

import (
	"fmt"
	"math"
	"sort"
	"sync"

	"github.com/manish96170/videofactory/domain"
)

// FramingRegistry looks up domain.Framing entries by ID. The zero
// value is not usable; construct one with NewFramingRegistry.
type FramingRegistry struct {
	mu      sync.RWMutex
	entries map[domain.ID]domain.Framing
}

// NewFramingRegistry returns a FramingRegistry seeded with the five
// built-in framings PHASES.md Phase 04 names: fullscreen, split-screen,
// picture-in-picture, developer-circle, screen-focused.
func NewFramingRegistry() *FramingRegistry {
	r := &FramingRegistry{entries: make(map[domain.ID]domain.Framing)}
	for _, f := range builtinFramings() {
		if err := r.Register(f); err != nil {
			panic(fmt.Sprintf("registry: invalid built-in framing %q: %v", f.ID, err))
		}
	}
	return r
}

func builtinFramings() []domain.Framing {
	full := domain.Bounds{X: 0, Y: 0, Width: 1, Height: 1}

	return []domain.Framing{
		{
			ID:          "fullscreen",
			Name:        "fullscreen",
			Description: "A single slot filling the entire frame.",
			Slots: []domain.FramingSlot{
				{Name: "main", Bounds: full},
			},
		},
		{
			ID:          "split-screen",
			Name:        "split-screen",
			Description: "Two equal slots side by side.",
			Slots: []domain.FramingSlot{
				{Name: "left", Bounds: domain.Bounds{X: 0, Y: 0, Width: 0.5, Height: 1}},
				{Name: "right", Bounds: domain.Bounds{X: 0.5, Y: 0, Width: 0.5, Height: 1}},
			},
		},
		{
			ID:          "picture-in-picture",
			Name:        "picture-in-picture",
			Description: "A fullscreen main slot with a small overlay slot in the bottom-right corner.",
			Slots: []domain.FramingSlot{
				{Name: "main", Bounds: full},
				{Name: "overlay", Bounds: domain.Bounds{X: 0.70, Y: 0.65, Width: 0.28, Height: 0.28}},
			},
		},
		{
			ID:          "developer-circle",
			Name:        "developer-circle",
			Description: "A fullscreen main slot (code/terminal/UI) with a small circular-framed overlay slot for a developer avatar/webcam. This is one reusable framing, not a hard-coded architecture (PHASES.md section 7) — the 'circular' presentation is a rendering detail for whatever draws the overlay slot's content, not something this registry enforces.",
			Slots: []domain.FramingSlot{
				{Name: "main", Bounds: full},
				{Name: "overlay", Bounds: domain.Bounds{X: 0.78, Y: 0.68, Width: 0.20, Height: 0.20}},
			},
		},
		{
			ID:          "screen-focused",
			Name:        "screen-focused",
			Description: "A single slot filling the frame, like fullscreen, but intended for content where the screen itself (code/terminal/UI) is the entire subject with no avatar/overlay slot at all.",
			Slots: []domain.FramingSlot{
				{Name: "main", Bounds: full},
			},
		},
	}
}

// Register adds framing to the registry. It returns an error if
// framing.ID is empty, already registered, has no slots, has a
// duplicate slot name, or any slot's bounds are malformed.
func (r *FramingRegistry) Register(framing domain.Framing) error {
	if framing.ID.IsZero() {
		return fmt.Errorf("registry: framing ID must not be empty")
	}
	if len(framing.Slots) == 0 {
		return fmt.Errorf("registry: framing %q must have at least one slot", framing.ID)
	}
	seenSlot := make(map[string]struct{}, len(framing.Slots))
	for _, slot := range framing.Slots {
		if slot.Name == "" {
			return fmt.Errorf("registry: framing %q has a slot with an empty name", framing.ID)
		}
		if _, dup := seenSlot[slot.Name]; dup {
			return fmt.Errorf("registry: framing %q has duplicate slot name %q", framing.ID, slot.Name)
		}
		seenSlot[slot.Name] = struct{}{}
		if err := validateSlotBounds(slot.Bounds); err != nil {
			return fmt.Errorf("registry: framing %q slot %q: %w", framing.ID, slot.Name, err)
		}
	}

	stored := cloneFraming(framing)
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.entries[framing.ID]; exists {
		return fmt.Errorf("registry: framing %q is already registered", framing.ID)
	}
	r.entries[framing.ID] = stored
	return nil
}

// Get returns the framing registered under id, and whether it was
// found. The returned value is a defensive copy: mutating its Slots
// slice cannot affect the registry's stored entry.
func (r *FramingRegistry) Get(id domain.ID) (domain.Framing, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	f, ok := r.entries[id]
	if !ok {
		return domain.Framing{}, false
	}
	return cloneFraming(f), true
}

// List returns every registered framing, ordered by ID for
// determinism. Each returned value is a defensive copy (see Get).
func (r *FramingRegistry) List() []domain.Framing {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]domain.Framing, 0, len(r.entries))
	for _, f := range r.entries {
		out = append(out, cloneFraming(f))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// cloneFraming returns a copy of f whose Slots slice does not alias
// f's, so the registry's internal state and anything a caller holds
// can never affect each other through this shared slice (found during
// Phase 04 review — see DECISIONS.md ADR-017's amendment).
// domain.FramingSlot has no nested slices/pointers, so copying the
// top-level slice is sufficient.
func cloneFraming(f domain.Framing) domain.Framing {
	f.Slots = append([]domain.FramingSlot(nil), f.Slots...)
	return f
}

// validateSlotBounds checks that b describes a well-formed, finite
// region within the normalized [0,1] frame (PHASES.md section 4.6.1 /
// DECISIONS.md ADR-007) — the same convention, tolerance, and
// NaN/Inf-rejection the render package's validateBounds applies, kept
// as a small local copy here rather than a shared dependency between
// the two packages.
func validateSlotBounds(b domain.Bounds) error {
	for name, v := range map[string]float64{"x": b.X, "y": b.Y, "width": b.Width, "height": b.Height} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return fmt.Errorf("bounds.%s must be finite, got %v", name, v)
		}
	}
	if b.Width <= 0 || b.Height <= 0 {
		return fmt.Errorf("bounds width/height must be positive, got %v/%v", b.Width, b.Height)
	}
	if b.X < 0 || b.Y < 0 {
		return fmt.Errorf("bounds x/y must not be negative, got %v/%v", b.X, b.Y)
	}
	if b.X+b.Width > 1.0001 || b.Y+b.Height > 1.0001 {
		return fmt.Errorf("bounds must fit within the normalized [0,1] frame, got x=%v y=%v width=%v height=%v", b.X, b.Y, b.Width, b.Height)
	}
	return nil
}
