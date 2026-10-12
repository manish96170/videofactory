package artifact

import (
	"errors"
	"fmt"
	"sort"

	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
)

// ErrUnknownRoot means a changed shot does not exist in the supplied graph.
var ErrUnknownRoot = errors.New("artifact: unknown changed root")

// InvalidatedShots returns changed roots plus all their downstream dependents,
// deduplicated and in lexical ID order (not execution/topological order). Empty
// roots return an empty slice, but still validate the entire graph. Every root
// must exist. This is a calculation only; no cache or project state is mutated.
//
// The function snapshots only IDs and DependsOn, the graph's consumed fields.
// It reuses project.BuildDependencyGraph, TopologicalOrder for cycle validation,
// and AffectedBy for the closure. Graph errors are wrapped without losing their
// identity (including *project.CycleError); errors return no partial result.
func InvalidatedShots(shots []domain.Shot, roots []domain.ID) ([]domain.ID, error) {
	snapshot := make([]domain.Shot, len(shots))
	known := make(map[domain.ID]bool, len(shots))
	for i, shot := range shots {
		snapshot[i] = domain.Shot{ID: shot.ID, DependsOn: append([]domain.ID(nil), shot.DependsOn...)}
		known[shot.ID] = true
	}
	roots = append([]domain.ID(nil), roots...)
	graph, err := project.BuildDependencyGraph(snapshot)
	if err != nil {
		return nil, fmt.Errorf("artifact: dependency graph: %w", err)
	}
	if _, err := graph.TopologicalOrder(); err != nil {
		return nil, fmt.Errorf("artifact: dependency graph: %w", err)
	}
	set := make(map[domain.ID]bool)
	for _, id := range roots {
		if !known[id] {
			return nil, ErrUnknownRoot
		}
		set[id] = true
	}
	for _, id := range graph.AffectedBy(roots...) {
		set[id] = true
	}
	result := make([]domain.ID, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result, nil
}
