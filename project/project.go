// Package project implements project persistence and the structural
// rules for the fundamental video data model (PHASES.md
// "PHASE 01 — PROJECT STATE / TIMELINE").
//
// It depends on domain for data contracts and idgen for stable ID
// generation. It does not depend on any renderer, AI provider, or TUI
// package: "A project can be created, loaded, modified, saved, and
// validated without any renderer or AI model" (PHASES.md Phase 01
// Required Result).
package project

import (
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/idgen"
)

// NewID generates a new stable domain.ID for use within this package's
// own constructors (New, and future project-package helpers). It is a
// thin wrapper around idgen.New; other packages that need to mint an ID
// but have no other reason to depend on project should call idgen.New
// directly rather than importing project just for this function.
func NewID() domain.ID {
	return domain.ID(idgen.New())
}

// New creates a new, empty Project with a freshly generated ID and
// CreatedAt/UpdatedAt set to now.
func New(name string, now time.Time) domain.Project {
	return domain.Project{
		ID:        NewID(),
		Name:      name,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
