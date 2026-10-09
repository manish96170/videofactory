package director

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/idgen"
	"github.com/manish96170/videofactory/project"
	"github.com/manish96170/videofactory/registry"
)

// FixtureDirectorVersion is FixtureDirector's self-reported version,
// recorded in PlanMetadata.
const FixtureDirectorVersion = "v1"

// FixtureDirector is a deterministic Director requiring no AI model —
// PHASES.md Phase 05's "deterministic fixture Director." It splits a
// prompt into sentences, each becoming one Shot, using fixed
// keyword/reading-pace heuristics (decompose.go) rather than any
// learned/AI judgment.
//
// If a PromptInterpreter is configured (WithPromptInterpreter),
// FixtureDirector uses its suggested Flow/shot hints where available,
// falling back to the deterministic heuristics for anything the
// interpreter didn't resolve — but FixtureDirector works correctly
// with none configured at all. An empty hint list allows fallback;
// a supplied hint with blank Text is invalid, not an instruction to
// discard that shot or fall back.
type FixtureDirector struct {
	flows      *registry.FlowRegistry
	framings   *registry.FramingRegistry
	primitives *registry.PrimitiveRegistry

	interpreter PromptInterpreter
	now         func() time.Time
}

// Option configures a FixtureDirector constructed with
// NewFixtureDirector.
type Option func(*FixtureDirector)

// WithPromptInterpreter configures an optional LLM-assisted hint
// source (PHASES.md Phase 05's "optional LLM adapter interface").
func WithPromptInterpreter(p PromptInterpreter) Option {
	return func(d *FixtureDirector) { d.interpreter = p }
}

// NewFixtureDirector creates a FixtureDirector backed by the given
// registries (typically registry.NewFlowRegistry() etc. — Phase 04).
func NewFixtureDirector(flows *registry.FlowRegistry, framings *registry.FramingRegistry, primitives *registry.PrimitiveRegistry, opts ...Option) *FixtureDirector {
	d := &FixtureDirector{flows: flows, framings: framings, primitives: primitives, now: time.Now}
	for _, opt := range opts {
		opt(d)
	}
	return d
}

// Plan implements Director. Hint Text is trimmed on a local copy, never
// in the interpreter-owned slice, and must not be blank. An AVATAR
// category or avatar keyword in Text always selects the DeveloperAvatar
// path and normalizes the category to AVATAR, even if a hint suggested
// another category. This conservative intent rule keeps category,
// primitives, and deterministic-vs-AI requirements consistent.
func (d *FixtureDirector) Plan(ctx context.Context, req PlanRequest) (PlanResult, error) {
	if err := ctx.Err(); err != nil {
		return PlanResult{}, err
	}
	if req.ProjectID.IsZero() {
		return PlanResult{}, fmt.Errorf("director: ProjectID must not be empty")
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return PlanResult{}, fmt.Errorf("director: Prompt must not be empty")
	}

	var interpretation PromptInterpretation
	if d.interpreter != nil {
		got, err := d.interpreter.Interpret(ctx, req.Prompt)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return PlanResult{}, ctxErr
		}
		if err != nil {
			return PlanResult{}, fmt.Errorf("director: prompt interpreter: %w", err)
		}
		interpretation = got
	}

	flow, err := d.selectFlow(req, interpretation)
	if err != nil {
		return PlanResult{}, err
	}
	framing, err := d.selectFraming(req, flow)
	if err != nil {
		return PlanResult{}, err
	}

	hints := interpretation.Shots
	if len(hints) == 0 {
		// An empty slice may still have interpreter-owned spare capacity.
		// Build fallback hints separately rather than append into it.
		hints = nil
		for _, s := range splitSentences(req.Prompt) {
			if err := ctx.Err(); err != nil {
				return PlanResult{}, err
			}
			hints = append(hints, ShotHint{Text: s, Category: inferCategory(s)})
		}
	}
	if len(hints) == 0 {
		return PlanResult{}, fmt.Errorf("director: prompt %q produced no shots", req.Prompt)
	}

	now := d.now()
	planID := domain.ID(idgen.New())

	shots := make([]domain.Shot, 0, len(hints))
	var assetReqs []AssetRequirement
	capSet := map[string]struct{}{}

	for i, hint := range hints {
		if err := ctx.Err(); err != nil {
			return PlanResult{}, err
		}
		hint.Text = strings.TrimSpace(hint.Text)
		if hint.Text == "" {
			return PlanResult{}, fmt.Errorf("director: shot hint %d: Text must not be empty or whitespace-only", i)
		}
		category := hint.Category
		if category == "" {
			category = inferCategory(hint.Text)
		}
		isAvatar := category == domain.ShotCategoryAvatar || mentionsAvatar(hint.Text)
		if isAvatar {
			category = domain.ShotCategoryAvatar
		}
		shotID := domain.ID(idgen.New())

		var primitives []domain.Primitive
		if isAvatar {
			primitives = []domain.Primitive{
				{
					ID:         domain.ID(idgen.New()),
					Type:       domain.PrimitiveTypeDeveloperAvatar,
					Properties: map[string]any{"script": hint.Text},
				},
			}
			assetReqs = append(assetReqs, AssetRequirement{
				ShotID:      shotID,
				Kind:        "generated-video",
				Description: fmt.Sprintf("developer avatar speaking: %q", hint.Text),
			})
		} else {
			primitives = []domain.Primitive{
				{ID: domain.ID(idgen.New()), Type: domain.PrimitiveTypeBackground, Properties: map[string]any{"color": "#101020"}},
				{ID: domain.ID(idgen.New()), Type: domain.PrimitiveTypeText, Properties: map[string]any{"text": hint.Text}},
			}
		}

		for _, p := range primitives {
			if caps, ok := d.primitives.Get(p.Type); ok {
				for _, tag := range caps.Tags {
					capSet[tag] = struct{}{}
				}
			}
		}

		shots = append(shots, domain.Shot{
			ID:            shotID,
			PlanID:        planID,
			Index:         i,
			Category:      category,
			Deterministic: !isAvatar,
			Duration:      estimateDuration(hint.Text),
			Primitives:    primitives,
		})
	}

	plan := domain.VideoPlan{
		ID:        planID,
		ProjectID: req.ProjectID,
		FlowID:    flow.ID,
		FramingID: framing.ID,
		Shots:     shots,
		CreatedAt: now,
	}

	if err := project.ValidatePlan(req.ProjectID, plan); err != nil {
		return PlanResult{}, fmt.Errorf("director: produced an invalid plan: %w", err)
	}

	caps := make([]string, 0, len(capSet))
	for tag := range capSet {
		caps = append(caps, tag)
	}
	sort.Strings(caps)

	if err := ctx.Err(); err != nil {
		return PlanResult{}, err
	}
	return PlanResult{
		Plan: plan,
		Metadata: PlanMetadata{
			Prompt:          req.Prompt,
			DirectorID:      "fixture",
			DirectorVersion: FixtureDirectorVersion,
			CreatedAt:       now,
		},
		AssetRequirements:    assetReqs,
		RequiredCapabilities: caps,
	}, nil
}

// selectFlow implements PHASES.md Phase 05's "flow selection": an
// explicit request override wins, then the PromptInterpreter's
// suggestion (if any), then a simple keyword heuristic over the whole
// prompt, defaulting to "code-tutorial".
func (d *FixtureDirector) selectFlow(req PlanRequest, interp PromptInterpretation) (domain.Flow, error) {
	id := req.FlowID
	if id.IsZero() {
		id = interp.SuggestedFlowID
	}
	if id.IsZero() {
		if mentionsAvatar(req.Prompt) {
			id = "developer-explainer"
		} else {
			id = "code-tutorial"
		}
	}
	flow, ok := d.flows.Get(id)
	if !ok {
		return domain.Flow{}, fmt.Errorf("director: unknown flow %q", id)
	}
	return flow, nil
}

// selectFraming implements PHASES.md Phase 05's "framing selection":
// an explicit request override wins, then the selected Flow's
// DefaultFramingID (Phase 04), then "fullscreen".
func (d *FixtureDirector) selectFraming(req PlanRequest, flow domain.Flow) (domain.Framing, error) {
	id := req.FramingID
	if id.IsZero() {
		id = flow.DefaultFramingID
	}
	if id.IsZero() {
		id = "fullscreen"
	}
	framing, ok := d.framings.Get(id)
	if !ok {
		return domain.Framing{}, fmt.Errorf("director: unknown framing %q", id)
	}
	return framing, nil
}

var _ Director = &FixtureDirector{}
