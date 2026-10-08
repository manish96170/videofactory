package domain

import "time"

// ShotCategory classifies the dominant content of a Shot.
//
// This is an open, string-based vocabulary rather than a closed enum: new
// categories can be introduced by future flows/providers without changing
// this package. The constants below are the "recommended conceptual shot
// categories" from PHASES.md section 6, not an exhaustive list.
type ShotCategory string

const (
	ShotCategoryUI          ShotCategory = "UI"
	ShotCategoryCode        ShotCategory = "CODE"
	ShotCategoryTerminal    ShotCategory = "TERMINAL"
	ShotCategoryChart       ShotCategory = "CHART"
	ShotCategoryAvatar      ShotCategory = "AVATAR"
	ShotCategoryTransition  ShotCategory = "TRANSITION"
	ShotCategoryEnvironment ShotCategory = "ENVIRONMENT"
	ShotCategoryHumanAction ShotCategory = "HUMAN_ACTION"
	ShotCategoryCinematic   ShotCategory = "CINEMATIC"
)

// PrimitiveType identifies the kind of a Primitive. Like ShotCategory, this
// is an open string vocabulary so new primitives can be registered by
// later phases/providers without modifying this package.
type PrimitiveType string

const (
	PrimitiveTypeCodeEditor      PrimitiveType = "CodeEditor"
	PrimitiveTypeTerminal        PrimitiveType = "Terminal"
	PrimitiveTypeBrowser         PrimitiveType = "Browser"
	PrimitiveTypeChart           PrimitiveType = "Chart"
	PrimitiveTypeText            PrimitiveType = "Text"
	PrimitiveTypeSubtitle        PrimitiveType = "Subtitle"
	PrimitiveTypeCursor          PrimitiveType = "Cursor"
	PrimitiveTypeImage           PrimitiveType = "Image"
	PrimitiveTypeTransition      PrimitiveType = "Transition"
	PrimitiveTypeDeveloperAvatar PrimitiveType = "DeveloperAvatar"
	PrimitiveTypeHumanAction     PrimitiveType = "HumanAction"
	PrimitiveTypeEnvironment     PrimitiveType = "Environment"
	PrimitiveTypeCinematicCamera PrimitiveType = "CinematicCamera"
	PrimitiveTypeAnimatedImage   PrimitiveType = "AnimatedImage"
)

// Primitive is a single deterministic or AI-capable building block
// composed into a Shot (PHASES.md section 6).
type Primitive struct {
	ID         ID             `json:"id"`
	Type       PrimitiveType  `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
}

// Shot is the smallest planned unit of video. Target granularity is
// roughly 2-3 seconds (PHASES.md section 4.4), though exact duration is
// not enforced by this package.
type Shot struct {
	ID            ID            `json:"id"`
	PlanID        ID            `json:"planId"`
	Index         int           `json:"index"`
	Category      ShotCategory  `json:"category"`
	Deterministic bool          `json:"deterministic"`
	Duration      time.Duration `json:"duration"`
	Primitives    []Primitive   `json:"primitives,omitempty"`

	// DependsOn lists other shot IDs this shot's generation/cache depends
	// on (e.g. asset reuse), enabling incremental rebuild (section 4.6).
	DependsOn []ID `json:"dependsOn,omitempty"`
}
