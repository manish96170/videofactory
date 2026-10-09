package project

import (
	"fmt"
	"strings"

	"github.com/manish96170/videofactory/domain"
)

// CycleError reports a dependency cycle found while building a
// DependencyGraph or computing a topological order. Cycle contains only
// the IDs that are actually part of the cycle (in traversal order, with
// the first ID repeated at the end to show closure), not any acyclic
// prefix that led to it.
type CycleError struct {
	Cycle []domain.ID
}

func (e *CycleError) Error() string {
	parts := make([]string, len(e.Cycle))
	for i, id := range e.Cycle {
		parts[i] = string(id)
	}
	return fmt.Sprintf("project: dependency cycle detected: %s", strings.Join(parts, " -> "))
}

// DependencyGraph is the shot dependency graph built from Shot.DependsOn
// (PHASES.md Phase 01 "Dependency representation"; section 4.6
// "incremental rebuild"). It only models relationships already present
// in a VideoPlan's shots; it has no awareness of caching, hashing, or
// rebuild scheduling, which arrive in Phase 10.
type DependencyGraph struct {
	// dependsOn maps a shot ID to the IDs it depends on, deduplicated
	// (a shot listing the same dependency twice in its own DependsOn
	// contributes one edge here).
	dependsOn map[domain.ID][]domain.ID
	// dependents maps a shot ID to the IDs that depend on it (the
	// reverse edges), used by Dependents/AffectedBy. Each dependent
	// appears at most once per key even if a shot lists the same
	// dependency more than once in its own DependsOn.
	dependents map[domain.ID][]domain.ID
	order      []domain.ID // insertion order, for deterministic output
}

// BuildDependencyGraph builds a DependencyGraph from shots. It returns
// an error if shots contains a duplicate, empty shot ID, or any shot
// depends on an ID not present in shots. Validate performs the same
// checks with more specific, per-shot error messages; this function's
// own checks exist so BuildDependencyGraph is safe to call directly
// (e.g. from tests or future callers) without going through Validate
// first.
func BuildDependencyGraph(shots []domain.Shot) (DependencyGraph, error) {
	g := DependencyGraph{
		dependsOn:  make(map[domain.ID][]domain.ID, len(shots)),
		dependents: make(map[domain.ID][]domain.ID, len(shots)),
	}

	known := make(map[domain.ID]struct{}, len(shots))
	for _, s := range shots {
		if s.ID.IsZero() {
			return DependencyGraph{}, fmt.Errorf("project: shot has an empty ID")
		}
		if _, dup := known[s.ID]; dup {
			return DependencyGraph{}, fmt.Errorf("project: duplicate shot ID %q", s.ID)
		}
		known[s.ID] = struct{}{}
	}

	dependentSeen := make(map[domain.ID]map[domain.ID]struct{}, len(shots))
	for _, s := range shots {
		g.order = append(g.order, s.ID)

		dedupedDeps := make([]domain.ID, 0, len(s.DependsOn))
		seenDep := make(map[domain.ID]struct{}, len(s.DependsOn))
		for _, dep := range s.DependsOn {
			if _, ok := known[dep]; !ok {
				return DependencyGraph{}, fmt.Errorf("project: shot %q depends on unknown shot %q", s.ID, dep)
			}
			if _, dup := seenDep[dep]; dup {
				continue // s already lists dep; keep dependsOn edge-deduplicated too
			}
			seenDep[dep] = struct{}{}
			dedupedDeps = append(dedupedDeps, dep)

			if dependentSeen[dep] == nil {
				dependentSeen[dep] = make(map[domain.ID]struct{})
			}
			if _, already := dependentSeen[dep][s.ID]; already {
				continue // s.ID already recorded as a dependent of dep
			}
			dependentSeen[dep][s.ID] = struct{}{}
			g.dependents[dep] = append(g.dependents[dep], s.ID)
		}
		g.dependsOn[s.ID] = dedupedDeps
	}

	return g, nil
}

// Dependents returns the IDs of shots that directly depend on id, i.e.
// shots whose Shot.DependsOn contains id. The result is in the order
// those shots appeared in the slice passed to BuildDependencyGraph, with
// each dependent listed at most once.
func (g DependencyGraph) Dependents(id domain.ID) []domain.ID {
	return append([]domain.ID(nil), g.dependents[id]...)
}

// AffectedBy returns the transitive closure of Dependents for every ID
// in ids: every shot that depends, directly or indirectly, on any of
// ids. This is the set a future incremental-rebuild step
// (PHASES.md section 4.6) would need to invalidate after ids change.
// ids themselves are not included unless they are also dependents of
// another changed ID.
func (g DependencyGraph) AffectedBy(ids ...domain.ID) []domain.ID {
	visited := make(map[domain.ID]struct{})
	var result []domain.ID

	var visit func(domain.ID)
	visit = func(id domain.ID) {
		for _, dep := range g.dependents[id] {
			if _, seen := visited[dep]; seen {
				continue
			}
			visited[dep] = struct{}{}
			result = append(result, dep)
			visit(dep)
		}
	}
	for _, id := range ids {
		visit(id)
	}

	return result
}

// TopologicalOrder returns shot IDs ordered so that every shot appears
// after everything it depends on. It returns a *CycleError if the graph
// contains a cycle; CycleError.Cycle contains only the cycle itself, not
// any acyclic path that led into it.
func (g DependencyGraph) TopologicalOrder() ([]domain.ID, error) {
	const (
		unvisited = 0
		visiting  = 1
		done      = 2
	)
	state := make(map[domain.ID]int, len(g.order))
	result := make([]domain.ID, 0, len(g.order))
	var path []domain.ID

	var visit func(domain.ID) error
	visit = func(id domain.ID) error {
		switch state[id] {
		case done:
			return nil
		case visiting:
			// id is already on the current path: the cycle is the
			// suffix of path starting at id's first occurrence, plus
			// id again to show closure. Any earlier, acyclic prefix of
			// path is not part of the cycle and must not be reported.
			start := 0
			for i, p := range path {
				if p == id {
					start = i
					break
				}
			}
			cycle := append(append([]domain.ID(nil), path[start:]...), id)
			return &CycleError{Cycle: cycle}
		}
		state[id] = visiting
		path = append(path, id)
		for _, dep := range g.dependsOn[id] {
			if err := visit(dep); err != nil {
				return err
			}
		}
		path = path[:len(path)-1]
		state[id] = done
		result = append(result, id)
		return nil
	}

	for _, id := range g.order {
		if err := visit(id); err != nil {
			return nil, err
		}
	}

	return result, nil
}
