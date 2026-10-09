package project

import (
	"errors"
	"fmt"

	"github.com/manish96170/videofactory/domain"
)

// Validate checks the structural invariants of a Project: required
// fields, Project/VideoPlan/Shot/Timeline linkage, dependency
// referential integrity and acyclicity, Timeline/VisualElement
// consistency, and Asset identity (PHASES.md Phase 01 "Timeline
// validation" / "Dependency representation").
//
// It does not validate business rules owned by later phases (flow/
// framing registry membership, provider eligibility, rendering
// feasibility, ...). It reports every problem it finds rather than
// stopping at the first one, joined with errors.Join.
func Validate(p domain.Project) error {
	var issues []error

	if p.ID.IsZero() {
		issues = append(issues, errors.New("project: ID must not be empty"))
	}
	if p.Name == "" {
		issues = append(issues, errors.New("project: Name must not be empty"))
	}

	var shots []domain.Shot
	if p.Plan != nil {
		issues = append(issues, validatePlan(p.ID, *p.Plan)...)
		shots = p.Plan.Shots
	}

	if p.Timeline != nil {
		if p.Timeline.ID.IsZero() {
			issues = append(issues, errors.New("timeline: ID must not be empty"))
		}
		if !p.ID.IsZero() && p.Timeline.ProjectID != p.ID {
			issues = append(issues, fmt.Errorf("timeline: ProjectID %q does not match Project.ID %q", p.Timeline.ProjectID, p.ID))
		}
		if err := ValidateTimeline(*p.Timeline, shots); err != nil {
			issues = append(issues, err)
		}
	}

	issues = append(issues, validateAssets(p.Assets)...)

	return errors.Join(issues...)
}

func validateAssets(assets []domain.Asset) []error {
	var issues []error
	seen := make(map[domain.ID]struct{}, len(assets))
	for _, a := range assets {
		if a.ID.IsZero() {
			issues = append(issues, errors.New("asset: ID must not be empty"))
			continue
		}
		if _, dup := seen[a.ID]; dup {
			issues = append(issues, fmt.Errorf("asset %q: duplicate asset ID", a.ID))
			continue
		}
		seen[a.ID] = struct{}{}

		if a.Kind == "" {
			issues = append(issues, fmt.Errorf("asset %q: Kind must not be empty", a.ID))
		}
		if a.URI == "" {
			issues = append(issues, fmt.Errorf("asset %q: URI must not be empty", a.ID))
		}
	}
	return issues
}

// ValidatePlan checks a VideoPlan's structural invariants on its own,
// without requiring a full domain.Project — useful for a Director
// (Phase 05) that produces a VideoPlan before it's attached to a
// Project's Plan field. projectID must be non-empty, and plan.ProjectID
// must match it. It otherwise applies the exact same checks Validate
// (above) applies to an embedded Plan, so a plan that passes here will
// also pass once embedded in a Project with a matching ID.
func ValidatePlan(projectID domain.ID, plan domain.VideoPlan) error {
	if projectID.IsZero() {
		return errors.New("project: ValidatePlan projectID must not be empty")
	}
	return errors.Join(validatePlan(projectID, plan)...)
}

func validatePlan(projectID domain.ID, plan domain.VideoPlan) []error {
	var issues []error

	if plan.ID.IsZero() {
		issues = append(issues, errors.New("plan: ID must not be empty"))
	}
	if plan.CreatedAt.IsZero() {
		issues = append(issues, errors.New("plan: CreatedAt must not be zero"))
	}
	if !projectID.IsZero() && plan.ProjectID != projectID {
		issues = append(issues, fmt.Errorf("plan: ProjectID %q does not match Project.ID %q", plan.ProjectID, projectID))
	}
	if plan.FlowID.IsZero() {
		issues = append(issues, errors.New("plan: FlowID must not be empty"))
	}
	if plan.FramingID.IsZero() {
		issues = append(issues, errors.New("plan: FramingID must not be empty"))
	}

	shotIDs := make(map[domain.ID]struct{}, len(plan.Shots))
	seenIndex := make(map[int]domain.ID, len(plan.Shots))
	for _, shot := range plan.Shots {
		if shot.ID.IsZero() {
			issues = append(issues, errors.New("shot: ID must not be empty"))
			continue
		}
		if _, dup := shotIDs[shot.ID]; dup {
			issues = append(issues, fmt.Errorf("shot %q: duplicate shot ID within plan", shot.ID))
			continue
		}
		shotIDs[shot.ID] = struct{}{}

		if shot.Index < 0 {
			issues = append(issues, fmt.Errorf("shot %q: Index must not be negative, got %d", shot.ID, shot.Index))
		} else if other, dup := seenIndex[shot.Index]; dup {
			issues = append(issues, fmt.Errorf("shot %q: Index %d duplicates shot %q's Index", shot.ID, shot.Index, other))
		} else {
			seenIndex[shot.Index] = shot.ID
		}

		if shot.Category == "" {
			issues = append(issues, fmt.Errorf("shot %q: Category must not be empty", shot.ID))
		}
		if shot.Duration <= 0 {
			issues = append(issues, fmt.Errorf("shot %q: Duration must be positive, got %v", shot.ID, shot.Duration))
		}

		primitiveIDs := make(map[domain.ID]struct{}, len(shot.Primitives))
		for _, prim := range shot.Primitives {
			if prim.ID.IsZero() {
				issues = append(issues, fmt.Errorf("shot %q: primitive ID must not be empty", shot.ID))
				continue
			}
			if _, dup := primitiveIDs[prim.ID]; dup {
				issues = append(issues, fmt.Errorf("shot %q: duplicate primitive ID %q", shot.ID, prim.ID))
				continue
			}
			primitiveIDs[prim.ID] = struct{}{}
			if prim.Type == "" {
				issues = append(issues, fmt.Errorf("shot %q: primitive %q: Type must not be empty", shot.ID, prim.ID))
			}
		}
	}

	for _, shot := range plan.Shots {
		if shot.ID.IsZero() {
			continue
		}
		if !plan.ID.IsZero() && shot.PlanID != plan.ID {
			issues = append(issues, fmt.Errorf("shot %q: PlanID %q does not match VideoPlan.ID %q", shot.ID, shot.PlanID, plan.ID))
		}
		dedupedDeps := make(map[domain.ID]struct{}, len(shot.DependsOn))
		for _, dep := range shot.DependsOn {
			if dep == shot.ID {
				issues = append(issues, fmt.Errorf("shot %q: depends on itself", shot.ID))
				continue
			}
			if _, ok := shotIDs[dep]; !ok {
				issues = append(issues, fmt.Errorf("shot %q: DependsOn references unknown shot %q", shot.ID, dep))
				continue
			}
			if _, dup := dedupedDeps[dep]; dup {
				issues = append(issues, fmt.Errorf("shot %q: DependsOn lists %q more than once", shot.ID, dep))
				continue
			}
			dedupedDeps[dep] = struct{}{}
		}
	}

	// Cycle detection: BuildDependencyGraph's own dangling-reference
	// error is redundant with the specific messages already appended
	// above, so only its cycle detection (surfaced via
	// TopologicalOrder) adds new information here.
	if graph, err := BuildDependencyGraph(plan.Shots); err == nil {
		if _, err := graph.TopologicalOrder(); err != nil {
			var cycleErr *CycleError
			if errors.As(err, &cycleErr) {
				issues = append(issues, err)
			}
		}
	}

	return issues
}

// ValidateTimeline checks a Timeline against the Shots it references
// (typically plan.Shots from the Timeline's Project):
//
//   - every TimelineEntry and VisualElement must reference a known shot
//   - every interval must be well-formed and non-negative
//     ([Start, End) with 0 <= Start < End)
//   - each shot may appear in at most one TimelineEntry
//   - a TimelineEntry's duration (End-Start) must equal its shot's
//     declared Duration — there is no separate trim/retiming concept in
//     the model yet, so the two must agree
//   - VisualElement IDs must be unique and non-empty
//   - every VisualElement's shot must actually be placed on the
//     timeline (have a TimelineEntry) — otherwise the element could
//     never be resolved to a project-relative timestamp via
//     EntryForShot/AbsoluteStart, breaking the element-to-shot/timeline
//     relationship (PHASES.md section 4.6.1)
//   - an element's shot-relative End must not exceed its shot's
//     declared Duration
//
// See DECISIONS.md ADR-007 for the shot-relative/project-relative
// timing convention this enforces.
func ValidateTimeline(timeline domain.Timeline, knownShots []domain.Shot) error {
	var issues []error

	shotsByID := make(map[domain.ID]domain.Shot, len(knownShots))
	for _, s := range knownShots {
		shotsByID[s.ID] = s
	}

	seenEntryShot := make(map[domain.ID]struct{}, len(timeline.Entries))
	for _, entry := range timeline.Entries {
		shot, ok := shotsByID[entry.ShotID]
		if !ok {
			issues = append(issues, fmt.Errorf("timeline entry: references unknown shot %q", entry.ShotID))
		}
		if _, dup := seenEntryShot[entry.ShotID]; dup {
			issues = append(issues, fmt.Errorf("timeline entry: shot %q placed more than once on the timeline", entry.ShotID))
		}
		seenEntryShot[entry.ShotID] = struct{}{}

		if entry.Start < 0 {
			issues = append(issues, fmt.Errorf("timeline entry for shot %q: Start must not be negative, got %v", entry.ShotID, entry.Start))
		}
		if entry.Start >= entry.End {
			issues = append(issues, fmt.Errorf("timeline entry for shot %q: Start (%v) must be < End (%v)", entry.ShotID, entry.Start, entry.End))
		} else if ok && entry.End-entry.Start != shot.Duration {
			issues = append(issues, fmt.Errorf("timeline entry for shot %q: placed duration (%v) does not match shot Duration (%v)", entry.ShotID, entry.End-entry.Start, shot.Duration))
		}
	}

	seenElementID := make(map[domain.ID]struct{}, len(timeline.Elements))
	for _, elem := range timeline.Elements {
		if elem.ID.IsZero() {
			issues = append(issues, errors.New("visual element: ID must not be empty"))
		} else if _, dup := seenElementID[elem.ID]; dup {
			issues = append(issues, fmt.Errorf("visual element %q: duplicate element ID", elem.ID))
		} else {
			seenElementID[elem.ID] = struct{}{}
		}

		shot, ok := shotsByID[elem.ShotID]
		if !ok {
			issues = append(issues, fmt.Errorf("visual element %q: references unknown shot %q", elem.ID, elem.ShotID))
		} else if _, placed := seenEntryShot[elem.ShotID]; !placed {
			issues = append(issues, fmt.Errorf("visual element %q: shot %q has no TimelineEntry, so the element cannot be resolved to a project-relative timestamp", elem.ID, elem.ShotID))
		}

		if elem.Start < 0 {
			issues = append(issues, fmt.Errorf("visual element %q: Start must not be negative, got %v", elem.ID, elem.Start))
		}
		if elem.Start >= elem.End {
			issues = append(issues, fmt.Errorf("visual element %q: Start (%v) must be < End (%v)", elem.ID, elem.Start, elem.End))
		}
		if ok && elem.End > shot.Duration {
			issues = append(issues, fmt.Errorf("visual element %q: End (%v) exceeds shot %q's Duration (%v)", elem.ID, elem.End, shot.ID, shot.Duration))
		}
	}

	return errors.Join(issues...)
}
