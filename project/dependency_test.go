package project

import (
	"errors"
	"testing"

	"github.com/manish96170/videofactory/domain"
)

func TestBuildDependencyGraphRejectsDanglingReference(t *testing.T) {
	shots := []domain.Shot{
		{ID: "a", DependsOn: []domain.ID{"missing"}},
	}
	if _, err := BuildDependencyGraph(shots); err == nil {
		t.Fatalf("expected error for dangling dependency reference")
	}
}

func TestBuildDependencyGraphRejectsDuplicateShotID(t *testing.T) {
	shots := []domain.Shot{
		{ID: "a"},
		{ID: "a"},
	}
	if _, err := BuildDependencyGraph(shots); err == nil {
		t.Fatalf("expected error for duplicate shot ID")
	}
}

func TestBuildDependencyGraphRejectsEmptyShotID(t *testing.T) {
	shots := []domain.Shot{{ID: ""}}
	if _, err := BuildDependencyGraph(shots); err == nil {
		t.Fatalf("expected error for empty shot ID")
	}
}

func TestBuildDependencyGraphDedupesRepeatedDependencyEdge(t *testing.T) {
	// b lists a twice in its own DependsOn; Dependents(a) must still
	// report b only once.
	shots := []domain.Shot{
		{ID: "a"},
		{ID: "b", DependsOn: []domain.ID{"a", "a"}},
	}
	g, err := BuildDependencyGraph(shots)
	if err != nil {
		t.Fatalf("BuildDependencyGraph: %v", err)
	}
	dependents := g.Dependents("a")
	if len(dependents) != 1 || dependents[0] != "b" {
		t.Fatalf("Dependents(a) = %v, want exactly [b]", dependents)
	}
}

func TestTopologicalOrderRespectsDependencies(t *testing.T) {
	// c depends on b, b depends on a: valid order must place a before b
	// before c.
	shots := []domain.Shot{
		{ID: "c", DependsOn: []domain.ID{"b"}},
		{ID: "a"},
		{ID: "b", DependsOn: []domain.ID{"a"}},
	}
	g, err := BuildDependencyGraph(shots)
	if err != nil {
		t.Fatalf("BuildDependencyGraph: %v", err)
	}

	order, err := g.TopologicalOrder()
	if err != nil {
		t.Fatalf("TopologicalOrder: %v", err)
	}

	pos := make(map[domain.ID]int, len(order))
	for i, id := range order {
		pos[id] = i
	}
	if pos["a"] > pos["b"] {
		t.Fatalf("expected a before b, got order %v", order)
	}
	if pos["b"] > pos["c"] {
		t.Fatalf("expected b before c, got order %v", order)
	}
	if len(order) != 3 {
		t.Fatalf("expected 3 shots in order, got %v", order)
	}
}

func TestTopologicalOrderDetectsCycle(t *testing.T) {
	shots := []domain.Shot{
		{ID: "a", DependsOn: []domain.ID{"b"}},
		{ID: "b", DependsOn: []domain.ID{"a"}},
	}
	g, err := BuildDependencyGraph(shots)
	if err != nil {
		t.Fatalf("BuildDependencyGraph: %v", err)
	}

	_, err = g.TopologicalOrder()
	if err == nil {
		t.Fatalf("expected a cycle error")
	}
	var cycleErr *CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
}

// TestCycleErrorExcludesAcyclicPrefix reproduces a cycle reached through
// a non-cyclic prefix (x -> a -> b -> a) and asserts the reported cycle
// contains only [a b a], not the leading x.
func TestCycleErrorExcludesAcyclicPrefix(t *testing.T) {
	shots := []domain.Shot{
		{ID: "x", DependsOn: []domain.ID{"a"}},
		{ID: "a", DependsOn: []domain.ID{"b"}},
		{ID: "b", DependsOn: []domain.ID{"a"}},
	}
	g, err := BuildDependencyGraph(shots)
	if err != nil {
		t.Fatalf("BuildDependencyGraph: %v", err)
	}

	_, err = g.TopologicalOrder()
	var cycleErr *CycleError
	if !errors.As(err, &cycleErr) {
		t.Fatalf("expected *CycleError, got %T: %v", err, err)
	}
	for _, id := range cycleErr.Cycle {
		if id == "x" {
			t.Fatalf("expected cycle to exclude the acyclic prefix 'x', got %v", cycleErr.Cycle)
		}
	}
	if len(cycleErr.Cycle) < 2 || cycleErr.Cycle[0] != cycleErr.Cycle[len(cycleErr.Cycle)-1] {
		t.Fatalf("expected cycle to start and end with the same ID to show closure, got %v", cycleErr.Cycle)
	}
}

func TestDependentsAndAffectedBy(t *testing.T) {
	// c depends on b, b depends on a, d depends on c.
	shots := []domain.Shot{
		{ID: "a"},
		{ID: "b", DependsOn: []domain.ID{"a"}},
		{ID: "c", DependsOn: []domain.ID{"b"}},
		{ID: "d", DependsOn: []domain.ID{"c"}},
		{ID: "unrelated"},
	}
	g, err := BuildDependencyGraph(shots)
	if err != nil {
		t.Fatalf("BuildDependencyGraph: %v", err)
	}

	direct := g.Dependents("a")
	if len(direct) != 1 || direct[0] != "b" {
		t.Fatalf("Dependents(a) = %v, want [b]", direct)
	}

	affected := g.AffectedBy("a")
	want := map[domain.ID]bool{"b": true, "c": true, "d": true}
	if len(affected) != len(want) {
		t.Fatalf("AffectedBy(a) = %v, want 3 entries matching %v", affected, want)
	}
	for _, id := range affected {
		if !want[id] {
			t.Fatalf("AffectedBy(a) included unexpected %q", id)
		}
	}
}
