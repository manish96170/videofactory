package artifact_test

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/manish96170/videofactory/artifact"
	"github.com/manish96170/videofactory/domain"
	"github.com/manish96170/videofactory/project"
)

func graphFixture() []domain.Shot {
	// Deliberately not lexical/topological input order. A diamond, a chain,
	// repeated edges, and an independent component exercise closure semantics.
	return []domain.Shot{
		{ID: "d", DependsOn: []domain.ID{"c", "b"}},
		{ID: "c", DependsOn: []domain.ID{"a"}},
		{ID: "x"},
		{ID: "a"},
		{ID: "e", DependsOn: []domain.ID{"d"}},
		{ID: "b", DependsOn: []domain.ID{"a", "a"}},
	}
}

func TestInvalidationRootsAndDownstream(t *testing.T) {
	cases := []struct {
		name        string
		roots, want []domain.ID
	}{
		{"root diamond", []domain.ID{"a"}, []domain.ID{"a", "b", "c", "d", "e"}},
		{"middle excludes ancestors", []domain.ID{"b"}, []domain.ID{"b", "d", "e"}},
		{"leaf included", []domain.ID{"e"}, []domain.ID{"e"}},
		{"independent root", []domain.ID{"x"}, []domain.ID{"x"}},
		{"overlapping roots", []domain.ID{"d", "b", "b", "c"}, []domain.ID{"b", "c", "d", "e"}},
		{"multiple components", []domain.ID{"x", "c"}, []domain.ID{"c", "d", "e", "x"}},
		{"no roots", nil, []domain.ID{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := artifact.InvalidatedShots(graphFixture(), tc.roots)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
	got, err := artifact.InvalidatedShots(nil, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("empty graph: %v, %v", got, err)
	}
}

func TestInvalidationInputOrderAndOwnership(t *testing.T) {
	shots, roots := graphFixture(), []domain.ID{"x", "a", "a"}
	wantShots, wantRoots := graphFixture(), []domain.ID{"x", "a", "a"}
	want := []domain.ID{"a", "b", "c", "d", "e", "x"}
	got, err := artifact.InvalidatedShots(shots, roots)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, %v", got, err)
	}
	if !reflect.DeepEqual(shots, wantShots) || !reflect.DeepEqual(roots, wantRoots) {
		t.Fatal("invalidation mutated input")
	}
	got[0] = "mutated output"
	if !reflect.DeepEqual(shots, wantShots) || !reflect.DeepEqual(roots, wantRoots) {
		t.Fatal("output aliases input")
	}
	got, err = artifact.InvalidatedShots(shots, roots)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("output mutation reached future calculations")
	}
	roots[0] = "changed root"
	shots[0].ID = "changed shot"
	shots[0].DependsOn[0] = "changed dependency"
	if !reflect.DeepEqual(got, want) {
		t.Fatal("input mutation reached returned result")
	}
	shots = graphFixture()
	for i, j := 0, len(shots)-1; i < j; i, j = i+1, j-1 {
		shots[i], shots[j] = shots[j], shots[i]
	}
	got, err = artifact.InvalidatedShots(shots, []domain.ID{"a", "x"})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("order-dependent result %v, %v", got, err)
	}
}

func TestInvalidationRejectsInvalidGraphs(t *testing.T) {
	cases := []struct {
		name  string
		shots []domain.Shot
		cycle []domain.ID
	}{
		{"empty ID", []domain.Shot{{}}, nil},
		{"duplicate ID", []domain.Shot{{ID: "a"}, {ID: "a"}}, nil},
		{"unknown dependency", []domain.Shot{{ID: "a", DependsOn: []domain.ID{"missing"}}}, nil},
		{"empty dependency", []domain.Shot{{ID: "a", DependsOn: []domain.ID{""}}}, nil},
		{"self cycle", []domain.Shot{{ID: "a", DependsOn: []domain.ID{"a"}}}, []domain.ID{"a", "a"}},
		{"cycle after prefix", []domain.Shot{
			{ID: "prefix", DependsOn: []domain.ID{"a"}},
			{ID: "a", DependsOn: []domain.ID{"b"}},
			{ID: "b", DependsOn: []domain.ID{"a"}},
		}, []domain.ID{"a", "b", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Invalid graph must fail even if the requested closure is empty.
			got, err := artifact.InvalidatedShots(tc.shots, nil)
			if err == nil || got != nil {
				t.Fatalf("invalid graph returned %v, %v", got, err)
			}
			var cycle *project.CycleError
			if tc.cycle != nil {
				if !errors.As(err, &cycle) || !reflect.DeepEqual(cycle.Cycle, tc.cycle) {
					t.Fatalf("lost project cycle error: %v", err)
				}
			} else {
				// Preserve the builder's actual validation error as an unwrap
				// cause rather than replacing it with unknown-root/miss behavior.
				_, builderErr := project.BuildDependencyGraph(tc.shots)
				if cause := errors.Unwrap(err); cause == nil || cause.Error() != builderErr.Error() {
					t.Fatalf("lost builder validation cause: %v", err)
				}
			}
		})
	}
	for _, roots := range [][]domain.ID{{"unknown"}, {""}, {"a", "unknown"}} {
		got, err := artifact.InvalidatedShots(graphFixture(), roots)
		if !errors.Is(err, artifact.ErrUnknownRoot) || got != nil {
			t.Fatalf("unknown roots: %v, %v", got, err)
		}
	}
}

func TestInvalidationConcurrentStableInputs(t *testing.T) {
	shots, roots := graphFixture(), []domain.ID{"b", "c"}
	want := []domain.ID{"b", "c", "d", "e"}
	var wg sync.WaitGroup
	for i := 0; i < 24; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := artifact.InvalidatedShots(shots, roots)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Errorf("concurrent invalidation: %v, %v", got, err)
			}
		}()
	}
	wg.Wait()
}
