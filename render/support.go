package render

import "github.com/manish96170/videofactory/domain"

// SupportsPrimitive reports implemented rendering, not conceptual registry
// capability. It does not validate properties or native tool availability.
func SupportsPrimitive(kind domain.PrimitiveType) bool {
	switch kind {
	case domain.PrimitiveTypeBackground, domain.PrimitiveTypeImage, domain.PrimitiveTypeText, domain.PrimitiveTypeTransition:
		return true
	default:
		return false
	}
}
