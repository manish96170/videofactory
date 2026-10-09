package render

import (
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/manish96170/videofactory/domain"
)

// stringProp extracts a required string property.
func stringProp(props map[string]any, key string) (string, error) {
	v, ok := props[key]
	if !ok {
		return "", fmt.Errorf("render: missing required property %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("render: property %q must be a string, got %T", key, v)
	}
	return s, nil
}

// stringPropOr extracts an optional string property, returning def if
// it's absent, or an error if it's present but not a string.
func stringPropOr(props map[string]any, key, def string) (string, error) {
	v, ok := props[key]
	if !ok {
		return def, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("render: property %q must be a string, got %T", key, v)
	}
	return s, nil
}

// floatPropOr extracts an optional numeric property, returning def if
// it's absent, or an error if it's present but not a number. Both
// float64 (the common case after a JSON round trip) and int (the
// common case for a value constructed directly in Go) are accepted;
// see DECISIONS.md ADR-008 on map[string]any extension-field typing.
func floatPropOr(props map[string]any, key string, def float64) (float64, error) {
	v, ok := props[key]
	if !ok {
		return def, nil
	}
	switch n := v.(type) {
	case float64:
		return n, nil
	case int:
		return float64(n), nil
	default:
		return 0, fmt.Errorf("render: property %q must be a number, got %T", key, v)
	}
}

// hexColorPattern matches a 6-digit hex color, optionally '#'-prefixed.
var hexColorPattern = regexp.MustCompile(`^#?[0-9A-Fa-f]{6}$`)

// parseHexColor validates s and returns it in ffmpeg's "0xRRGGBB" color
// syntax.
func parseHexColor(s string) (string, error) {
	if !hexColorPattern.MatchString(s) {
		return "", fmt.Errorf("render: invalid color %q, want 6 hex digits optionally prefixed with '#'", s)
	}
	return "0x" + strings.TrimPrefix(s, "#"), nil
}

// boundsProp extracts an optional Bounds property. It accepts a
// domain.Bounds/*domain.Bounds value (the common case when a Primitive
// is constructed directly in Go) or a map[string]any with numeric
// x/y/width/height keys (the common case after a JSON round trip,
// where Properties itself came from Unmarshal). Returns (nil, nil) if
// the key is absent.
func boundsProp(props map[string]any, key string) (*domain.Bounds, error) {
	v, ok := props[key]
	if !ok {
		return nil, nil
	}
	switch b := v.(type) {
	case domain.Bounds:
		bc := b
		return &bc, nil
	case *domain.Bounds:
		return b, nil
	case map[string]any:
		get := func(k string) (float64, error) {
			raw, ok := b[k]
			if !ok {
				return 0, fmt.Errorf("render: bounds missing %q", k)
			}
			switch n := raw.(type) {
			case float64:
				return n, nil
			case int:
				return float64(n), nil
			default:
				return 0, fmt.Errorf("render: bounds.%s must be a number, got %T", k, raw)
			}
		}
		x, err := get("x")
		if err != nil {
			return nil, err
		}
		y, err := get("y")
		if err != nil {
			return nil, err
		}
		w, err := get("width")
		if err != nil {
			return nil, err
		}
		h, err := get("height")
		if err != nil {
			return nil, err
		}
		return &domain.Bounds{X: x, Y: y, Width: w, Height: h}, nil
	default:
		return nil, fmt.Errorf("render: property %q must be domain.Bounds or a map, got %T", key, v)
	}
}

// requireFinite returns an error if v is NaN or infinite.
func requireFinite(name string, v float64) error {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return fmt.Errorf("render: %s must be finite, got %v", name, v)
	}
	return nil
}

// validateBounds checks that b describes a well-formed region within
// the normalized [0,1] frame (PHASES.md section 4.6.1 / ADR-007).
func validateBounds(b domain.Bounds) error {
	for name, v := range map[string]float64{"x": b.X, "y": b.Y, "width": b.Width, "height": b.Height} {
		if err := requireFinite("bounds."+name, v); err != nil {
			return err
		}
	}
	if b.Width <= 0 || b.Height <= 0 {
		return fmt.Errorf("render: bounds width/height must be positive, got %v/%v", b.Width, b.Height)
	}
	if b.X < 0 || b.Y < 0 {
		return fmt.Errorf("render: bounds x/y must not be negative, got %v/%v", b.X, b.Y)
	}
	if b.X+b.Width > 1.0001 || b.Y+b.Height > 1.0001 { // tiny epsilon for float rounding
		return fmt.Errorf("render: bounds must fit within the normalized [0,1] frame, got x=%v y=%v width=%v height=%v", b.X, b.Y, b.Width, b.Height)
	}
	return nil
}

// boundsToPixelBox converts normalized bounds to a pixel box in
// settings' frame, rounding width/height up to the nearest even number
// (required by yuv420p 4:2:0 chroma subsampling). A nil bounds means
// "fullscreen".
func boundsToPixelBox(b *domain.Bounds, settings Settings) (x, y, w, h int) {
	if b == nil {
		return 0, 0, settings.Width, settings.Height
	}
	x = roundToInt(b.X * float64(settings.Width))
	y = roundToInt(b.Y * float64(settings.Height))
	w = roundEven(roundToInt(b.Width * float64(settings.Width)))
	h = roundEven(roundToInt(b.Height * float64(settings.Height)))
	return
}

func roundToInt(f float64) int {
	if f < 0 {
		return int(f - 0.5)
	}
	return int(f + 0.5)
}

func roundEven(n int) int {
	if n < 2 {
		return 2
	}
	if n%2 != 0 {
		n++
	}
	return n
}
