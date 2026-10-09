package project

import (
	"strings"
	"testing"
	"time"

	"github.com/manish96170/videofactory/domain"
)

func TestNewProjectHasStableUniqueIDs(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a := New("demo-a", now)
	b := New("demo-b", now)

	if a.ID.IsZero() || b.ID.IsZero() {
		t.Fatalf("expected non-zero IDs, got a=%q b=%q", a.ID, b.ID)
	}
	if a.ID == b.ID {
		t.Fatalf("expected distinct IDs for distinct projects, got %q twice", a.ID)
	}
	if !a.CreatedAt.Equal(now) || !a.UpdatedAt.Equal(now) {
		t.Fatalf("expected CreatedAt/UpdatedAt to equal now, got %+v", a)
	}
}

func TestValidatePlanStandaloneMatchesEmbeddedValidation(t *testing.T) {
	now := time.Now()
	p := validProject(now)

	// A standalone ValidatePlan call on a valid plan must succeed...
	if err := ValidatePlan(p.ID, *p.Plan); err != nil {
		t.Fatalf("ValidatePlan() = %v, want nil", err)
	}

	// ...and must catch the same problems Validate(Project) does when
	// the plan is embedded, e.g. a mismatched ProjectID.
	badPlan := *p.Plan
	badPlan.ProjectID = NewID()
	if err := ValidatePlan(p.ID, badPlan); err == nil {
		t.Fatalf("expected ValidatePlan to reject a mismatched ProjectID")
	}

	// A plan that fails standalone must also fail once embedded.
	p.Plan = &badPlan
	if err := Validate(p); err == nil {
		t.Fatalf("expected Validate(Project) to also reject the same invalid plan once embedded")
	}
}

func TestValidatePlanRequiresExpectedProjectLinkage(t *testing.T) {
	for _, tc := range []struct {
		name       string
		expectedID domain.ID
		planID     domain.ID
		wantError  string
	}{
		{name: "matching", expectedID: "project", planID: "project"},
		{name: "mismatched", expectedID: "project", planID: "other-project", wantError: "does not match"},
		{name: "empty expected with linked plan", planID: "project", wantError: "projectID must not be empty"},
		{name: "both empty", wantError: "projectID must not be empty"},
		{name: "empty plan linkage", expectedID: "project", wantError: "does not match"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := *validProject(time.Now()).Plan
			plan.ProjectID = tc.planID
			err := ValidatePlan(tc.expectedID, plan)
			if tc.wantError == "" {
				if err != nil {
					t.Fatalf("ValidatePlan: %v", err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("ValidatePlan error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func validProject(now time.Time) domain.Project {
	projectID := NewID()
	planID := NewID()
	shotA := NewID()
	shotB := NewID()

	return domain.Project{
		ID:   projectID,
		Name: "demo",
		Plan: &domain.VideoPlan{
			ID:        planID,
			ProjectID: projectID,
			FlowID:    domain.ID("flow-developer-explainer"),
			FramingID: domain.ID("framing-fullscreen"),
			Shots: []domain.Shot{
				{ID: shotA, PlanID: planID, Index: 0, Category: domain.ShotCategoryCode, Duration: 2 * time.Second},
				{ID: shotB, PlanID: planID, Index: 1, Category: domain.ShotCategoryTerminal, Duration: 2 * time.Second, DependsOn: []domain.ID{shotA}},
			},
			CreatedAt: now,
		},
		Timeline: &domain.Timeline{
			ID:        NewID(),
			ProjectID: projectID,
			Entries: []domain.TimelineEntry{
				{ShotID: shotA, Start: 0, End: 2 * time.Second},
				{ShotID: shotB, Start: 2 * time.Second, End: 4 * time.Second},
			},
		},
		Assets: []domain.Asset{
			{ID: NewID(), Kind: domain.AssetKindImage, URI: "assets/frame-00.png"},
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestValidateAcceptsWellFormedProject(t *testing.T) {
	now := time.Now()
	if err := Validate(validProject(now)); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}

func TestValidateRejectsEmptyProjectIDAndName(t *testing.T) {
	err := Validate(domain.Project{})
	if err == nil {
		t.Fatalf("expected error for empty Project")
	}
}

func TestValidateRejectsPlanProjectIDMismatch(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Plan.ProjectID = NewID() // deliberately wrong
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for mismatched Plan.ProjectID")
	}
}

func TestValidateRejectsDuplicateShotIDs(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Plan.Shots[1].ID = p.Plan.Shots[0].ID
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for duplicate shot IDs")
	}
}

func TestValidateRejectsDanglingDependency(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Plan.Shots[1].DependsOn = []domain.ID{NewID()} // unknown shot
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for dangling dependency")
	}
}

func TestValidateRejectsSelfDependency(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Plan.Shots[0].DependsOn = []domain.ID{p.Plan.Shots[0].ID}
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for self dependency")
	}
}

func TestValidateRejectsDependencyCycle(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	// shot B already depends on shot A; make A depend on B too -> cycle.
	p.Plan.Shots[0].DependsOn = []domain.ID{p.Plan.Shots[1].ID}
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for dependency cycle")
	}
}

func TestValidateRejectsWrongShotPlanID(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Plan.Shots[0].PlanID = NewID()
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for mismatched Shot.PlanID")
	}
}

func TestValidateProjectWithoutPlanIsValid(t *testing.T) {
	now := time.Now()
	p := New("no-plan-yet", now)
	if err := Validate(p); err != nil {
		t.Fatalf("Validate() = %v, want nil for a project with no plan yet", err)
	}
}

func TestValidateTimelineAcceptsWellFormed(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	shotA, shotB := p.Plan.Shots[0].ID, p.Plan.Shots[1].ID

	timeline := domain.Timeline{
		ID:        NewID(),
		ProjectID: p.ID,
		Entries: []domain.TimelineEntry{
			{ShotID: shotA, Start: 0, End: 2 * time.Second},
			{ShotID: shotB, Start: 2 * time.Second, End: 4 * time.Second},
		},
	}
	if err := ValidateTimeline(timeline, p.Plan.Shots); err != nil {
		t.Fatalf("ValidateTimeline() = %v, want nil", err)
	}
}

func TestValidateTimelineRejectsUnknownShotAndBadInterval(t *testing.T) {
	now := time.Now()
	p := validProject(now)

	timeline := domain.Timeline{
		ID:        NewID(),
		ProjectID: p.ID,
		Entries: []domain.TimelineEntry{
			{ShotID: NewID(), Start: 0, End: 2 * time.Second},                          // unknown shot
			{ShotID: p.Plan.Shots[0].ID, Start: 5 * time.Second, End: 5 * time.Second}, // zero-length
		},
	}
	err := ValidateTimeline(timeline, p.Plan.Shots)
	if err == nil {
		t.Fatalf("expected ValidateTimeline to report errors")
	}
}

func TestValidateTimelineRejectsDuplicatePlacementOfSameShot(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	shotA := p.Plan.Shots[0].ID

	timeline := domain.Timeline{
		ID:        NewID(),
		ProjectID: p.ID,
		Entries: []domain.TimelineEntry{
			{ShotID: shotA, Start: 0, End: time.Second},
			{ShotID: shotA, Start: time.Second, End: 2 * time.Second}, // same shot placed twice
		},
	}
	if err := ValidateTimeline(timeline, p.Plan.Shots); err == nil {
		t.Fatalf("expected error for a shot placed more than once on the timeline")
	}
}

func TestValidateTimelineRejectsNegativeStart(t *testing.T) {
	now := time.Now()
	p := validProject(now)

	timeline := domain.Timeline{
		Entries: []domain.TimelineEntry{
			{ShotID: p.Plan.Shots[0].ID, Start: -time.Second, End: time.Second},
		},
	}
	if err := ValidateTimeline(timeline, p.Plan.Shots); err == nil {
		t.Fatalf("expected error for negative Start")
	}
}

func TestValidateTimelineRejectsElementExceedingShotDuration(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	shotA := p.Plan.Shots[0].ID // Duration: 2s

	timeline := domain.Timeline{
		Elements: []domain.VisualElement{
			{ID: NewID(), ShotID: shotA, Start: 0, End: 3 * time.Second}, // exceeds the shot's 2s duration
		},
	}
	if err := ValidateTimeline(timeline, p.Plan.Shots); err == nil {
		t.Fatalf("expected error for an element exceeding its shot's duration")
	}
}

func TestValidateTimelineRejectsDuplicateElementID(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	shotA := p.Plan.Shots[0].ID
	dup := NewID()

	timeline := domain.Timeline{
		Elements: []domain.VisualElement{
			{ID: dup, ShotID: shotA, Start: 0, End: time.Second},
			{ID: dup, ShotID: shotA, Start: time.Second, End: 2 * time.Second},
		},
	}
	if err := ValidateTimeline(timeline, p.Plan.Shots); err == nil {
		t.Fatalf("expected error for duplicate visual element IDs")
	}
}

func TestValidateTimelineRejectsElementOnUnplacedShot(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	shotA := p.Plan.Shots[0].ID

	// shotA is a known shot but has no TimelineEntry: the element can
	// never be resolved to a project-relative timestamp.
	timeline := domain.Timeline{
		Elements: []domain.VisualElement{
			{ID: NewID(), ShotID: shotA, Start: 0, End: time.Second},
		},
	}
	err := ValidateTimeline(timeline, p.Plan.Shots)
	if err == nil {
		t.Fatalf("expected error for an element whose shot has no TimelineEntry")
	}
}

func TestValidateTimelineRejectsEntryDurationMismatch(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	shotA := p.Plan.Shots[0].ID // Duration: 2s

	timeline := domain.Timeline{
		Entries: []domain.TimelineEntry{
			{ShotID: shotA, Start: 0, End: 5 * time.Second}, // shot is 2s, not 5s
		},
	}
	if err := ValidateTimeline(timeline, p.Plan.Shots); err == nil {
		t.Fatalf("expected error for a TimelineEntry whose duration doesn't match its shot's Duration")
	}
}

func TestValidateTimelineAcceptsElementOnPlacedShot(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	shotA := p.Plan.Shots[0].ID // Duration: 2s

	timeline := domain.Timeline{
		Entries: []domain.TimelineEntry{
			{ShotID: shotA, Start: 10 * time.Second, End: 12 * time.Second},
		},
		Elements: []domain.VisualElement{
			{ID: NewID(), ShotID: shotA, Start: 0, End: time.Second},
		},
	}
	if err := ValidateTimeline(timeline, p.Plan.Shots); err != nil {
		t.Fatalf("ValidateTimeline() = %v, want nil", err)
	}
}

func TestValidateRejectsEmbeddedTimelineProjectIDMismatch(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Timeline.ProjectID = NewID() // deliberately wrong
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for mismatched Timeline.ProjectID")
	}
}

func TestValidateRejectsNonPositiveShotDuration(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Plan.Shots[0].Duration = 0
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for non-positive shot Duration")
	}
}

func TestValidateRejectsEmptyShotCategory(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Plan.Shots[0].Category = ""
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for empty shot Category")
	}
}

func TestValidateRejectsDuplicateShotIndex(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Plan.Shots[1].Index = p.Plan.Shots[0].Index
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for duplicate shot Index")
	}
}

func TestValidateRejectsDuplicateAndEmptyPrimitiveID(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	dup := NewID()
	p.Plan.Shots[0].Primitives = []domain.Primitive{
		{ID: dup, Type: domain.PrimitiveTypeCodeEditor},
		{ID: dup, Type: domain.PrimitiveTypeText},
	}
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for duplicate primitive ID")
	}

	p2 := validProject(now)
	p2.Plan.Shots[0].Primitives = []domain.Primitive{{Type: domain.PrimitiveTypeCodeEditor}} // empty ID
	if err := Validate(p2); err == nil {
		t.Fatalf("expected error for empty primitive ID")
	}
}

func TestValidateRejectsEmptyFlowOrFramingID(t *testing.T) {
	now := time.Now()

	p := validProject(now)
	p.Plan.FlowID = ""
	if err := Validate(p); err == nil {
		t.Fatalf("expected error for empty FlowID")
	}

	p2 := validProject(now)
	p2.Plan.FramingID = ""
	if err := Validate(p2); err == nil {
		t.Fatalf("expected error for empty FramingID")
	}
}

func TestValidateRejectsAssetProblems(t *testing.T) {
	now := time.Now()

	emptyID := validProject(now)
	emptyID.Assets = []domain.Asset{{Kind: domain.AssetKindImage, URI: "a.png"}}
	if err := Validate(emptyID); err == nil {
		t.Fatalf("expected error for empty asset ID")
	}

	dupID := NewID()
	dup := validProject(now)
	dup.Assets = []domain.Asset{
		{ID: dupID, Kind: domain.AssetKindImage, URI: "a.png"},
		{ID: dupID, Kind: domain.AssetKindImage, URI: "b.png"},
	}
	if err := Validate(dup); err == nil {
		t.Fatalf("expected error for duplicate asset ID")
	}

	emptyKind := validProject(now)
	emptyKind.Assets = []domain.Asset{{ID: NewID(), URI: "a.png"}}
	if err := Validate(emptyKind); err == nil {
		t.Fatalf("expected error for empty asset Kind")
	}

	emptyURI := validProject(now)
	emptyURI.Assets = []domain.Asset{{ID: NewID(), Kind: domain.AssetKindImage}}
	if err := Validate(emptyURI); err == nil {
		t.Fatalf("expected error for empty asset URI")
	}
}

func TestValidateAcceptsWellFormedAssets(t *testing.T) {
	now := time.Now()
	p := validProject(now)
	p.Assets = append(p.Assets, domain.Asset{ID: NewID(), Kind: domain.AssetKindVideo, URI: "clip.mp4"})
	if err := Validate(p); err != nil {
		t.Fatalf("Validate() = %v, want nil", err)
	}
}
