package changeset

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
)

// Validate snapshots and validates every operation, transforms a private copy,
// validates the complete result and calculates invalidation before exposing a
// handle. No caller state is modified even if a later operation fails.
func Validate(ctx context.Context, p domain.Project, proposal Proposal) (*Validated, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	base, hash, err := snapshotProject(p)
	if err != nil {
		return nil, err
	}
	if proposal.BaseHash != hash {
		return nil, ErrStale
	}
	cs, err := copyChangeSet(proposal.ChangeSet)
	if err != nil {
		return nil, err
	}
	if !handle(cs.ID) || !handle(cs.ProjectID) || cs.ProjectID != base.ID ||
		cs.Status != domain.ChangeSetProposed || !validTime(cs.CreatedAt) {
		return nil, ErrInvalid
	}
	switch cs.Source {
	case "manual", "director-chat", "review-feedback":
	default:
		return nil, ErrInvalid
	}
	cs.CreatedAt = cs.CreatedAt.UTC().Round(0)
	roots := make(map[domain.ID]bool)
	seen := make(map[string]bool)
	for _, c := range cs.Changes {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		root, key, err := mutate(&base, c)
		if err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, ErrInvalid
		}
		seen[key] = true
		if root != "" {
			roots[root] = true
		}
	}
	result, resultHash, err := snapshotProject(base)
	if err != nil {
		return nil, err
	}
	if hash == resultHash {
		return nil, ErrInvalid
	}
	changed := make([]domain.ID, 0, len(roots))
	for id := range roots {
		changed = append(changed, id)
	}
	sort.Slice(changed, func(i, j int) bool { return changed[i] < changed[j] })
	var shots []domain.Shot
	if result.Plan != nil {
		shots = result.Plan.Shots
	}
	invalidated, err := artifact.InvalidatedShots(shots, changed)
	if err != nil {
		return nil, ErrInvalid
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	s := &validatedState{result: result, preview: Preview{
		Proposal: Proposal{BaseHash: hash, ChangeSet: cs}, ResultHash: resultHash,
		ChangedRoots: changed, InvalidatedShots: invalidated,
		Impact: Impact{ChangedShots: len(changed), InvalidatedShots: len(invalidated)},
	}}
	s.record("VALIDATED")
	return &Validated{state: s}, nil
}

func copyChangeSet(cs domain.ChangeSet) (domain.ChangeSet, error) {
	if len(cs.Changes) == 0 || len(cs.Changes) > MaxChanges {
		return domain.ChangeSet{}, ErrInvalid
	}
	cs.Changes = append([]domain.Change(nil), cs.Changes...)
	for i := range cs.Changes {
		m, err := copyMap(cs.Changes[i].Payload)
		if err != nil {
			return domain.ChangeSet{}, err
		}
		cs.Changes[i].Payload = m
	}
	b, err := json.Marshal(cs)
	if err != nil || len(b) > MaxChanges*(MaxTextBytes+1024) {
		return domain.ChangeSet{}, ErrInvalid
	}
	return cs, nil
}

func mutate(p *domain.Project, c domain.Change) (root domain.ID, key string, err error) {
	if !handle(c.TargetID) {
		return "", "", ErrInvalid
	}
	if c.Op == OpSetProjectName {
		name, ok := c.Payload["name"].(string)
		if !ok || len(c.Payload) != 1 || c.TargetID != p.ID || !text(name, 256) {
			return "", "", ErrInvalid
		}
		p.Name = name
		return "", "project/name", nil
	}
	var property string
	switch c.Op {
	case OpSetText:
		property = "text"
	case OpSetColor:
		property = "color"
	case OpSetFontSize:
		property = "fontSize"
	default:
		return "", "", ErrUnsupported
	}
	shot, ok := c.Payload["shotId"].(string)
	if !ok || !handle(domain.ID(shot)) || len(c.Payload) != 2 || p.Plan == nil {
		return "", "", ErrInvalid
	}
	value, ok := c.Payload[property]
	if !ok {
		return "", "", ErrInvalid
	}
	switch property {
	case "text":
		s, ok := value.(string)
		if !ok || !text(s, MaxTextBytes) {
			return "", "", ErrInvalid
		}
	case "color":
		s, ok := value.(string)
		if !ok || !color(s) {
			return "", "", ErrInvalid
		}
	case "fontSize":
		n, ok := number(value)
		if !ok || n <= 0 || n > 512 {
			return "", "", ErrInvalid
		}
		value = n // Renderer consumes float64 or int, not json.Number.
	}
	for i := range p.Plan.Shots {
		s := &p.Plan.Shots[i]
		if s.ID != domain.ID(shot) {
			continue
		}
		for j := range s.Primitives {
			prim := &s.Primitives[j]
			if prim.ID != c.TargetID {
				continue
			}
			if prim.Type != domain.PrimitiveTypeText && !(property == "color" && prim.Type == domain.PrimitiveTypeBackground) {
				return "", "", ErrUnsupported
			}
			if reflect.DeepEqual(prim.Properties[property], value) {
				return "", shot + "/" + string(c.TargetID) + "/" + property, nil
			}
			if prim.Properties == nil {
				prim.Properties = make(map[string]any)
			}
			prim.Properties[property] = value
			// Refresh only identity-matching metadata; the authoritative primitive
			// is resolved first. Other visual elements are untouched.
			if p.Timeline != nil {
				for k := range p.Timeline.Elements {
					e := &p.Timeline.Elements[k]
					if e.ID == prim.ID && e.ShotID == s.ID {
						if e.EditableProperties == nil {
							e.EditableProperties = make(map[string]any)
						}
						e.EditableProperties[property] = value
					}
				}
			}
			return s.ID, shot + "/" + string(c.TargetID) + "/" + property, nil
		}
	}
	return "", "", ErrInvalid
}

func checkContext(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	return ctx.Err()
}

func handle(id domain.ID) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func validTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1 && t.Year() <= 9999 }

func text(s string, max int) bool {
	return len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && strings.TrimSpace(s) != ""
}

func color(s string) bool {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func number(v any) (float64, bool) {
	var n float64
	switch v := v.(type) {
	case float64:
		n = v
	case int:
		n = float64(v)
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}
