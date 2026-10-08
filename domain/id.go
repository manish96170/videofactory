package domain

// ID is a stable, opaque identifier for a domain entity.
//
// Phase 00 only defines the type. The strategy for generating stable IDs
// (ULID/UUID, content hashing, etc.) is established in Phase 01 and must
// not be assumed by code depending on this package.
type ID string

// IsZero reports whether the ID is unset.
func (id ID) IsZero() bool {
	return id == ""
}
